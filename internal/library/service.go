package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/mediafs"
)

var (
	ErrInvalidContentType = errors.New("content_type must be movies, tv, or mixed")
	ErrNameRequired       = errors.New("name required")
	ErrNotFound           = errors.New("not found")
	ErrRootInUse          = errors.New("another library already uses this folder")
)

// Service is the catalogue + library admin implementation.
type Service struct {
	DB       *sql.DB
	Grants   LibraryGrants
	Prober   ffmpeg.Prober
	Thumber  ffmpeg.Thumber
	CacheDir string
	// Audit and Cfg are optional; rating changes are audited through the
	// shared audit table and client IPs resolved without trusted proxies
	// when unset.
	Audit *audit.Log
	Cfg   config.Config
	// Storage holds the roots library folders may live under. Library
	// folders are validated against it and created by ViewDock itself; with
	// no roots configured no library can be created or re-pointed.
	Storage mediafs.Roots
	// RemoteCopy finds a finished copy of an external source's title on
	// this server. Such a title then plays like a local file.
	RemoteCopy func(ctx context.Context, sourceID, remoteID string) (path, container string, size int64, ok bool)
	// RemoteCopyDir is the folder those copies live in; playback may read
	// them for the source's libraries.
	RemoteCopyDir string
	scan          ScanStart
	moves         moveState
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
	resolved, created, err := s.prepareRoot(ctx, rootPath, name, "")
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
		if created {
			_ = os.Remove(resolved) // only succeeds while still empty
		}
		return Library{}, err
	}
	_, _ = s.DB.ExecContext(ctx, `
		INSERT OR IGNORE INTO library_role_grants(role_id, library_id, can_download)
		SELECT id, ?, 0 FROM roles WHERE name = 'User'
	`, lib.ID)
	return lib, nil
}

// prepareRoot validates a requested library folder against the storage
// roots, creates it (with parents, owner and mode) when missing, proves it is
// writable and returns its link-free absolute path. selfID is the library
// being edited, if any, so it does not conflict with itself.
func (s *Service) prepareRoot(ctx context.Context, requested, name, selfID string) (string, bool, error) {
	target, err := s.Storage.Resolve(requested, name)
	if err != nil {
		return "", false, err
	}
	if err := s.rootAvailable(ctx, target, selfID); err != nil {
		return "", false, err
	}
	created, err := s.Storage.EnsureDir(target)
	if err != nil {
		return "", created, s.Storage.Explain(target, err)
	}
	resolved, err := ResolveRoot(target)
	if err != nil {
		return "", created, err
	}
	if err := s.rootAvailable(ctx, resolved, selfID); err != nil {
		return "", created, err
	}
	if err := s.notCatalogued(ctx, resolved, selfID); err != nil {
		return "", created, err
	}
	return resolved, created, nil
}

// notCatalogued refuses a folder whose files another library already lists
// (a library at /media holding /media/movies), which would catalogue the
// same files twice. Such titles are moved with Move content instead.
func (s *Service) notCatalogued(ctx context.Context, dir, selfID string) error {
	var name string
	var n int
	// An exact, case-sensitive prefix match (LIKE ignores case in SQLite).
	prefix := filepath.Clean(dir) + string(os.PathSeparator)
	err := s.DB.QueryRowContext(ctx, `
		SELECT l.name, COUNT(*) FROM media_files mf JOIN libraries l ON l.id = mf.library_id
		WHERE mf.library_id <> ? AND substr(mf.abs_path, 1, ?) = ?
		GROUP BY l.name ORDER BY COUNT(*) DESC LIMIT 1
	`, selfID, utf8.RuneCountInString(prefix), prefix).Scan(&name, &n)
	if err != nil || n == 0 {
		return nil
	}
	return fmt.Errorf("%s already lists %s in this folder; choose a new folder and use Move content to move them", name, plural(n, "file", "files"))
}

