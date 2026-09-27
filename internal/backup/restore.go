package backup

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/storage"
)

const maxManifestBytes = 16 << 20

// loadManifest reads and validates id/manifest.json from store.
func loadManifest(ctx context.Context, store storage.Store, id string) (Manifest, error) {
	if !ValidID(id) {
		return Manifest{}, ErrInvalidID
	}
	obj, err := store.Get(ctx, id+"/"+manifestName)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return Manifest{}, ErrNotFound
		}
		return Manifest{}, err
	}
	defer obj.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(obj.Body, maxManifestBytes+1))
	if err != nil {
		return Manifest{}, err
	}
	if len(raw) > maxManifestBytes {
		return Manifest{}, ErrManifest
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, ErrManifest
	}
	if err := m.validate(); err != nil || m.ID != id {
		return Manifest{}, ErrManifest
	}
	return m, nil
}

// verifyFile streams one backup file, optionally saving it to dest, and
// checks its size and SHA-256 against the manifest.
func verifyFile(ctx context.Context, store storage.Store, id string, f File, dest string) error {
	obj, err := store.Get(ctx, id+"/"+f.Name)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("%w: %s is missing", ErrChecksum, f.Name)
		}
		return err
	}
	defer obj.Body.Close()
	h := sha256.New()
	var w io.Writer = h
	var out *os.File
	if dest != "" {
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		out, err = os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer out.Close()
		w = io.MultiWriter(out, h)
	}
	n, err := io.Copy(w, io.LimitReader(obj.Body, f.Size+1))
	if err != nil {
		return err
	}
	if n != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return fmt.Errorf("%w: %s", ErrChecksum, f.Name)
	}
	if out != nil {
		if err := out.Sync(); err != nil {
			return err
		}
		return out.Close()
	}
	return nil
}

// fetchVerified downloads every file of m into dir and verifies it.
func fetchVerified(ctx context.Context, store storage.Store, m Manifest, dir string) error {
	for _, f := range m.Files {
		if err := verifyFile(ctx, store, m.ID, f, filepath.Join(dir, filepath.FromSlash(f.Name))); err != nil {
			return err
		}
	}
	return nil
}

// Target is the database a backup is restored into. Snapshot backups restored
// into SQLite replace the database file at SQLitePath, which requires the
// server to be stopped. Every other combination copies rows into DB, which
// must already be migrated to the backup's schema version.
type Target struct {
	Dialect    db.Dialect
	SQLitePath string
	DB         *sql.DB
}

type RestoreOptions struct {
	// Force allows restoring over a database that already has user accounts.
	Force bool
	// Actor is recorded in the audit event written after a restore.
	Actor string
}

type RestoreResult struct {
	ID     string `json:"id"`
	Mode   string `json:"mode"`
	Tables int    `json:"tables"`
	Rows   int64  `json:"rows"`
	// SafetyCopy is the previous SQLite file kept when a file restore replaced
	// an existing database.
	SafetyCopy string `json:"safety_copy,omitempty"`
}

const (
	ModeFile = "file"
	ModeRows = "rows"
)

func restoreMode(m Manifest, t Target) string {
	if m.Kind == KindSQLiteSnapshot && t.Dialect != db.DialectPostgres && t.SQLitePath != "" {
		return ModeFile
	}
	return ModeRows
}

// Restore verifies every file of backup id against its manifest, stages the
// files under stagingDir and restores them into target.
func Restore(ctx context.Context, store storage.Store, id string, target Target, stagingDir string, opts RestoreOptions) (RestoreResult, error) {
	m, err := loadManifest(ctx, store, id)
	if err != nil {
		return RestoreResult{}, err
	}
	latest, err := LatestSchemaVersion()
	if err != nil {
		return RestoreResult{}, err
	}
	if m.SchemaVersion > latest {
		return RestoreResult{}, ErrSchemaNewer
	}
	mode := restoreMode(m, target)
	var dir string
	if mode == ModeFile {
		// Stage next to the database so the final rename stays on one volume.
		dir, err = os.MkdirTemp(filepath.Dir(target.SQLitePath), ".vd-restore-")
	} else {
		if err := os.MkdirAll(stagingDir, 0o700); err != nil {
			return RestoreResult{}, err
		}
		dir, err = os.MkdirTemp(stagingDir, "restore-")
	}
	if err != nil {
		return RestoreResult{}, err
	}
	defer os.RemoveAll(dir)
	if err := fetchVerified(ctx, store, m, dir); err != nil {
		return RestoreResult{}, err
	}
	if mode == ModeFile {
		return restoreSQLiteFile(ctx, m, dir, target.SQLitePath, opts)
	}
	if target.DB == nil {
		return RestoreResult{}, errors.New("restore target database is not open")
	}
	return restoreRows(ctx, m, dir, target.DB, target.Dialect, opts)
}

