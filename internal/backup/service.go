package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/storage"
	"github.com/viewdock/viewdock/internal/version"
)

const (
	// incompleteAfter is how long an upload without a manifest may sit at the
	// destination before retention treats it as abandoned.
	incompleteAfter = 6 * time.Hour
	// retryAfter spaces scheduled attempts after a failure.
	retryAfter = 30 * time.Minute
	// startupDelay keeps the first scheduled backup away from process start.
	startupDelay  = 2 * time.Minute
	createTimeout = 2 * time.Hour
)

// Service owns backup creation, retention, listing and the schedule.
type Service struct {
	DB      *sql.DB
	Dialect db.Dialect
	Cfg     config.Config
	Audit   *audit.Log
	Log     *slog.Logger
	// KeyID returns the fingerprint of the master key, recorded in manifests
	// so a restore can warn when the key differs. Never the key itself.
	KeyID func() string
	// Settings returns the current backup settings; nil uses EnvSettings.
	Settings func() Settings
	// Logical exports JSON Lines on SQLite too, instead of a VACUUM INTO
	// snapshot. PostgreSQL always uses the logical format.
	Logical bool

	mu          sync.Mutex
	running     bool
	started     bool
	historyRead bool
	lastAt      time.Time
	lastFailure time.Time
	nextRun     time.Time
	kick        chan struct{}
	now         func() time.Time
}

func New(sqlDB *sql.DB, cfg config.Config, aud *audit.Log, logger *slog.Logger) *Service {
	return &Service{
		DB: sqlDB, Dialect: db.DialectOf(sqlDB), Cfg: cfg, Audit: aud, Log: logger,
		kick: make(chan struct{}, 1),
	}
}

// LocalDir is where the local destination keeps backups.
func (s *Service) LocalDir() string { return filepath.Join(s.Cfg.ConfigDir, "backups") }

// StagingDir holds files while a backup is written or a restore is verified.
func (s *Service) StagingDir() string { return filepath.Join(s.Cfg.ConfigDir, ".backup-staging") }

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) settings() Settings {
	if s.Settings != nil {
		return s.Settings().normalized()
	}
	return EnvSettings(s.Cfg).normalized()
}

func (s *Service) keyID() string {
	if s.KeyID != nil {
		return s.KeyID()
	}
	return ""
}

// Store opens the currently configured backup destination and names its
// provider ("s3" or "local"), for connectivity probes.
func (s *Service) Store() (storage.Store, string, error) {
	set := s.settings()
	provider := "local"
	if set.Destination == DestinationS3 {
		provider = "s3"
	}
	store, _, err := s.destination(set)
	return store, provider, err
}

// destination opens the configured store. The label never contains
// credentials.
func (s *Service) destination(set Settings) (storage.Store, string, error) {
	if set.Destination == DestinationS3 {
		s3, err := storage.NewS3(set.S3)
		if err != nil {
			return nil, "", fmt.Errorf("%w: %w", ErrDestination, err)
		}
		store, err := storage.WithPrefix(s3, set.S3Prefix)
		if err != nil {
			return nil, "", fmt.Errorf("%w: %w", ErrDestination, err)
		}
		label := "s3://" + set.S3.Bucket
		if set.S3Prefix != "" {
			label += "/" + set.S3Prefix
		}
		return store, label, nil
	}
	store, err := storage.NewLocal(s.LocalDir())
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrDestination, err)
	}
	return store, store.Root, nil
}

func (s *Service) begin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	s.running = true
	return true
}

func (s *Service) end() {
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
}

// Create takes a backup now and applies retention. Only one backup runs at a
// time; a concurrent call returns ErrBusy.
func (s *Service) Create(ctx context.Context, trigger, actorID, ip string) (Manifest, error) {
	if !s.begin() {
		return Manifest{}, ErrBusy
	}
	defer s.end()
	set := s.settings()
	store, _, err := s.destination(set)
	if err != nil {
		return Manifest{}, err
	}
	m, err := s.create(ctx, store, trigger, actorID)
	if err != nil {
		return Manifest{}, err
	}
	s.mu.Lock()
	if m.CreatedAt.After(s.lastAt) {
		s.lastAt = m.CreatedAt
	}
	s.lastFailure = time.Time{}
	s.mu.Unlock()
	if err := s.prune(ctx, store, set.Retention); err != nil {
		s.log().Warn("backup retention failed", "category", "backup", "err", err)
	}
	s.Audit.Event(ctx, actorID, "backup.create", m.ID, ip, fmt.Sprintf("trigger=%s kind=%s bytes=%d", trigger, m.Kind, m.TotalSize()))
	s.wake()
	return m, nil
}

