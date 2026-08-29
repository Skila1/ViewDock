package scan

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/library"
	"strconv"

	"github.com/viewdock/viewdock/internal/media"
)

// SizeStable is how long a file's size must hold still before probe.
// Tests may set this to 0.
var SizeStable = 30 * time.Second

// PollInterval is used when fsnotify is unavailable or the path looks remote.
var PollInterval = 120 * time.Second

const catalogueBatch = 24

type Scanner struct {
	DB     *sql.DB
	Libs   *library.Service
	Prober ffmpeg.Prober
	// OnIdle runs after a library scan finishes (success or failure).
	OnIdle func()

	mu   sync.Mutex
	runs map[string]bool
}

// New constructs a Scanner. libs may be nil only in parser-only tests.
func New(db *sql.DB, libs *library.Service, prober ffmpeg.Prober) *Scanner {
	return &Scanner{DB: db, Libs: libs, Prober: prober, runs: map[string]bool{}}
}

var _ library.ScanStart = (*Scanner)(nil)

func (s *Scanner) StartScan(ctx context.Context, libraryID string) (string, error) {
	if s.Libs != nil {
		if _, err := s.Libs.Get(ctx, libraryID); err != nil {
			return "", err
		}
	} else if err := s.libraryExists(ctx, libraryID); err != nil {
		return "", err
	}
	s.mu.Lock()
	if s.runs[libraryID] {
		s.mu.Unlock()
		var existing string
		_ = s.DB.QueryRowContext(ctx, `
			SELECT id FROM scan_runs WHERE library_id = ? AND status = 'running' ORDER BY started_at DESC LIMIT 1
		`, libraryID).Scan(&existing)
		if existing != "" {
			return existing, nil
		}
	}
	s.runs[libraryID] = true
	s.mu.Unlock()

	id := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO scan_runs(id, library_id, status, started_at, files_seen, files_added, error)
		VALUES (?, ?, 'running', ?, 0, 0, '')
	`, id, libraryID, now)
	if err != nil {
		s.mu.Lock()
		delete(s.runs, libraryID)
		s.mu.Unlock()
		return "", err
	}
	go s.runScan(libraryID, id)
	return id, nil
}

func (s *Scanner) libraryExists(ctx context.Context, id string) error {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM libraries WHERE id = ?`, id).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return library.ErrNotFound
	}
	return err
}

func (s *Scanner) runScan(libraryID, runID string) {
	ctx := context.Background()
	defer func() {
		s.mu.Lock()
		delete(s.runs, libraryID)
		s.mu.Unlock()
	}()
	seen, added, err := s.scanLibrary(ctx, libraryID)
	status, errText := "ok", ""
	if err != nil {
		status, errText = "failed", err.Error()
	}
	_, _ = s.DB.ExecContext(ctx, `
		UPDATE scan_runs SET status = ?, finished_at = ?, files_seen = ?, files_added = ?, error = ?
		WHERE id = ?
	`, status, time.Now().UTC().Format(time.RFC3339), seen, added, errText, runID)
	if s.OnIdle != nil {
		s.OnIdle()
	}
}

func (s *Scanner) scanLibrary(ctx context.Context, libraryID string) (seen, added int, err error) {
	root, err := s.rootOf(ctx, libraryID)
	if err != nil {
		return 0, 0, err
	}
	if _, err := os.Stat(root); err != nil {
		s.markLibraryOffline(ctx, libraryID)
		return 0, 0, err
	}

	var files []foundFile
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && ShouldSkip(rel) {
				return filepath.SkipDir
			}
			if strings.EqualFold(d.Name(), ".viewdock-staging") {
				return filepath.SkipDir
			}
			return nil
		}
		if ShouldSkip(rel) || !IsVideo(rel) {
			return nil
		}
		if err := library.ContainsPath(root, path); err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, foundFile{abs: path, rel: rel, info: info})
		return nil
	})
	if err != nil {
		return 0, 0, err
	}

	seenRel := map[string]bool{}
	for i := 0; i < len(files); i += catalogueBatch {
		end := i + catalogueBatch
		if end > len(files) {
			end = len(files)
		}
		n, err := s.catalogueBatch(ctx, libraryID, root, files[i:end])
		added += n
		if err != nil {
			return seen, added, err
		}
		for _, f := range files[i:end] {
			seenRel[f.rel] = true
		}
	}
	seen = len(files)
	s.markUnseen(ctx, libraryID, root, seenRel)

	for _, f := range files {
		s.probeWhenStable(ctx, libraryID, f.abs, f.rel)
	}
	return seen, added, nil
}