// restoreSQLiteFile swaps the verified snapshot into place. An existing
// database is checkpointed and kept as <path>.pre-restore-<time> so the
// previous state can be put back by renaming it.
func restoreSQLiteFile(ctx context.Context, m Manifest, dir, dbPath string, opts RestoreOptions) (RestoreResult, error) {
	src := filepath.Join(dir, snapshotName)
	snap, err := openSQLiteReadOnly(src)
	if err != nil {
		return RestoreResult{}, err
	}
	var check string
	err = snap.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&check)
	_ = snap.Close()
	if err != nil || check != "ok" {
		return RestoreResult{}, fmt.Errorf("%w: snapshot failed its integrity check", ErrChecksum)
	}
	res := RestoreResult{ID: m.ID, Mode: ModeFile, Tables: len(m.Tables)}
	if _, err := os.Stat(dbPath); err == nil {
		cur, err := db.Open(dbPath, 5000)
		if err != nil {
			return RestoreResult{}, err
		}
		empty, err := isEmpty(ctx, cur, db.DialectSQLite)
		if err == nil {
			_, err = cur.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
		}
		closeErr := cur.Close()
		if err != nil {
			return RestoreResult{}, err
		}
		if closeErr != nil {
			return RestoreResult{}, closeErr
		}
		if !empty && !opts.Force {
			return RestoreResult{}, ErrNotEmpty
		}
		res.SafetyCopy = dbPath + ".pre-restore-" + time.Now().UTC().Format("20060102T150405Z")
		if err := os.Rename(dbPath, res.SafetyCopy); err != nil {
			return RestoreResult{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return RestoreResult{}, err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(dbPath + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return RestoreResult{}, err
		}
	}
	if err := os.Rename(src, dbPath); err != nil {
		return RestoreResult{}, err
	}
	restored, err := db.Open(dbPath, 5000)
	if err != nil {
		return res, err
	}
	defer restored.Close()
	audit.New(restored).Event(ctx, opts.Actor, "backup.restore", m.ID, "", "mode=file kind="+m.Kind)
	return res, nil
}

// rowSource yields one table of a backup.
type rowSource struct {
	name    string
	columns []string
	rows    int64 // -1 when unknown
	open    func() (next func() ([]any, error), closeFn func() error, err error)
}

func restoreRows(ctx context.Context, m Manifest, dir string, target *sql.DB, dialect db.Dialect, opts RestoreOptions) (RestoreResult, error) {
	targetSchema, err := schemaVersion(ctx, target, dialect)
	if err != nil {
		return RestoreResult{}, err
	}
	if targetSchema != m.SchemaVersion {
		return RestoreResult{}, fmt.Errorf("%w: backup %d, target %d", ErrSchemaMismatch, m.SchemaVersion, targetSchema)
	}
	sources, cleanup, err := rowSources(ctx, m, dir)
	if err != nil {
		return RestoreResult{}, err
	}
	defer cleanup()

	tx, err := target.BeginTx(ctx, nil)
	if err != nil {
		return RestoreResult{}, err
	}
	defer tx.Rollback()
	if dialect != db.DialectPostgres {
		if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
			return RestoreResult{}, err
		}
	}
	empty, err := isEmpty(ctx, tx, dialect)
	if err != nil {
		return RestoreResult{}, err
	}
	if !empty && !opts.Force {
		return RestoreResult{}, ErrNotEmpty
	}
	exist, err := existingTables(ctx, tx, dialect)
	if err != nil {
		return RestoreResult{}, err
	}
	for _, src := range sources {
		if !exist[src.name] {
			return RestoreResult{}, fmt.Errorf("%w: table %s does not exist in the target", ErrSchemaMismatch, src.name)
		}
		cols, err := tableColumns(ctx, tx, src.name)
		if err != nil {
			return RestoreResult{}, err
		}
		have := map[string]bool{}
		for _, c := range cols {
			have[strings.ToLower(c)] = true
		}
		for _, c := range src.columns {
			if !have[strings.ToLower(c)] {
				return RestoreResult{}, fmt.Errorf("%w: column %s.%s does not exist in the target", ErrSchemaMismatch, src.name, c)
			}
		}
	}
	// Seed rows from migrations and, with Force, existing data are replaced.
	for i := len(sources) - 1; i >= 0; i-- {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+quoteIdent(sources[i].name)); err != nil {
			return RestoreResult{}, fmt.Errorf("clear %s: %w", sources[i].name, err)
		}
	}
	res := RestoreResult{ID: m.ID, Mode: ModeRows, Tables: len(sources)}
	for _, src := range sources {
		n, err := copyRows(ctx, tx, src)
		if err != nil {
			return RestoreResult{}, fmt.Errorf("restore %s: %w", src.name, err)
		}
		if src.rows >= 0 && n != src.rows {
			return RestoreResult{}, fmt.Errorf("%w: %s has %d rows, manifest lists %d", ErrChecksum, src.name, n, src.rows)
		}
		res.Rows += n
	}
	if err := tx.Commit(); err != nil {
		return RestoreResult{}, err
	}
	audit.New(target).Event(ctx, opts.Actor, "backup.restore", m.ID, "", fmt.Sprintf("mode=rows kind=%s tables=%d rows=%d", m.Kind, res.Tables, res.Rows))
	return res, nil
}

