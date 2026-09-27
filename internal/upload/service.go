package upload

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/scan"
)

const (
	MaxSize      = 10 << 30 // 10 GiB
	MaxChunk     = 16 << 20 // 16 MiB per PUT
	CopyBuf      = 32 << 10
	sessionTTL   = 24 * time.Hour
	retainDone   = 7 * 24 * time.Hour
	statusOpen   = "open"
	statusProc   = "processing"
	statusDone   = "complete"
	statusFail   = "failed"
	statusCancel = "cancelled"
)

var (
	ErrUploadsDisabled = errors.New("uploads disabled for library")
	ErrTooLarge        = errors.New("file exceeds 10GiB")
	ErrOffset          = errors.New("upload offset mismatch")
	ErrClosed          = errors.New("upload not open")
	ErrForbidden       = errors.New("upload requires an administrator")
	ErrNotVideo        = errors.New("only video files can be uploaded")
	ErrLibraryKind     = errors.New("filename does not match this library type")
	ErrExpired         = errors.New("upload session expired")
	ErrOwner           = errors.New("upload belongs to another user")
	ErrDuplicate       = errors.New("could not allocate a unique filename")
	ErrSizeMismatch    = errors.New("uploaded size does not match the declared size")
	ErrInvalidMedia    = errors.New("file is not a valid video")
	ErrNotWritable     = errors.New("library folder is not writable")
)

type Ingester interface {
	IngestFile(ctx context.Context, libraryID, absPath string) error
}

type Service struct {
	DB      *sql.DB
	Libs    *library.Service
	Ingest  Ingester
	Prober  ffmpeg.Prober
	Staging string

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func New(db *sql.DB, libs *library.Service, ingest Ingester, prober ffmpeg.Prober, staging string) *Service {
	return &Service{DB: db, Libs: libs, Ingest: ingest, Prober: prober, Staging: staging, locks: map[string]*sync.Mutex{}}
}

type Session struct {
	ID          string `json:"id"`
	LibraryID   string `json:"library_id"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	Offset      int64  `json:"offset"`
	Status      string `json:"status"`
	ItemKind    string `json:"item_kind,omitempty"`
	ItemID      string `json:"item_id,omitempty"`
	MediaFileID string `json:"media_file_id,omitempty"`
	Error       string `json:"error,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	CreatedBy   string `json:"-"`
	StagingPath string `json:"-"`
}

func CanUpload(p *auth.Principal) bool {
	return p != nil && p.IsUser() && p.IsAdmin && p.HasPerm(auth.PermMediaUpload)
}

func (s *Service) lock(id string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}
	m := s.locks[id]
	if m == nil {
		m = &sync.Mutex{}
		s.locks[id] = m
	}
	return m
}

func (s *Service) Create(ctx context.Context, p *auth.Principal, libraryID, filename string, size int64, mime string) (Session, error) {
	if !CanUpload(p) {
		return Session{}, ErrForbidden
	}
	if size <= 0 || size > MaxSize {
		return Session{}, ErrTooLarge
	}
	filename = sanitizeFilename(filename)
	if filename == "" {
		return Session{}, ErrNotVideo
	}
	if err := mimeOK(mime); err != nil {
		return Session{}, err
	}
	lib, err := s.Libs.Get(ctx, libraryID)
	if err != nil {
		return Session{}, err
	}
	if !lib.UploadsEnabled {
		return Session{}, ErrUploadsDisabled
	}
	if err := libraryKindOK(lib.ContentType, filename); err != nil {
		return Session{}, err
	}
	if err := writableDir(lib.RootPath); err != nil {
		return Session{}, err
	}
	if err := os.MkdirAll(s.Staging, 0o755); err != nil {
		return Session{}, err
	}
	id := uuid.NewString()
	staging := filepath.Join(s.Staging, id)
	f, err := os.OpenFile(staging, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return Session{}, err
	}
	_ = f.Close()
	now := time.Now().UTC()
	exp := now.Add(sessionTTL).Format(time.RFC3339)
	nowS := now.Format(time.RFC3339)
	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO uploads(id, library_id, filename, staging_path, size_bytes, offset_bytes, created_by, created_at, updated_at, status, expires_at, error, mime)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, '', ?)
	`, id, libraryID, filename, staging, size, p.UserID, nowS, nowS, statusOpen, exp, strings.TrimSpace(mime))
	if err != nil {
		_ = os.Remove(staging)
		return Session{}, err
	}
	return Session{ID: id, LibraryID: libraryID, Filename: filename, Size: size, Status: statusOpen, ExpiresAt: exp, CreatedBy: p.UserID, StagingPath: staging}, nil
}

func (s *Service) Get(ctx context.Context, id string) (Session, error) {
	var u Session
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, library_id, filename, staging_path, size_bytes, offset_bytes, status, created_by,
		       COALESCE(expires_at,''), COALESCE(error,''), COALESCE(media_file_id,''), COALESCE(item_kind,''), COALESCE(item_id,'')
		FROM uploads WHERE id = ?
	`, id).Scan(&u.ID, &u.LibraryID, &u.Filename, &u.StagingPath, &u.Size, &u.Offset, &u.Status, &u.CreatedBy,
		&u.ExpiresAt, &u.Error, &u.MediaFileID, &u.ItemKind, &u.ItemID)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, library.ErrNotFound
	}
	return u, err
}