type foundFile struct {
	abs, rel string
	info     os.FileInfo
}

func (s *Scanner) catalogueBatch(ctx context.Context, libraryID, root string, files []foundFile) (int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	added := 0
	for _, f := range files {
		n, err := s.upsertFile(ctx, tx, libraryID, root, f.abs, f.rel, f.info)
		if err != nil {
			return added, err
		}
		added += n
	}
	return added, tx.Commit()
}

func (s *Scanner) upsertFile(ctx context.Context, tx *sql.Tx, libraryID, root, abs, rel string, info os.FileInfo) (int, error) {
	parsed := Parse(rel)
	now := time.Now().UTC().Format(time.RFC3339)
	mtime := info.ModTime().UTC().Format(time.RFC3339)
	kind := parsed.Kind
	if kind == KindUnknown && !parsed.Skip {
		kind = KindMovie
		if parsed.Confidence == "" {
			parsed.Confidence = ConfLow
		}
	}
	if parsed.Skip {
		return 0, nil
	}

	var existing string
	_ = tx.QueryRowContext(ctx, `SELECT id FROM media_files WHERE library_id = ? AND rel_path = ?`, libraryID, rel).Scan(&existing)
	added := 0
	fileID := existing
	if fileID == "" {
		fileID = uuid.NewString()
		added = 1
		_, err := tx.ExecContext(ctx, `
			INSERT INTO media_files(id, library_id, rel_path, abs_path, size_bytes, inode, mtime, kind,
				extra_kind, probe_status, probe_error, probed_at, availability, duration_ms, container,
				video_codec, audio_codec, width, height, created_at, updated_at, identity_hint, parse_confidence)
			VALUES (?, ?, ?, ?, ?, '', ?, ?, ?, 'pending', '', '', 'online', 0, '', '', '', 0, 0, ?, ?, ?, ?)
		`, fileID, libraryID, rel, abs, info.Size(), mtime, kind, parsed.ExtraKind, now, now, parsed.Hint, parsed.Confidence)
		if err != nil {
			return 0, err
		}
	} else {
		_, err := tx.ExecContext(ctx, `
			UPDATE media_files SET abs_path = ?, size_bytes = ?, mtime = ?, kind = ?, extra_kind = ?,
				identity_hint = ?, parse_confidence = ?, availability = 'online', updated_at = ?
			WHERE id = ?
		`, abs, info.Size(), mtime, kind, parsed.ExtraKind, parsed.Hint, parsed.Confidence, now, fileID)
		if err != nil {
			return 0, err
		}
	}

	switch kind {
	case KindMovie:
		movieID, err := s.ensureMovie(ctx, tx, libraryID, parsed, now)
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE media_files SET movie_id = ? WHERE id = ?`, movieID, fileID); err != nil {
			return 0, err
		}
	case KindEpisode:
		if err := s.ensureEpisodes(ctx, tx, libraryID, fileID, parsed, now); err != nil {
			return 0, err
		}
	case KindExtra:
		if movieID, err := s.guessExtraMovie(ctx, tx, libraryID, rel, parsed, now); err == nil && movieID != "" {
			_, _ = tx.ExecContext(ctx, `UPDATE media_files SET movie_id = ? WHERE id = ?`, movieID, fileID)
		}
	}
	s.enqueueMatch(ctx, tx, kind, libraryID, parsed)
	return added, nil
}

func (s *Scanner) ensureMovie(ctx context.Context, tx *sql.Tx, libraryID string, p ParseResult, now string) (string, error) {
	var id string
	q := `SELECT id FROM movies WHERE library_id = ? AND lower(title) = lower(?)`
	args := []any{libraryID, p.Title}
	if p.Year > 0 {
		q += ` AND year = ?`
		args = append(args, p.Year)
	} else {
		q += ` AND year IS NULL`
	}
	err := tx.QueryRowContext(ctx, q, args...).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = uuid.NewString()
	review := boolInt(p.NeedsReview)
	var year any
	if p.Year > 0 {
		year = p.Year
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO movies(id, library_id, title, year, sort_title, overview, metadata_source, unmatched, needs_review, hint_mismatch, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, '', 'filename', 1, ?, 0, ?, ?)
	`, id, libraryID, p.Title, year, sortTitle(p.Title), review, now, now)
	if err != nil {
		return "", err
	}
	_ = library.UpsertFTS(ctx, tx, "movie", id, p.Title, p.Year, "")
	return id, nil
}

