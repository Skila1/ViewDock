package upload

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/library"
)

const (
	MaxSize    = 50 << 30 // 50 GiB
	CopyBuf    = 32 << 10 // 32 KiB
	stagingDir = ".viewdock-staging"
)

var (
	ErrUploadsDisabled = errors.New("uploads disabled for library")
	ErrTooLarge        = errors.New("file exceeds 50GiB")
	ErrOffset          = errors.New("upload offset mismatch")
	ErrClosed          = errors.New("upload not open")
)

type Ingester interface {
	IngestFile(ctx context.Context, libraryID, absPath string) error
}

type Service struct {
	DB     *sql.DB
	Libs   *library.Service
	Ingest Ingester
}

func New(db *sql.DB, libs *library.Service, ingest Ingester) *Service {
	return &Service{DB: db, Libs: libs, Ingest: ingest}
}

type Session struct {
	ID          string `json:"id"`
	LibraryID   string `json:"library_id"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	Offset      int64  `json:"offset"`
	Status      string `json:"status"`
	StagingPath string `json:"-"`
}

func (s *Service) Create(ctx context.Context, libraryID, filename string, size int64, createdBy string) (Session, error) {
	if size <= 0 || size > MaxSize {
		return Session{}, ErrTooLarge
	}
	lib, err := s.Libs.Get(ctx, libraryID)
	if err != nil {
		return Session{}, err
	}
	if !lib.UploadsEnabled {
		return Session{}, ErrUploadsDisabled
	}
	filename = sanitizeName(filename)
	if filename == "" {
		return Session{}, errors.New("filename required")
	}
	dest := filepath.Join(lib.RootPath, filepath.FromSlash(filename))
	if err := library.ContainsPath(lib.RootPath, dest); err != nil {
		return Session{}, err
	}
	id := uuid.NewString()
	stageRoot := filepath.Join(lib.RootPath, stagingDir)
	if err := os.MkdirAll(stageRoot, 0o755); err != nil {
		return Session{}, err
	}
	staging := filepath.Join(stageRoot, id)
	if err := library.ContainsPath(lib.RootPath, staging); err != nil {
		return Session{}, err
	}
	f, err := os.OpenFile(staging, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return Session{}, err
	}
	_ = f.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO uploads(id, library_id, filename, staging_path, size_bytes, offset_bytes, created_by, created_at, updated_at, status)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, 'open')
	`, id, libraryID, filename, staging, size, createdBy, now, now)
	if err != nil {
		_ = os.Remove(staging)
		return Session{}, err
	}
	return Session{ID: id, LibraryID: libraryID, Filename: filename, Size: size, Status: "open", StagingPath: staging}, nil
}

func (s *Service) Get(ctx context.Context, id string) (Session, error) {
	var u Session
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, library_id, filename, staging_path, size_bytes, offset_bytes, status
		FROM uploads WHERE id = ?
	`, id).Scan(&u.ID, &u.LibraryID, &u.Filename, &u.StagingPath, &u.Size, &u.Offset, &u.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, library.ErrNotFound
	}
	return u, err
}

func (s *Service) WriteAt(ctx context.Context, id string, offset int64, r io.Reader) (Session, error) {
	u, err := s.Get(ctx, id)
	if err != nil {
		return Session{}, err
	}
	if u.Status != "open" {
		return Session{}, ErrClosed
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
	n, err := io.CopyBuffer(f, io.LimitReader(r, remain), make([]byte, CopyBuf))
	_ = f.Close()
	if err != nil {
		return Session{}, err
	}
	u.Offset += n
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.DB.ExecContext(ctx, `UPDATE uploads SET offset_bytes = ?, updated_at = ? WHERE id = ?`, u.Offset, now, id)
	if err != nil {
		return Session{}, err
	}
	if u.Offset >= u.Size {
		if err := s.finalize(ctx, u); err != nil {
			return Session{}, err
		}
		u.Status = "done"
	}
	return u, nil
}

func (s *Service) finalize(ctx context.Context, u Session) error {
	lib, err := s.Libs.Get(ctx, u.LibraryID)
	if err != nil {
		return err
	}
	dest := filepath.Join(lib.RootPath, filepath.FromSlash(u.Filename))
	if err := library.ContainsPath(lib.RootPath, dest); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.Rename(u.StagingPath, dest); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = s.DB.ExecContext(ctx, `UPDATE uploads SET status = 'done', updated_at = ? WHERE id = ?`, now, u.ID)
	if s.Ingest != nil {
		return s.Ingest.IngestFile(ctx, u.LibraryID, dest)
	}
	return nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	u, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	_ = os.Remove(u.StagingPath)
	_, err = s.DB.ExecContext(ctx, `UPDATE uploads SET status = 'cancelled', updated_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = strings.TrimSpace(name)
	if name == "" || filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return ""
	}
	parts := strings.Split(name, "/")
	var clean []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "." || p == ".." {
			return ""
		}
		clean = append(clean, p)
	}
	return strings.Join(clean, "/")
}