func (s *Service) List(ctx context.Context, p *auth.Principal) ([]Session, error) {
	if !CanUpload(p) {
		return nil, ErrForbidden
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, library_id, filename, staging_path, size_bytes, offset_bytes, status, created_by,
		       COALESCE(expires_at,''), COALESCE(error,''), COALESCE(media_file_id,''), COALESCE(item_kind,''), COALESCE(item_id,'')
		FROM uploads ORDER BY updated_at DESC LIMIT 50
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var u Session
		if err := rows.Scan(&u.ID, &u.LibraryID, &u.Filename, &u.StagingPath, &u.Size, &u.Offset, &u.Status, &u.CreatedBy,
			&u.ExpiresAt, &u.Error, &u.MediaFileID, &u.ItemKind, &u.ItemID); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if out == nil {
		out = []Session{}
	}
	return out, rows.Err()
}

func (s *Service) authorize(p *auth.Principal, u Session, write bool) error {
	if !CanUpload(p) {
		return ErrForbidden
	}
	if write && u.CreatedBy != "" && u.CreatedBy != p.UserID {
		return ErrOwner
	}
	return nil
}

func (s *Service) WriteAt(ctx context.Context, p *auth.Principal, id string, offset int64, r io.Reader) (Session, error) {
	lk := s.lock(id)
	lk.Lock()
	defer lk.Unlock()

	u, err := s.Get(ctx, id)
	if err != nil {
		return Session{}, err
	}
	if err := s.authorize(p, u, true); err != nil {
		return Session{}, err
	}
	if u.Status != statusOpen {
		return Session{}, ErrClosed
	}
	if expired(u.ExpiresAt) {
		return Session{}, ErrExpired
	}
	if offset != u.Offset {
		return Session{}, ErrOffset
	}
	remain := u.Size - u.Offset
	if remain < 0 {
		return Session{}, ErrTooLarge
	}
	f, err := os.OpenFile(u.StagingPath, os.O_WRONLY, 0o644)
	if err != nil {
		return Session{}, err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		_ = f.Close()
		return Session{}, err
	}
	capN := remain
	if capN > MaxChunk {
		capN = MaxChunk
	}
	n, err := io.CopyBuffer(f, io.LimitReader(r, capN), make([]byte, CopyBuf))
	_ = f.Close()
	if err != nil {
		return Session{}, err
	}
	next := u.Offset + n
	now := time.Now().UTC()
	exp := now.Add(sessionTTL).Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx, `
		UPDATE uploads SET offset_bytes = ?, updated_at = ?, expires_at = ? WHERE id = ? AND offset_bytes = ? AND status = ?
	`, next, now.Format(time.RFC3339), exp, id, u.Offset, statusOpen)
	if err != nil {
		return Session{}, err
	}
	if aff, _ := res.RowsAffected(); aff == 0 {
		return Session{}, ErrOffset
	}
	u.Offset = next
	u.ExpiresAt = exp
	if u.Offset >= u.Size {
		if err := s.finalize(ctx, u); err != nil {
			_ = s.fail(ctx, u.ID, err)
			return Session{}, err
		}
		return s.Get(ctx, id)
	}
	return u, nil
}