func (s *Scanner) ensureEpisodes(ctx context.Context, tx *sql.Tx, libraryID, fileID string, p ParseResult, now string) error {
	var seriesID string
	q := `SELECT id FROM series WHERE library_id = ? AND lower(title) = lower(?)`
	args := []any{libraryID, p.Title}
	if p.Year > 0 {
		q += ` AND year = ?`
		args = append(args, p.Year)
	}
	err := tx.QueryRowContext(ctx, q, args...).Scan(&seriesID)
	if errors.Is(err, sql.ErrNoRows) {
		seriesID = uuid.NewString()
		var year any
		if p.Year > 0 {
			year = p.Year
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO series(id, library_id, title, year, sort_title, overview, metadata_source, unmatched, needs_review, hint_mismatch, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, '', 'filename', 1, ?, 0, ?, ?)
		`, seriesID, libraryID, p.Title, year, sortTitle(p.Title), boolInt(p.NeedsReview), now, now)
		if err != nil {
			return err
		}
		_ = library.UpsertFTS(ctx, tx, "series", seriesID, p.Title, p.Year, "")
	} else if err != nil {
		return err
	}

	season := p.Season
	var seasonID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM seasons WHERE series_id = ? AND number = ?`, seriesID, season).Scan(&seasonID)
	if errors.Is(err, sql.ErrNoRows) {
		seasonID = uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO seasons(id, series_id, number, title) VALUES (?, ?, ?, '')`, seasonID, seriesID, season); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	eps := p.Episodes
	if len(eps) == 0 {
		eps = []int{0}
	}
	for _, n := range eps {
		var epID string
		err := tx.QueryRowContext(ctx, `SELECT id FROM episodes WHERE series_id = ? AND season = ? AND number = ?`, seriesID, season, n).Scan(&epID)
		if errors.Is(err, sql.ErrNoRows) {
			epID = uuid.NewString()
			title := "Episode " + itoa(n)
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO episodes(id, series_id, season_id, season, number, title, overview, intro_source)
				VALUES (?, ?, ?, ?, ?, ?, '', '')
			`, epID, seriesID, seasonID, season, n, title); err != nil {
				return err
			}
			_ = library.UpsertFTS(ctx, tx, "episode", epID, p.Title+" "+title, p.Year, "")
		} else if err != nil {
			return err
		}
		_, _ = tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO media_file_episodes(media_file_id, episode_id) VALUES (?, ?)
		`, fileID, epID)
	}
	return nil
}

func (s *Scanner) guessExtraMovie(ctx context.Context, tx *sql.Tx, libraryID, rel string, p ParseResult, now string) (string, error) {
	dir := filepath.ToSlash(filepath.Dir(rel))
	for dir != "" && dir != "." {
		base := filepath.Base(dir)
		if extraFolders[strings.ToLower(base)] != "" {
			dir = filepath.ToSlash(filepath.Dir(dir))
			continue
		}
		parent := Parse(base + ".mkv")
		if parent.Title != "" && parent.Year > 0 {
			return s.ensureMovie(ctx, tx, libraryID, parent, now)
		}
		break
	}
	if p.Title != "" && p.Year > 0 {
		return s.ensureMovie(ctx, tx, libraryID, ParseResult{Title: p.Title, Year: p.Year, Confidence: p.Confidence}, now)
	}
	return "", nil
}

func (s *Scanner) enqueueMatch(ctx context.Context, tx *sql.Tx, kind, libraryID string, p ParseResult) {
	if p.ExtraKind != "" || kind == KindExtra {
		return
	}
	itemKind, itemID := "", ""
	switch kind {
	case KindMovie:
		var id string
		q := `SELECT id FROM movies WHERE library_id = ? AND lower(title) = lower(?)`
		args := []any{libraryID, p.Title}
		if p.Year > 0 {
			q += ` AND year = ?`
			args = append(args, p.Year)
		}
		if tx.QueryRowContext(ctx, q, args...).Scan(&id) == nil {
			itemKind, itemID = "movie", id
		}
	case KindEpisode:
		var id string
		q := `SELECT id FROM series WHERE library_id = ? AND lower(title) = lower(?)`
		args := []any{libraryID, p.Title}
		if p.Year > 0 {
			q += ` AND year = ?`
			args = append(args, p.Year)
		}
		if tx.QueryRowContext(ctx, q, args...).Scan(&id) == nil {
			itemKind, itemID = "series", id
		}
	}
	if itemKind == "" || itemID == "" {
		return
	}
	_, _ = tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO match_queue(id, item_kind, item_id, query, year, attempts, last_error, created_at)
		VALUES (?, ?, ?, ?, ?, 0, '', ?)
	`, uuid.NewString(), itemKind, itemID, p.Title, nullYear(p.Year), time.Now().UTC().Format(time.RFC3339))
}

