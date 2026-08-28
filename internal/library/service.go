package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/ffmpeg"
)

var (
	ErrInvalidContentType = errors.New("content_type must be movies, tv, or mixed")
	ErrNameRequired       = errors.New("name required")
	ErrNotFound           = errors.New("not found")
)

// Service is the catalogue + library admin implementation.
type Service struct {
	DB       *sql.DB
	Grants   LibraryGrants
	Prober   ffmpeg.Prober
	Thumber  ffmpeg.Thumber
	CacheDir string
	scan     ScanStart
}

// NewService constructs a library Service. grants, prober, and thumber may be nil.
func NewService(db *sql.DB, grants LibraryGrants, prober ffmpeg.Prober, thumber ffmpeg.Thumber, cacheDir string) *Service {
	return &Service{DB: db, Grants: grants, Prober: prober, Thumber: thumber, CacheDir: cacheDir}
}

var (
	_ LibrarySetup    = (*Service)(nil)
	_ MediaLocator    = (*Service)(nil)
	_ MediaCatalog    = (*Service)(nil)
	_ CollectionAdmin = (*Service)(nil)
	_ ScanStart       = (*Service)(nil)
)

// SetScan wires ScanStart after construction (avoids an import cycle with scan).
func (s *Service) SetScan(sc ScanStart) { s.scan = sc }

func (s *Service) Create(ctx context.Context, name, rootPath, contentType string) (Library, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Library{}, ErrNameRequired
	}
	if err := validContentType(contentType); err != nil {
		return Library{}, err
	}
	resolved, err := ResolveRoot(rootPath)
	if err != nil {
		return Library{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	lib := Library{
		ID:             uuid.NewString(),
		Name:           name,
		RootPath:       resolved,
		ContentType:    contentType,
		UploadsEnabled: true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO libraries(id, name, root_path, content_type, uploads_enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?)
	`, lib.ID, lib.Name, lib.RootPath, lib.ContentType, now, now)
	if err != nil {
		return Library{}, err
	}
	_, _ = s.DB.ExecContext(ctx, `
		INSERT OR IGNORE INTO library_role_grants(role_id, library_id, can_download)
		SELECT id, ?, 0 FROM roles WHERE name = 'User'
	`, lib.ID)
	return lib, nil
}

func (s *Service) Get(ctx context.Context, id string) (Library, error) {
	return scanLibrary(s.DB.QueryRowContext(ctx, `
		SELECT id, name, root_path, content_type, uploads_enabled, created_at, updated_at
		FROM libraries WHERE id = ?
	`, id))
}

func (s *Service) List(ctx context.Context) ([]Library, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, name, root_path, content_type, uploads_enabled, created_at, updated_at
		FROM libraries ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Library{}
	for rows.Next() {
		lib, err := scanLibraryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, lib)
	}
	return out, rows.Err()
}

type Patch struct {
	Name           *string
	RootPath       *string
	ContentType    *string
	UploadsEnabled *bool
}

func (s *Service) Update(ctx context.Context, id string, patch Patch) (Library, error) {
	lib, err := s.Get(ctx, id)
	if err != nil {
		return Library{}, err
	}
	if patch.Name != nil {
		n := strings.TrimSpace(*patch.Name)
		if n == "" {
			return Library{}, ErrNameRequired
		}
		lib.Name = n
	}
	if patch.ContentType != nil {
		if err := validContentType(*patch.ContentType); err != nil {
			return Library{}, err
		}
		lib.ContentType = *patch.ContentType
	}
	if patch.RootPath != nil {
		resolved, err := ResolveRoot(*patch.RootPath)
		if err != nil {
			return Library{}, err
		}
		lib.RootPath = resolved
	}
	if patch.UploadsEnabled != nil {
		lib.UploadsEnabled = *patch.UploadsEnabled
	}
	lib.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	up := 0
	if lib.UploadsEnabled {
		up = 1
	}
	_, err = s.DB.ExecContext(ctx, `
		UPDATE libraries SET name = ?, root_path = ?, content_type = ?, uploads_enabled = ?, updated_at = ?
		WHERE id = ?
	`, lib.Name, lib.RootPath, lib.ContentType, up, lib.UpdatedAt, id)
	return lib, err
}

func (s *Service) Delete(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM libraries WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) StartScan(ctx context.Context, libraryID string) (string, error) {
	if s.scan == nil {
		return "", errors.New("scan not wired")
	}
	if _, err := s.Get(ctx, libraryID); err != nil {
		return "", err
	}
	return s.scan.StartScan(ctx, libraryID)
}

func validContentType(ct string) error {
	switch ct {
	case "movies", "tv", "mixed":
		return nil
	default:
		return ErrInvalidContentType
	}
}

func scanLibrary(row *sql.Row) (Library, error) {
	var lib Library
	var up int
	err := row.Scan(&lib.ID, &lib.Name, &lib.RootPath, &lib.ContentType, &up, &lib.CreatedAt, &lib.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Library{}, ErrNotFound
	}
	lib.UploadsEnabled = up == 1
	return lib, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanLibraryRow(row rowScanner) (Library, error) {
	var lib Library
	var up int
	err := row.Scan(&lib.ID, &lib.Name, &lib.RootPath, &lib.ContentType, &up, &lib.CreatedAt, &lib.UpdatedAt)
	lib.UploadsEnabled = up == 1
	return lib, err
}

func nullInt(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

func sortTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, p := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(s, p) {
			art := strings.TrimSpace(p)
			return strings.TrimSpace(s[len(p):]) + ", " + art
		}
	}
	return s
}

func inClause(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

func asAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

func (s *Service) artworkURL(ctx context.Context, kind, itemKind, itemID string) *string {
	var path string
	err := s.DB.QueryRowContext(ctx, `
		SELECT path FROM artwork WHERE item_kind = ? AND item_id = ? AND kind = ?
	`, itemKind, itemID, kind).Scan(&path)
	if err != nil || path == "" {
		return nil
	}
	u := fmt.Sprintf("/api/v1/artwork/%s/%s/%s", kind, itemKind, itemID)
	return &u
}

func (s *Service) openContained(absPath, libraryID string) (*os.File, error) {
	if err := s.Contains(libraryID, absPath); err != nil {
		return nil, err
	}
	return os.Open(absPath)
}