func (s *Service) finalize(ctx context.Context, u Session) error {
	_, _ = s.DB.ExecContext(ctx, `UPDATE uploads SET status = ?, updated_at = ? WHERE id = ?`, statusProc, time.Now().UTC().Format(time.RFC3339), u.ID)
	if err := fsyncPath(u.StagingPath); err != nil {
		return err
	}
	st, err := os.Stat(u.StagingPath)
	if err != nil {
		return err
	}
	if st.Size() != u.Size {
		return ErrSizeMismatch
	}
	if s.Prober != nil {
		info, err := s.Prober.ProbeFile(ctx, u.StagingPath)
		if err != nil || !hasVideo(info) {
			return ErrInvalidMedia
		}
	}
	lib, err := s.Libs.Get(ctx, u.LibraryID)
	if err != nil {
		return err
	}
	dest, finalName, err := uniqueDest(lib.RootPath, u.Filename)
	if err != nil {
		return err
	}
	if err := library.ContainsPath(lib.RootPath, dest); err != nil {
		return err
	}
	if err := atomicPlace(u.StagingPath, dest); err != nil {
		return err
	}
	if s.Ingest != nil {
		if err := s.Ingest.IngestFile(ctx, u.LibraryID, dest); err != nil {
			return err
		}
	}
	kind, itemID, mediaID := s.lookupItem(ctx, u.LibraryID, dest)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.DB.ExecContext(ctx, `
		UPDATE uploads SET status = ?, filename = ?, updated_at = ?, media_file_id = ?, item_kind = ?, item_id = ?, error = ''
		WHERE id = ?
	`, statusDone, finalName, now, mediaID, kind, itemID, u.ID)
	return err
}

func (s *Service) fail(ctx context.Context, id string, cause error) error {
	msg := cause.Error()
	u, _ := s.Get(ctx, id)
	if u.StagingPath != "" {
		_ = os.Remove(u.StagingPath)
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE uploads SET status = ?, error = ?, updated_at = ? WHERE id = ?`,
		statusFail, msg, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (s *Service) Delete(ctx context.Context, p *auth.Principal, id string) error {
	u, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.authorize(p, u, true); err != nil {
		return err
	}
	_ = os.Remove(u.StagingPath)
	_, err = s.DB.ExecContext(ctx, `UPDATE uploads SET status = ?, updated_at = ? WHERE id = ?`,
		statusCancel, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (s *Service) Sweep(ctx context.Context) {
	now := time.Now().UTC()
	rows, err := s.DB.QueryContext(ctx, `SELECT id, staging_path, status, expires_at, updated_at FROM uploads`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, staging, status, exp, updated string
		if err := rows.Scan(&id, &staging, &status, &exp, &updated); err != nil {
			return
		}
		if status == statusOpen && expired(exp) {
			_ = os.Remove(staging)
			_, _ = s.DB.ExecContext(ctx, `UPDATE uploads SET status = ?, error = ?, updated_at = ? WHERE id = ?`,
				statusCancel, ErrExpired.Error(), now.Format(time.RFC3339), id)
			continue
		}
		if (status == statusDone || status == statusFail || status == statusCancel) && olderThan(updated, retainDone) {
			if status != statusDone {
				_ = os.Remove(staging)
			}
			_, _ = s.DB.ExecContext(ctx, `DELETE FROM uploads WHERE id = ?`, id)
		}
	}
}

func (s *Service) lookupItem(ctx context.Context, libraryID, abs string) (kind, itemID, mediaID string) {
	_ = s.DB.QueryRowContext(ctx, `
		SELECT id, kind, COALESCE(movie_id,'') FROM media_files WHERE library_id = ? AND abs_path = ?
	`, libraryID, abs).Scan(&mediaID, &kind, &itemID)
	if kind == "episode" {
		_ = s.DB.QueryRowContext(ctx, `
			SELECT episode_id FROM media_file_episodes WHERE media_file_id = ? LIMIT 1
		`, mediaID).Scan(&itemID)
	}
	return kind, itemID, mediaID
}

func mimeOK(mime string) error {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if mime == "" || mime == "application/octet-stream" || strings.HasPrefix(mime, "video/") {
		return nil
	}
	return ErrNotVideo
}

func libraryKindOK(contentType, filename string) error {
	parsed := scan.Parse(filename)
	switch contentType {
	case "movies":
		if parsed.Kind == scan.KindEpisode {
			return ErrLibraryKind
		}
	case "tv":
		if parsed.Kind == scan.KindMovie {
			return ErrLibraryKind
		}
	}
	return nil
}

func writableDir(dir string) error {
	f, err := os.CreateTemp(dir, ".vd-write-*")
	if err != nil {
		return ErrNotWritable
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

func expired(exp string) bool {
	if exp == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, exp)
	if err != nil {
		return false
	}
	return time.Now().UTC().After(t)
}

func olderThan(raw string, age time.Duration) bool {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return false
	}
	return time.Since(t) > age
}

func hasVideo(info *ffmpeg.MediaInfo) bool {
	if info == nil {
		return false
	}
	if strings.TrimSpace(info.VideoCodec) != "" {
		return true
	}
	for _, st := range info.Streams {
		if st.Kind == "video" {
			return true
		}
	}
	return false
}