func (s *Scanner) probeWhenStable(ctx context.Context, libraryID, abs, rel string) {
	if s.Prober == nil {
		return
	}
	waitSizeStable(abs)
	var fileID string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM media_files WHERE library_id = ? AND rel_path = ?`, libraryID, rel).Scan(&fileID)
	if err != nil {
		return
	}
	_ = media.PersistProbe(ctx, s.DB, s.Prober, fileID)
}

func waitSizeStable(path string) {
	if SizeStable <= 0 {
		return
	}
	poll := SizeStable / 2
	if poll < time.Second {
		poll = time.Second
	}
	var last int64 = -1
	stableSince := time.Time{}
	deadline := time.Now().Add(SizeStable * 20)
	for time.Now().Before(deadline) {
		st, err := os.Stat(path)
		if err != nil {
			time.Sleep(poll)
			continue
		}
		sz := st.Size()
		if sz == last {
			if stableSince.IsZero() {
				stableSince = time.Now()
			}
			if time.Since(stableSince) >= SizeStable {
				return
			}
		} else {
			last = sz
			stableSince = time.Time{}
		}
		time.Sleep(poll)
	}
}

func (s *Scanner) markUnseen(ctx context.Context, libraryID, root string, seen map[string]bool) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, rel_path, abs_path FROM media_files WHERE library_id = ?`, libraryID)
	if err != nil {
		return
	}
	defer rows.Close()
	type row struct{ id, rel, abs string }
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.rel, &r.abs); err == nil {
			all = append(all, r)
		}
	}
	for _, r := range all {
		if seen[r.rel] {
			continue
		}
		_, err := os.Stat(r.abs)
		avail := media.ClassifyAvailability(r.abs, root, err)
		_, _ = s.DB.ExecContext(ctx, `UPDATE media_files SET availability = ?, updated_at = ? WHERE id = ?`,
			avail, time.Now().UTC().Format(time.RFC3339), r.id)
	}
}

func (s *Scanner) markLibraryOffline(ctx context.Context, libraryID string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE media_files SET availability = 'offline', updated_at = ? WHERE library_id = ?`,
		time.Now().UTC().Format(time.RFC3339), libraryID)
}

func (s *Scanner) rootOf(ctx context.Context, libraryID string) (string, error) {
	if s.Libs != nil {
		lib, err := s.Libs.Get(ctx, libraryID)
		if err != nil {
			return "", err
		}
		return lib.RootPath, nil
	}
	var root string
	err := s.DB.QueryRowContext(ctx, `SELECT root_path FROM libraries WHERE id = ?`, libraryID).Scan(&root)
	return root, err
}

// IngestFile catalogues a single new file (upload / watch) then probes it.
func (s *Scanner) IngestFile(ctx context.Context, libraryID, absPath string) error {
	root, err := s.rootOf(ctx, libraryID)
	if err != nil {
		return err
	}
	if err := library.ContainsPath(root, absPath); err != nil {
		return err
	}
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	if ShouldSkip(rel) || !IsVideo(rel) {
		return nil
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := s.upsertFile(ctx, tx, libraryID, root, absPath, rel, info); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if s.Prober != nil {
		var fileID string
		if err := s.DB.QueryRowContext(ctx, `SELECT id FROM media_files WHERE library_id = ? AND rel_path = ?`, libraryID, rel).Scan(&fileID); err == nil {
			_ = media.PersistProbe(ctx, s.DB, s.Prober, fileID)
		}
	}
	if s.OnIdle != nil {
		s.OnIdle()
	}
	return nil
}

func sortTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, p := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(s, p) {
			return strings.TrimSpace(s[len(p):]) + ", " + strings.TrimSpace(p)
		}
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func itoa(n int) string { return strconv.Itoa(n) }

func nullYear(y int) any {
	if y <= 0 {
		return nil
	}
	return y
}