func copyRows(ctx context.Context, tx *sql.Tx, src rowSource) (int64, error) {
	quoted := make([]string, len(src.columns))
	marks := make([]string, len(src.columns))
	for i, c := range src.columns {
		quoted[i] = quoteIdent(c)
		marks[i] = "?"
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO `+quoteIdent(src.name)+` (`+strings.Join(quoted, ", ")+`) VALUES (`+strings.Join(marks, ", ")+`)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	next, closeFn, err := src.open()
	if err != nil {
		return 0, err
	}
	defer closeFn()
	var n int64
	for {
		vals, err := next()
		if errors.Is(err, io.EOF) {
			return n, closeFn()
		}
		if err != nil {
			return n, err
		}
		if len(vals) != len(src.columns) {
			return n, fmt.Errorf("%w: row has %d values, expected %d", ErrManifest, len(vals), len(src.columns))
		}
		if _, err := stmt.ExecContext(ctx, vals...); err != nil {
			return n, err
		}
		n++
	}
}

func rowSources(ctx context.Context, m Manifest, dir string) ([]rowSource, func(), error) {
	if m.Kind == KindLogicalJSON {
		var out []rowSource
		for _, t := range m.Tables {
			path := filepath.Join(dir, filepath.FromSlash(t.File))
			out = append(out, rowSource{name: t.Name, columns: t.Columns, rows: t.Rows, open: jsonLinesReader(path)})
		}
		return out, func() {}, nil
	}
	snap, err := openSQLiteReadOnly(filepath.Join(dir, snapshotName))
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = snap.Close() }
	tables, err := applicationTables()
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	exist, err := existingTables(ctx, snap, db.DialectSQLite)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	var out []rowSource
	for _, name := range tables {
		if !exist[name] {
			continue
		}
		cols, err := tableColumns(ctx, snap, name)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		for _, c := range cols {
			if !columnPattern.MatchString(c) {
				cleanup()
				return nil, nil, fmt.Errorf("%w: unsupported column name in %s", ErrManifest, name)
			}
		}
		out = append(out, rowSource{name: name, columns: cols, rows: -1, open: sqliteTableReader(ctx, snap, name, len(cols))})
	}
	return out, cleanup, nil
}

