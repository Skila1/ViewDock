package backup

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/viewdock/viewdock/internal/db"
)

// staged is a backup written to a local staging directory, ready for upload.
type staged struct {
	kind   string
	schema int
	files  []File
	tables []Table
}

// snapshotSQLite writes a consistent copy of a live SQLite database with
// VACUUM INTO, then checks the copy before it is accepted.
func snapshotSQLite(ctx context.Context, sqlDB *sql.DB, dir string) (staged, error) {
	path := filepath.Join(dir, snapshotName)
	if err := db.VacuumInto(ctx, sqlDB, path); err != nil {
		return staged{}, fmt.Errorf("sqlite snapshot: %w", err)
	}
	snap, err := openSQLiteReadOnly(path)
	if err != nil {
		return staged{}, err
	}
	defer snap.Close()
	var check string
	if err := snap.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&check); err != nil {
		return staged{}, fmt.Errorf("snapshot integrity check: %w", err)
	}
	if check != "ok" {
		return staged{}, fmt.Errorf("snapshot integrity check failed: %s", check)
	}
	schema, err := schemaVersion(ctx, snap, db.DialectSQLite)
	if err != nil {
		return staged{}, err
	}
	if err := snap.Close(); err != nil {
		return staged{}, err
	}
	f, err := hashFile(path, snapshotName)
	if err != nil {
		return staged{}, err
	}
	return staged{kind: KindSQLiteSnapshot, schema: schema, files: []File{f}}, nil
}

func openSQLiteReadOnly(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	sqlDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(abs)+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	return sqlDB, nil
}

// exportLogical streams every application table to JSON Lines inside a single
// read-only transaction, so all tables come from the same point in time.
// Rows are written as they are read; nothing is held in memory per table.
func exportLogical(ctx context.Context, sqlDB *sql.DB, dialect db.Dialect, dir string) (staged, error) {
	tables, err := applicationTables()
	if err != nil {
		return staged{}, err
	}
	var opts *sql.TxOptions
	if dialect == db.DialectPostgres {
		opts = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	tx, err := sqlDB.BeginTx(ctx, opts)
	if err != nil {
		return staged{}, err
	}
	defer tx.Rollback()
	schema, err := schemaVersion(ctx, tx, dialect)
	if err != nil {
		return staged{}, err
	}
	exist, err := existingTables(ctx, tx, dialect)
	if err != nil {
		return staged{}, err
	}
	if err := os.MkdirAll(filepath.Join(dir, tablesDir), 0o700); err != nil {
		return staged{}, err
	}
	out := staged{kind: KindLogicalJSON, schema: schema}
	for _, name := range tables {
		if !exist[name] {
			continue
		}
		t, f, err := exportTable(ctx, tx, dir, name)
		if err != nil {
			return staged{}, fmt.Errorf("export %s: %w", name, err)
		}
		out.tables = append(out.tables, t)
		out.files = append(out.files, f)
	}
	return out, nil
}

func exportTable(ctx context.Context, tx *sql.Tx, dir, name string) (Table, File, error) {
	rel := tablesDir + "/" + name + ".jsonl"
	rows, err := tx.QueryContext(ctx, `SELECT * FROM `+quoteIdent(name))
	if err != nil {
		return Table{}, File{}, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return Table{}, File{}, err
	}
	for _, c := range cols {
		if !columnPattern.MatchString(c) {
			return Table{}, File{}, fmt.Errorf("unsupported column name %q", c)
		}
	}
	f, err := os.OpenFile(filepath.Join(dir, filepath.FromSlash(rel)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Table{}, File{}, err
	}
	defer f.Close()
	h := sha256.New()
	cw := &countingWriter{w: io.MultiWriter(f, h)}
	bw := bufio.NewWriterSize(cw, 256<<10)
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	cells := make([]any, len(cols))
	var n int64
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return Table{}, File{}, err
		}
		for i, v := range vals {
			cells[i] = encodeCell(v)
		}
		line, err := json.Marshal(cells)
		if err != nil {
			return Table{}, File{}, err
		}
		if _, err := bw.Write(line); err != nil {
			return Table{}, File{}, err
		}
		if err := bw.WriteByte('\n'); err != nil {
			return Table{}, File{}, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return Table{}, File{}, err
	}
	if err := bw.Flush(); err != nil {
		return Table{}, File{}, err
	}
	if err := f.Sync(); err != nil {
		return Table{}, File{}, err
	}
	if err := f.Close(); err != nil {
		return Table{}, File{}, err
	}
	return Table{Name: name, File: rel, Columns: cols, Rows: n},
		File{Name: rel, Size: cw.n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// rawNumber marshals as a JSON number token without re-encoding.
type rawNumber string

func (r rawNumber) MarshalJSON() ([]byte, error) { return []byte(r), nil }

// encodeCell maps a database value to JSON without losing type or bytes.
// Floats always carry a decimal point or exponent so they decode as floats;
// binary data and strings that are not valid UTF-8 are base64 encoded.
func encodeCell(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case int64, bool:
		return x
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return map[string]string{"f": strconv.FormatFloat(x, 'g', -1, 64)}
		}
		s := strconv.FormatFloat(x, 'g', -1, 64)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		return rawNumber(s)
	case []byte:
		return map[string]string{"b64": base64.StdEncoding.EncodeToString(x)}
	case string:
		if !utf8.ValidString(x) {
			return map[string]string{"s64": base64.StdEncoding.EncodeToString([]byte(x))}
		}
		return x
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(x)
	}
}

// decodeCell reverses encodeCell for a value decoded with UseNumber.
func decodeCell(v any) (any, error) {
	switch x := v.(type) {
	case nil, bool, string:
		return x, nil
	case json.Number:
		s := string(x)
		if strings.ContainsAny(s, ".eE") {
			return strconv.ParseFloat(s, 64)
		}
		return strconv.ParseInt(s, 10, 64)
	case map[string]any:
		if len(x) != 1 {
			return nil, ErrManifest
		}
		for k, raw := range x {
			s, ok := raw.(string)
			if !ok {
				return nil, ErrManifest
			}
			switch k {
			case "b64":
				return base64.StdEncoding.DecodeString(s)
			case "s64":
				b, err := base64.StdEncoding.DecodeString(s)
				return string(b), err
			case "f":
				return strconv.ParseFloat(s, 64)
			}
		}
		return nil, ErrManifest
	default:
		return nil, ErrManifest
	}
}

func hashFile(path, name string) (File, error) {
	f, err := os.Open(path)
	if err != nil {
		return File{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return File{}, err
	}
	return File{Name: name, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}