func (s *Service) create(ctx context.Context, store storage.Store, trigger, actorID string) (Manifest, error) {
	if s.DB == nil {
		return Manifest{}, errors.New("database is not open")
	}
	now := s.clock()
	id, err := newID(now)
	if err != nil {
		return Manifest{}, err
	}
	dir := filepath.Join(s.StagingDir(), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(dir)

	var st staged
	if s.Dialect == db.DialectPostgres || s.Logical {
		st, err = exportLogical(ctx, s.DB, s.Dialect, dir)
	} else {
		st, err = snapshotSQLite(ctx, s.DB, dir)
	}
	if err != nil {
		return Manifest{}, err
	}
	m := Manifest{
		Format: FormatName, FormatVersion: FormatVersion, ID: id, Kind: st.kind, Trigger: trigger,
		AppVersion: version.Version, Dialect: string(s.Dialect), SchemaVersion: st.schema,
		CreatedAt: now, CreatedBy: actorID, MasterKeyID: s.keyID(),
		Files: st.files, Tables: st.tables, Notice: MasterKeyNotice,
	}
	if m.Dialect == "" {
		m.Dialect = string(db.DialectSQLite)
	}
	if err := m.validate(); err != nil {
		return Manifest{}, fmt.Errorf("built manifest failed validation: %w", err)
	}
	var uploaded []string
	cleanup := func() {
		for _, key := range uploaded {
			_ = store.Delete(context.WithoutCancel(ctx), key)
		}
	}
	for _, f := range m.Files {
		key := id + "/" + f.Name
		if err := putFile(ctx, store, key, filepath.Join(dir, filepath.FromSlash(f.Name)), f.Size, contentType(f.Name)); err != nil {
			cleanup()
			return Manifest{}, fmt.Errorf("upload %s: %w", f.Name, err)
		}
		uploaded = append(uploaded, key)
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		cleanup()
		return Manifest{}, err
	}
	if err := store.Put(ctx, id+"/"+manifestName, bytes.NewReader(raw), int64(len(raw)), "application/json"); err != nil {
		cleanup()
		return Manifest{}, fmt.Errorf("upload manifest: %w", err)
	}
	return m, nil
}

func putFile(ctx context.Context, store storage.Store, key, path string, size int64, ct string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return store.Put(ctx, key, f, size, ct)
}

func contentType(name string) string {
	switch {
	case name == snapshotName:
		return "application/vnd.sqlite3"
	case strings.HasSuffix(name, ".jsonl"):
		return "application/x-ndjson"
	}
	return "application/octet-stream"
}

type backupKeys struct {
	id       string
	keys     []string
	complete bool
	newest   time.Time
}

func groupBackups(items []storage.ObjectInfo) map[string]*backupKeys {
	out := map[string]*backupKeys{}
	for _, it := range items {
		id, rest, ok := strings.Cut(it.Key, "/")
		if !ok || !ValidID(id) {
			continue
		}
		g := out[id]
		if g == nil {
			g = &backupKeys{id: id}
			out[id] = g
		}
		g.keys = append(g.keys, it.Key)
		if rest == manifestName {
			g.complete = true
		}
		if it.LastModified.After(g.newest) {
			g.newest = it.LastModified
		}
	}
	return out
}

func listAll(ctx context.Context, store storage.Store) ([]storage.ObjectInfo, error) {
	lister, ok := store.(storage.Lister)
	if !ok {
		return nil, storage.ErrListUnsupported
	}
	return lister.List(ctx, "")
}

// prune keeps the newest keep complete backups and removes abandoned partial
// uploads. IDs start with their UTC creation time, so they sort by age.
func (s *Service) prune(ctx context.Context, store storage.Store, keep int) error {
	items, err := listAll(ctx, store)
	if err != nil {
		return err
	}
	groups := groupBackups(items)
	var complete []string
	for id, g := range groups {
		if g.complete {
			complete = append(complete, id)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(complete)))
	var errs []error
	if keep > 0 && len(complete) > keep {
		for _, id := range complete[keep:] {
			if err := deleteKeys(ctx, store, groups[id]); err != nil {
				errs = append(errs, err)
				continue
			}
			s.Audit.Event(ctx, "", "backup.prune", id, "", fmt.Sprintf("retention=%d", keep))
		}
	}
	cutoff := s.clock().Add(-incompleteAfter)
	for _, g := range groups {
		if !g.complete && g.newest.Before(cutoff) {
			if err := deleteKeys(ctx, store, g); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// deleteKeys removes the manifest first, so an interrupted delete leaves an
// incomplete backup that retention cleans up later rather than a listed one
// with missing files.
func deleteKeys(ctx context.Context, store storage.Store, g *backupKeys) error {
	keys := append([]string(nil), g.keys...)
	sort.SliceStable(keys, func(i, j int) bool {
		return strings.HasSuffix(keys[i], "/"+manifestName) && !strings.HasSuffix(keys[j], "/"+manifestName)
	})
	for _, key := range keys {
		if err := store.Delete(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

// List returns complete backups at the configured destination, newest first.
func (s *Service) List(ctx context.Context) ([]Manifest, error) {
	store, _, err := s.destination(s.settings())
	if err != nil {
		return nil, err
	}
	return listManifests(ctx, store, s.log())
}

func listManifests(ctx context.Context, store storage.Store, logger *slog.Logger) ([]Manifest, error) {
	items, err := listAll(ctx, store)
	if err != nil {
		return nil, err
	}
	out := []Manifest{}
	for id, g := range groupBackups(items) {
		if !g.complete {
			continue
		}
		m, err := loadManifest(ctx, store, id)
		if err != nil {
			logger.Warn("unreadable backup manifest", "category", "backup", "id", id, "err", err)
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// Manifest loads one backup's manifest.
func (s *Service) Manifest(ctx context.Context, id string) (Manifest, error) {
	if !ValidID(id) {
		return Manifest{}, ErrInvalidID
	}
	store, _, err := s.destination(s.settings())
	if err != nil {
		return Manifest{}, err
	}
	return loadManifest(ctx, store, id)
}

// Delete removes every object of a backup.
func (s *Service) Delete(ctx context.Context, id, actorID, ip string) error {
	if !ValidID(id) {
		return ErrInvalidID
	}
	store, _, err := s.destination(s.settings())
	if err != nil {
		return err
	}
	lister, ok := store.(storage.Lister)
	if !ok {
		return storage.ErrListUnsupported
	}
	items, err := lister.List(ctx, id+"/")
	if err != nil {
		return err
	}
	g := groupBackups(items)[id]
	if g == nil || !g.complete {
		return ErrNotFound
	}
	if err := deleteKeys(ctx, store, g); err != nil {
		return err
	}
	s.Audit.Event(ctx, actorID, "backup.delete", id, ip, "")
	return nil
}

// WriteArchive streams a backup as a gzip-compressed tar containing
// <id>/manifest.json followed by every file, the same layout as the local
// destination, so an extracted archive can be restored from --dir.
func (s *Service) WriteArchive(ctx context.Context, id string, w io.Writer) error {
	store, _, err := s.destination(s.settings())
	if err != nil {
		return err
	}
	m, err := loadManifest(ctx, store, id)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: id + "/" + manifestName, Mode: 0o600, Size: int64(len(raw)), ModTime: m.CreatedAt, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err := tw.Write(raw); err != nil {
		return err
	}
	for _, f := range m.Files {
		if err := copyToTar(ctx, tw, store, id, f, m.CreatedAt); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func copyToTar(ctx context.Context, tw *tar.Writer, store storage.Store, id string, f File, mod time.Time) error {
	obj, err := store.Get(ctx, id+"/"+f.Name)
	if err != nil {
		return err
	}
	defer obj.Body.Close()
	if err := tw.WriteHeader(&tar.Header{Name: id + "/" + f.Name, Mode: 0o600, Size: f.Size, ModTime: mod, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	n, err := io.Copy(tw, io.LimitReader(obj.Body, f.Size))
	if err != nil {
		return err
	}
	if n != f.Size {
		return fmt.Errorf("%w: %s is shorter than its manifest entry", ErrChecksum, f.Name)
	}
	return nil
}

// Validate checks a stored backup against its manifest and the running
// database without changing anything.
func (s *Service) Validate(ctx context.Context, id, actorID, ip string) (Report, error) {
	if !ValidID(id) {
		return Report{}, ErrInvalidID
	}
	store, _, err := s.destination(s.settings())
	if err != nil {
		return Report{}, err
	}
	path := ""
	if s.Dialect != db.DialectPostgres {
		path = s.Cfg.DatabasePath
	}
	rep, err := Validate(ctx, store, id, s.DB, s.Dialect, path, s.keyID())
	if err != nil {
		return Report{}, err
	}
	s.Audit.Event(ctx, actorID, "backup.validate", id, ip, fmt.Sprintf("valid=%t problems=%d", rep.Valid, len(rep.Problems)))
	return rep, nil
}

// CheckDestination verifies the configured destination is reachable.
func (s *Service) CheckDestination(ctx context.Context) error {
	store, _, err := s.destination(s.settings())
	if err != nil {
		return err
	}
	if c, ok := store.(storage.Checker); ok {
		if err := c.Check(ctx); err != nil {
			return fmt.Errorf("%w: %w", ErrDestination, err)
		}
	}
	return nil
}

// Status is the scheduler and destination summary shown in the admin UI.
type Status struct {
	Destination   string     `json:"destination"`
	Location      string     `json:"location"`
	ScheduleHours int        `json:"schedule_hours"`
	Retention     int        `json:"retention"`
	Running       bool       `json:"running"`
	NextRun       *time.Time `json:"next_run,omitempty"`
	LastBackupAt  *time.Time `json:"last_backup_at,omitempty"`
	LastFailureAt *time.Time `json:"last_failure_at,omitempty"`
	Dialect       string     `json:"dialect"`
	MasterKeyID   string     `json:"master_key_id,omitempty"`
	Notice        string     `json:"notice"`
}

func (s *Service) Status() Status {
	set := s.settings()
	_, label, err := s.destination(set)
	if err != nil {
		label = ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{
		Destination: set.Destination, Location: label, ScheduleHours: set.ScheduleHours, Retention: set.Retention,
		Running: s.running, Dialect: string(s.Dialect), MasterKeyID: s.keyID(), Notice: MasterKeyNotice,
	}
	if st.Dialect == "" {
		st.Dialect = string(db.DialectSQLite)
	}
	if !s.nextRun.IsZero() {
		t := s.nextRun
		st.NextRun = &t
	}
	if !s.lastAt.IsZero() {
		t := s.lastAt
		st.LastBackupAt = &t
	}
	if !s.lastFailure.IsZero() {
		t := s.lastFailure
		st.LastFailureAt = &t
	}
	return st
}

// Start runs the scheduler until ctx ends. It is safe to call once per
// process; later calls are ignored.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	go s.loop(ctx)
}

// Reschedule re-reads settings and destination history, for example after
// an administrator changes a backup setting.
func (s *Service) Reschedule() {
	s.mu.Lock()
	s.historyRead = false
	s.mu.Unlock()
	s.wake()
}

func (s *Service) wake() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *Service) loop(ctx context.Context) {
	for {
		wait, enabled := s.plan(ctx)
		var fire <-chan time.Time
		var timer *time.Timer
		if enabled {
			timer = time.NewTimer(wait)
			fire = timer.C
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-s.kick:
			if timer != nil {
				timer.Stop()
			}
		case <-fire:
			s.runScheduled(ctx)
		}
	}
}

// plan returns how long to wait before the next scheduled backup. The last
// backup time comes from the destination once, then from memory.
func (s *Service) plan(ctx context.Context) (time.Duration, bool) {
	set := s.settings()
	now := s.clock()
	if set.ScheduleHours <= 0 {
		s.mu.Lock()
		s.nextRun = time.Time{}
		s.mu.Unlock()
		return 0, false
	}
	s.mu.Lock()
	needHistory := !s.historyRead
	s.mu.Unlock()
	if needHistory {
		latest, err := s.latestBackupTime(ctx, set)
		if err != nil {
			s.log().Warn("backup schedule could not read destination", "category", "backup", "err", err)
			s.mu.Lock()
			s.nextRun = now.Add(retryAfter)
			s.mu.Unlock()
			return retryAfter, true
		}
		s.mu.Lock()
		s.historyRead = true
		if latest.After(s.lastAt) {
			s.lastAt = latest
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	interval := time.Duration(set.ScheduleHours) * time.Hour
	next := now.Add(startupDelay)
	if !s.lastAt.IsZero() {
		next = s.lastAt.Add(interval)
	}
	if !s.lastFailure.IsZero() && s.lastFailure.Add(retryAfter).After(next) {
		next = s.lastFailure.Add(retryAfter)
	}
	if earliest := now.Add(time.Second); next.Before(earliest) {
		next = earliest
	}
	s.nextRun = next
	return next.Sub(now), true
}

func (s *Service) latestBackupTime(ctx context.Context, set Settings) (time.Time, error) {
	store, _, err := s.destination(set)
	if err != nil {
		return time.Time{}, err
	}
	items, err := listAll(ctx, store)
	if err != nil {
		return time.Time{}, err
	}
	var latest time.Time
	for id, g := range groupBackups(items) {
		if !g.complete {
			continue
		}
		if t, err := time.Parse("20060102T150405Z", id[3:19]); err == nil && t.After(latest) {
			latest = t
		}
	}
	return latest, nil
}

func (s *Service) runScheduled(ctx context.Context) {
	runCtx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()
	m, err := s.Create(runCtx, TriggerScheduled, "", "")
	switch {
	case err == nil:
		s.log().Info("scheduled backup complete", "category", "backup", "id", m.ID, "bytes", m.TotalSize())
	case errors.Is(err, ErrBusy):
	default:
		s.mu.Lock()
		s.lastFailure = s.clock()
		s.mu.Unlock()
		s.log().Error("scheduled backup failed", "category", "backup", "err", err)
		s.Audit.Event(ctx, "", "backup.failed", "", "", "trigger=scheduled")
	}
}