// rootAvailable rejects a folder that another library (local or remote)
// already uses as its root.
func (s *Service) rootAvailable(ctx context.Context, dir, selfID string) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, root_path FROM libraries`)
	if err != nil {
		return err
	}
	defer rows.Close()
	want := filepath.Clean(dir)
	for rows.Next() {
		var id, root string
		if err := rows.Scan(&id, &root); err != nil {
			return err
		}
		if id != selfID && root != "" && filepath.Clean(root) == want {
			return ErrRootInUse
		}
	}
	return rows.Err()
}

func (s *Service) Get(ctx context.Context, id string) (Library, error) {
	return scanLibrary(s.DB.QueryRowContext(ctx, `
		SELECT id, name, root_path, content_type, uploads_enabled, created_at, updated_at
		FROM libraries WHERE id = ? AND id NOT IN (`+remoteLibraries+`)
	`, id))
}

// remoteLibraries selects the read-only libraries owned by external media
// sources. They are managed by their source, never scanned or edited here.
const remoteLibraries = `SELECT library_id FROM media_sources`

func (s *Service) List(ctx context.Context) ([]Library, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, name, root_path, content_type, uploads_enabled, created_at, updated_at
		FROM libraries WHERE id NOT IN (`+remoteLibraries+`) ORDER BY name
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
	if (patch.RootPath != nil || patch.ContentType != nil) && s.MoveBusy(id) {
		return Library{}, ErrMoveBusy
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
		if *patch.ContentType != lib.ContentType {
			if err := s.typeChangeAllowed(ctx, id, *patch.ContentType); err != nil {
				return Library{}, err
			}
		}
		lib.ContentType = *patch.ContentType
	}
	if patch.RootPath != nil && !s.sameRoot(*patch.RootPath, lib) {
		resolved, _, err := s.prepareRoot(ctx, *patch.RootPath, lib.Name, id)
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

// sameRoot reports whether a requested folder is the library's current one,
// written either way: as stored or as the host sees it.
func (s *Service) sameRoot(requested string, lib Library) bool {
	requested = strings.TrimSpace(requested)
	if filepath.Clean(requested) == filepath.Clean(lib.RootPath) {
		return true
	}
	target, err := s.Storage.Resolve(requested, lib.Name)
	return err == nil && filepath.Clean(target) == filepath.Clean(lib.RootPath)
}

// withHostPath fills in the host's path of a library in a folder of the host.
func (s *Service) withHostPath(lib Library) Library {
	lib.HostPath = s.Storage.HostPath(lib.RootPath)
	return lib
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	if s.MoveBusy(id) {
		return ErrMoveBusy
	}
	if err := DeleteLibraryFTS(ctx, s.DB, id); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM libraries WHERE id = ? AND id NOT IN (`+remoteLibraries+`)`, id)
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

// typeChangeAllowed refuses a content type that the library's existing
// titles would violate, so a library's type always describes its content.
func (s *Service) typeChangeAllowed(ctx context.Context, libraryID, contentType string) error {
	var movies, shows int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM movies WHERE library_id = ?`, libraryID).Scan(&movies)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM series WHERE library_id = ?`, libraryID).Scan(&shows)
	switch {
	case contentType == "movies" && shows > 0:
		return &TypeConflictError{ContentType: contentType, Count: shows, Kind: "series"}
	case contentType == "tv" && movies > 0:
		return &TypeConflictError{ContentType: contentType, Count: movies, Kind: "movie"}
	}
	return nil
}

// TypeConflictError explains why a library cannot switch content type.
type TypeConflictError struct {
	ContentType string
	Kind        string
	Count       int
}

func (e *TypeConflictError) Error() string {
	what := plural(e.Count, "TV show", "TV shows")
	if e.Kind == "movie" {
		what = plural(e.Count, "movie", "movies")
	}
	return fmt.Sprintf("this library still contains %s, which a %s library cannot hold; move them to a compatible library first", what, ContentTypeLabel(e.ContentType))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
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
	return artworkPath(kind, itemKind, itemID)
}

func artworkPath(kind, itemKind, itemID string) *string {
	u := fmt.Sprintf("/api/v1/artwork/%s/%s/%s", kind, itemKind, itemID)
	return &u
}

// artworkSet lists, in one query, the items of itemKind that have artwork
// of kind, for lists that would otherwise ask once per title.
func (s *Service) artworkSet(ctx context.Context, kind, itemKind string) map[string]bool {
	out := map[string]bool{}
	rows, err := s.DB.QueryContext(ctx, `SELECT item_id FROM artwork WHERE kind = ? AND item_kind = ? AND path <> ''`, kind, itemKind)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out
}

func (s *Service) openContained(absPath, libraryID string) (*os.File, error) {
	if err := s.Contains(libraryID, absPath); err != nil {
		return nil, err
	}
	return os.Open(absPath)
}
