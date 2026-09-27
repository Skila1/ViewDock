package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jackc/pgx/v5/stdlib"
)

// PostgresDriverName is a pgx-backed driver that accepts the SQLite dialect used
// throughout ViewDock (positional ? placeholders, INSERT OR IGNORE, datetime('now'),
// printf padding and case-insensitive LIKE) and translates it once per query shape.
const PostgresDriverName = "viewdock-pgx"

var registerOnce sync.Once

func registerPostgresDriver() {
	registerOnce.Do(func() {
		sql.Register(PostgresDriverName, &pgCompatDriver{base: stdlib.GetDefaultDriver()})
	})
}

// IsPostgres reports whether a *sql.DB was opened through the compatibility driver.
func IsPostgres(sqlDB *sql.DB) bool {
	if sqlDB == nil {
		return false
	}
	_, ok := sqlDB.Driver().(*pgCompatDriver)
	return ok
}

// DialectOf returns the dialect behind a *sql.DB.
func DialectOf(sqlDB *sql.DB) Dialect {
	if IsPostgres(sqlDB) {
		return DialectPostgres
	}
	return DialectSQLite
}

// QueryStats counts statements per dialect so write budgets can be measured.
type QueryStats struct {
	Reads  int64 `json:"reads"`
	Writes int64 `json:"writes"`
}

var stats struct{ reads, writes atomic.Int64 }

// Stats returns process-wide statement counters for the compatibility driver.
func Stats() QueryStats {
	return QueryStats{Reads: stats.reads.Load(), Writes: stats.writes.Load()}
}

func countStatement(query string) {
	q := strings.TrimSpace(query)
	if len(q) >= 6 && strings.EqualFold(q[:6], "SELECT") || len(q) >= 4 && strings.EqualFold(q[:4], "WITH") {
		stats.reads.Add(1)
		return
	}
	stats.writes.Add(1)
}

type pgCompatDriver struct{ base driver.Driver }

func (d *pgCompatDriver) Open(name string) (driver.Conn, error) {
	c, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &pgCompatConn{Conn: c}, nil
}

// pgCompatConnector binds a pool to its own outage breaker.
type pgCompatConnector struct {
	driver *pgCompatDriver
	base   driver.Connector
	b      *breaker
}

func (c *pgCompatConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if !isHealthProbe(ctx) {
		gctx, done, err := c.b.guard(ctx)
		if err != nil {
			return nil, err
		}
		defer done()
		ctx = gctx
	}
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &pgCompatConn{Conn: conn, b: c.b}, nil
}

func (c *pgCompatConnector) Driver() driver.Driver { return c.driver }

// openPostgres opens a translated, outage-aware pool. The health watcher runs
// until ctx ends or the pool is closed.
func openPostgres(ctx context.Context, dsn string) (*sql.DB, error) {
	registerPostgresDriver()
	d := &pgCompatDriver{base: stdlib.GetDefaultDriver()}
	dc, ok := d.base.(driver.DriverContext)
	if !ok {
		return nil, errors.New("pgcompat: base driver has no connector")
	}
	base, err := dc.OpenConnector(dsn)
	if err != nil {
		return nil, err
	}
	b := newBreaker()
	sqlDB := sql.OpenDB(&pgCompatConnector{driver: d, base: base, b: b})
	watchHealth(ctx, sqlDB, b)
	return sqlDB, nil
}

type pgCompatConn struct {
	driver.Conn
	b *breaker
}

func (c *pgCompatConn) Prepare(query string) (driver.Stmt, error) {
	return c.Conn.Prepare(TranslateSQLite(query))
}

func (c *pgCompatConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return p.PrepareContext(ctx, TranslateSQLite(query))
	}
	return c.Prepare(query)
}