func jsonLinesReader(path string) func() (func() ([]any, error), func() error, error) {
	return func() (func() ([]any, error), func() error, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, err
		}
		dec := json.NewDecoder(bufio.NewReaderSize(f, 256<<10))
		dec.UseNumber()
		closed := false
		closeFn := func() error {
			if closed {
				return nil
			}
			closed = true
			return f.Close()
		}
		next := func() ([]any, error) {
			var cells []any
			if err := dec.Decode(&cells); err != nil {
				if errors.Is(err, io.EOF) {
					return nil, io.EOF
				}
				return nil, ErrManifest
			}
			for i, c := range cells {
				v, err := decodeCell(c)
				if err != nil {
					return nil, ErrManifest
				}
				cells[i] = v
			}
			return cells, nil
		}
		return next, closeFn, nil
	}
}

func sqliteTableReader(ctx context.Context, snap *sql.DB, name string, width int) func() (func() ([]any, error), func() error, error) {
	return func() (func() ([]any, error), func() error, error) {
		rows, err := snap.QueryContext(ctx, `SELECT * FROM `+quoteIdent(name))
		if err != nil {
			return nil, nil, err
		}
		closed := false
		closeFn := func() error {
			if closed {
				return nil
			}
			closed = true
			return rows.Close()
		}
		next := func() ([]any, error) {
			if !rows.Next() {
				if err := rows.Err(); err != nil {
					return nil, err
				}
				return nil, io.EOF
			}
			vals := make([]any, width)
			ptrs := make([]any, width)
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return nil, err
			}
			return vals, nil
		}
		return next, closeFn, nil
	}
}

// Report is the result of a restore validation.
type Report struct {
	ID               string   `json:"id"`
	Valid            bool     `json:"valid"`
	Problems         []string `json:"problems"`
	Kind             string   `json:"kind"`
	SchemaVersion    int      `json:"schema_version"`
	CurrentSchema    int      `json:"current_schema"`
	SchemaCompatible bool     `json:"schema_compatible"`
	RestoreMode      string   `json:"restore_mode"`
	CurrentEmpty     bool     `json:"current_database_empty"`
	MasterKeyMatches *bool    `json:"master_key_matches,omitempty"`
	FilesChecked     int      `json:"files_checked"`
	Bytes            int64    `json:"bytes"`
}

// Validate checks a backup without changing anything: manifest shape, every
// file's size and SHA-256, schema compatibility with this build and whether
// the current database would accept a restore without Force.
func Validate(ctx context.Context, store storage.Store, id string, current *sql.DB, dialect db.Dialect, sqlitePath, keyID string) (Report, error) {
	m, err := loadManifest(ctx, store, id)
	if err != nil {
		return Report{}, err
	}
	rep := Report{ID: id, Kind: m.Kind, SchemaVersion: m.SchemaVersion, Problems: []string{}}
	for _, f := range m.Files {
		if err := verifyFile(ctx, store, id, f, ""); err != nil {
			if errors.Is(err, ErrChecksum) {
				rep.Problems = append(rep.Problems, err.Error())
				continue
			}
			return Report{}, err
		}
		rep.FilesChecked++
		rep.Bytes += f.Size
	}
	latest, err := LatestSchemaVersion()
	if err != nil {
		return Report{}, err
	}
	rep.RestoreMode = restoreMode(m, Target{Dialect: dialect, SQLitePath: sqlitePath})
	if current != nil {
		if rep.CurrentSchema, err = schemaVersion(ctx, current, dialect); err != nil {
			return Report{}, err
		}
		if rep.CurrentEmpty, err = isEmpty(ctx, current, dialect); err != nil {
			return Report{}, err
		}
	}
	switch {
	case m.SchemaVersion > latest:
		rep.Problems = append(rep.Problems, ErrSchemaNewer.Error())
	case rep.RestoreMode == ModeFile, current == nil:
		rep.SchemaCompatible = true
	case m.SchemaVersion == rep.CurrentSchema:
		rep.SchemaCompatible = true
	default:
		rep.Problems = append(rep.Problems, fmt.Sprintf("%s (backup %d, database %d); restore with the ViewDock version that created it, then upgrade", ErrSchemaMismatch.Error(), m.SchemaVersion, rep.CurrentSchema))
	}
	if m.MasterKeyID != "" && keyID != "" {
		match := m.MasterKeyID == keyID
		rep.MasterKeyMatches = &match
	}
	rep.Valid = len(rep.Problems) == 0
	return rep, nil
}
