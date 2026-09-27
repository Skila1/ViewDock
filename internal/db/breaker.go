package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"time"
)

// ErrUnavailable is returned without contacting the database while the
// health watcher considers it down.
var ErrUnavailable = errors.New("database unavailable")

const (
	healthInterval  = time.Second
	healthTimeout   = 1500 * time.Millisecond
	tripAfterFailed = 2
	// A stalled server accepts TCP but never answers the startup exchange.
	connectTimeout = 5 * time.Second
)

type healthProbeKey struct{}

// Health probes must reach the server while the breaker is open.
func isHealthProbe(ctx context.Context) bool {
	v, _ := ctx.Value(healthProbeKey{}).(bool)
	return v
}

// breaker fails queries fast during an outage. A stalled server (for example
// a paused container or a black-holed network) never returns errors on its
// own, so tripping also cancels every in-flight statement.
type breaker struct {
	open     atomic.Bool
	trips    atomic.Int64
	since    atomic.Int64
	mu       sync.Mutex
	next     uint64
	inflight map[uint64]context.CancelFunc
}

func newBreaker() *breaker { return &breaker{inflight: map[uint64]context.CancelFunc{}} }

func (b *breaker) guard(ctx context.Context) (context.Context, func(), error) {
	if b == nil {
		return ctx, func() {}, nil
	}
	if b.open.Load() {
		return nil, nil, ErrUnavailable
	}
	ctx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	b.next++
	id := b.next
	b.inflight[id] = cancel
	b.mu.Unlock()
	return ctx, func() {
		b.mu.Lock()
		delete(b.inflight, id)
		b.mu.Unlock()
		cancel()
	}, nil
}

func (b *breaker) trip() {
	if b.open.Swap(true) {
		return
	}
	b.trips.Add(1)
	b.since.Store(time.Now().UnixMilli())
	b.mu.Lock()
	for id, cancel := range b.inflight {
		cancel()
		delete(b.inflight, id)
	}
	b.mu.Unlock()
}

func (b *breaker) reset() {
	if b.open.Swap(false) {
		b.since.Store(time.Now().UnixMilli())
	}
}

// Health describes database reachability as seen by the health watcher.
type Health struct {
	Available bool      `json:"available"`
	Since     time.Time `json:"since,omitempty"`
	Trips     int64     `json:"trips"`
	Watched   bool      `json:"watched"`
}

// HealthOf reports the breaker state of a pool opened by OpenProvider.
func HealthOf(sqlDB *sql.DB) Health {
	b := breakerOf(sqlDB)
	if b == nil {
		return Health{Available: true}
	}
	h := Health{Available: !b.open.Load(), Trips: b.trips.Load(), Watched: true}
	if ms := b.since.Load(); ms > 0 {
		h.Since = time.UnixMilli(ms).UTC()
	}
	return h
}

var breakers sync.Map // *sql.DB -> *breaker

func breakerOf(sqlDB *sql.DB) *breaker {
	if sqlDB == nil {
		return nil
	}
	if v, ok := breakers.Load(sqlDB); ok {
		return v.(*breaker)
	}
	return nil
}

// watchHealth pings the pool on a fixed cadence until ctx ends, tripping the
// breaker after consecutive failures and closing it on the first success.
func watchHealth(ctx context.Context, sqlDB *sql.DB, b *breaker) {
	breakers.Store(sqlDB, b)
	go func() {
		t := time.NewTicker(healthInterval)
		defer t.Stop()
		failed := 0
		for {
			select {
			case <-ctx.Done():
				breakers.Delete(sqlDB)
				return
			case <-t.C:
			}
			pctx, cancel := context.WithTimeout(context.WithValue(ctx, healthProbeKey{}, true), healthTimeout)
			err := sqlDB.PingContext(pctx)
			cancel()
			if errors.Is(err, sql.ErrConnDone) || ctx.Err() != nil {
				breakers.Delete(sqlDB)
				return
			}
			if err == nil {
				failed = 0
				b.reset()
				continue
			}
			failed++
			if failed >= tripAfterFailed {
				b.trip()
			}
		}
	}()
}

// trackedRows releases the breaker registration when the result set closes.
type trackedRows struct {
	driver.Rows
	done func()
}

func (r *trackedRows) Close() error {
	err := r.Rows.Close()
	r.done()
	return err
}

func (r *trackedRows) ColumnTypeDatabaseTypeName(i int) string {
	if c, ok := r.Rows.(driver.RowsColumnTypeDatabaseTypeName); ok {
		return c.ColumnTypeDatabaseTypeName(i)
	}
	return ""
}

func (r *trackedRows) ColumnTypeScanType(i int) reflect.Type {
	if c, ok := r.Rows.(driver.RowsColumnTypeScanType); ok {
		return c.ColumnTypeScanType(i)
	}
	return reflect.TypeOf(new(any)).Elem()
}

func (r *trackedRows) ColumnTypeNullable(i int) (bool, bool) {
	if c, ok := r.Rows.(driver.RowsColumnTypeNullable); ok {
		return c.ColumnTypeNullable(i)
	}
	return false, false
}

func (r *trackedRows) ColumnTypeLength(i int) (int64, bool) {
	if c, ok := r.Rows.(driver.RowsColumnTypeLength); ok {
		return c.ColumnTypeLength(i)
	}
	return 0, false
}

func (r *trackedRows) ColumnTypePrecisionScale(i int) (int64, int64, bool) {
	if c, ok := r.Rows.(driver.RowsColumnTypePrecisionScale); ok {
		return c.ColumnTypePrecisionScale(i)
	}
	return 0, 0, false
}