func (c *pgCompatConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	b, ok := c.Conn.(driver.ConnBeginTx)
	if !ok {
		return nil, errors.New("pgcompat: BeginTx unsupported")
	}
	gctx, done, err := c.b.guard(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := b.BeginTx(gctx, opts)
	if err != nil {
		done()
		return nil, err
	}
	return &guardedTx{Tx: tx, done: done}, nil
}

// guardedTx keeps the guarded context alive for the whole transaction, since
// the driver binds the transaction to the context it was started with.
type guardedTx struct {
	driver.Tx
	done func()
}

func (t *guardedTx) Commit() error {
	defer t.done()
	return t.Tx.Commit()
}

func (t *guardedTx) Rollback() error {
	defer t.done()
	return t.Tx.Rollback()
}

func (c *pgCompatConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	e, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	gctx, done, err := c.b.guard(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	countStatement(query)
	return e.ExecContext(gctx, TranslateSQLite(query), args)
}

func (c *pgCompatConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	gctx, done, err := c.b.guard(ctx)
	if err != nil {
		return nil, err
	}
	countStatement(query)
	rows, err := q.QueryContext(gctx, TranslateSQLite(query), args)
	if err != nil {
		done()
		return nil, err
	}
	return &trackedRows{Rows: rows, done: done}, nil
}

func (c *pgCompatConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

// ResetSession may ping the server before a pooled connection is reused, so it
// is subject to the breaker like any statement.
func (c *pgCompatConn) ResetSession(ctx context.Context) error {
	r, ok := c.Conn.(driver.SessionResetter)
	if !ok {
		return nil
	}
	if !isHealthProbe(ctx) {
		gctx, done, err := c.b.guard(ctx)
		if err != nil {
			return driver.ErrBadConn
		}
		defer done()
		ctx = gctx
	}
	return r.ResetSession(ctx)
}

func (c *pgCompatConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

func (c *pgCompatConn) CheckNamedValue(nv *driver.NamedValue) error {
	if n, ok := c.Conn.(driver.NamedValueChecker); ok {
		return n.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

var (
	translateCache sync.Map
	printfPad      = regexp.MustCompile(`printf\('%0(\d)d',\s*([^)]+)\)`)
	insertIgnore   = regexp.MustCompile(`(?is)^\s*INSERT\s+OR\s+IGNORE\s+INTO`)
	likeWord       = regexp.MustCompile(`(?i)\sLIKE\s`)
)

const (
	pgNowText  = `to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')`
	pgNowISO   = `to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`
	pgNowISONZ = `to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS')`
)

// TranslateSQLite rewrites the SQLite dialect used by ViewDock into PostgreSQL.
// Results are cached because the set of distinct statements is small and fixed.
func TranslateSQLite(query string) string {
	if v, ok := translateCache.Load(query); ok {
		return v.(string)
	}
	out := translateSQLite(query)
	translateCache.Store(query, out)
	return out
}

func translateSQLite(query string) string {
	q := query
	q = strings.ReplaceAll(q, "datetime('now')", pgNowText)
	q = strings.ReplaceAll(q, "strftime('%Y-%m-%dT%H:%M:%SZ', 'now')", pgNowISO)
	q = strings.ReplaceAll(q, "strftime('%Y-%m-%dT%H:%M:%S', 'now')", pgNowISONZ)
	q = strings.ReplaceAll(q, " COLLATE NOCASE", "")
	q = printfPad.ReplaceAllString(q, "lpad(CAST($2 AS TEXT), $1, '0')")
	if insertIgnore.MatchString(q) {
		q = insertIgnore.ReplaceAllString(q, "INSERT INTO")
		if !strings.Contains(strings.ToUpper(q), "ON CONFLICT") {
			q = appendBeforeReturning(strings.TrimRight(strings.TrimSpace(q), ";"), " ON CONFLICT DO NOTHING")
		}
	}
	q = likeWord.ReplaceAllStringFunc(q, func(m string) string {
		return m[:1] + "ILIKE" + m[len(m)-1:]
	})
	return RewritePlaceholders(q)
}

func appendBeforeReturning(q, clause string) string {
	upper := strings.ToUpper(q)
	if i := strings.LastIndex(upper, " RETURNING "); i >= 0 {
		return q[:i] + clause + q[i:]
	}
	return q + clause
}
