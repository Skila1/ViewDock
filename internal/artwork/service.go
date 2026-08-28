package artwork

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/metadata"
)

type Service struct {
	DB       *sql.DB
	CacheDir string
	Thumber  ffmpeg.Thumber
	TMDB     *metadata.Client
	thumbMu  sync.Mutex
}

func New(db *sql.DB, cacheDir string, thumber ffmpeg.Thumber, tmdb *metadata.Client) *Service {
	return &Service{DB: db, CacheDir: cacheDir, Thumber: thumber, TMDB: tmdb}
}

// FetchTMDB downloads an allowlisted TMDB image into VD_CACHE_DIR/artwork.
func (s *Service) FetchTMDB(ctx context.Context, kind, itemKind, itemID, tmdbPath string) error {
	if s.TMDB == nil {
		return errors.New("no tmdb client")
	}
	if s.locked(ctx, itemKind, itemID, kind) {
		return nil
	}
	imgURL, err := s.TMDB.ImageURL(tmdbPath)
	if err != nil {
		return err
	}
	body, err := s.TMDBGet(ctx, imgURL)
	if err != nil {
		return err
	}
	rel := filepath.ToSlash(filepath.Join("artwork", kind, itemKind, itemID+".jpg"))
	dest := filepath.Join(s.CacheDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dest, body, 0o644); err != nil {
		return err
	}
	return s.upsert(ctx, itemKind, itemID, kind, rel, "tmdb", false)
}

func (s *Service) TMDBGet(ctx context.Context, rawURL string) ([]byte, error) {
	if s.TMDB == nil {
		return nil, errors.New("no tmdb client")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.TMDB.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, errors.New("image download failed")
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// UploadCustom stores a user image and locks that artwork kind.
func (s *Service) UploadCustom(ctx context.Context, kind, itemKind, itemID, ext string, r io.Reader) error {
	if !validKind(kind) || !validItem(itemKind) {
		return errors.New("invalid kind")
	}
	if ext == "" {
		ext = ".jpg"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	rel := filepath.ToSlash(filepath.Join("artwork", kind, itemKind, itemID+ext))
	dest := filepath.Join(s.CacheDir, filepath.FromSlash(rel))
	if s.CacheDir != "" {
		if err := library.ContainsPath(s.CacheDir, dest); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(r, 12<<20))
	_ = f.Close()
	if err != nil {
		return err
	}
	if err := s.upsert(ctx, itemKind, itemID, kind, rel, "custom", true); err != nil {
		return err
	}
	_, _ = s.DB.ExecContext(ctx, `
		INSERT OR IGNORE INTO metadata_locks(item_kind, item_id, field) VALUES (?, ?, ?)
	`, itemKind, itemID, kind)
	return nil
}

// EnsureThumb generates a thumbnail with at most one idle/in-flight worker.
func (s *Service) EnsureThumb(ctx context.Context, src, dest string, atMS int64) error {
	if s.Thumber == nil {
		return errors.New("no thumber")
	}
	s.thumbMu.Lock()
	defer s.thumbMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return s.Thumber.Thumb(ctx, src, dest, atMS)
}

func (s *Service) upsert(ctx context.Context, itemKind, itemID, kind, path, source string, locked bool) error {
	lk := 0
	if locked {
		lk = 1
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO artwork(id, item_kind, item_id, kind, path, source, locked)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(item_kind, item_id, kind) DO UPDATE SET
			path = excluded.path, source = excluded.source, locked = CASE WHEN artwork.locked = 1 THEN 1 ELSE excluded.locked END
	`, uuid.NewString(), itemKind, itemID, kind, path, source, lk)
	return err
}

func (s *Service) locked(ctx context.Context, itemKind, itemID, kind string) bool {
	var n int
	_ = s.DB.QueryRowContext(ctx, `
		SELECT locked FROM artwork WHERE item_kind = ? AND item_id = ? AND kind = ?
	`, itemKind, itemID, kind).Scan(&n)
	if n == 1 {
		return true
	}
	_ = s.DB.QueryRowContext(ctx, `
		SELECT 1 FROM metadata_locks WHERE item_kind = ? AND item_id = ? AND field = ?
	`, itemKind, itemID, kind).Scan(&n)
	return n == 1
}

func validKind(k string) bool { return k == "poster" || k == "backdrop" || k == "thumb" }
func validItem(k string) bool { return k == "movie" || k == "series" || k == "episode" }
