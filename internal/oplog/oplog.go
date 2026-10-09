package oplog

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/db"
)

type Entry struct {
	ID        string         `json:"id"`
	CreatedAt string         `json:"created_at"`
	Level     string         `json:"level"`
	Category  string         `json:"category"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	ActorID   string         `json:"actor_id,omitempty"`
}

type Filter struct {
	Level    string
	Category string
	Q        string
	Actor    string
	Limit    int
	After    string
}

type Store struct {
	DB db.Queryer
	ch chan Entry
	// retainDays deletes logs older than that many days when positive.
	// Zero, the default, keeps them until an administrator prunes them.
	retainDays atomic.Int64
	// ClientIP names the caller in the audit log of a prune.
	ClientIP func(*http.Request) string
}

func (s *Store) SetRetentionDays(days int) { s.retainDays.Store(int64(days)) }

// Stats is how much the operational log holds.
type Stats struct {
	Rows   int64  `json:"rows"`
	Bytes  int64  `json:"bytes"`
	Oldest string `json:"oldest,omitempty"`
	Newest string `json:"newest,omitempty"`
	// RetentionDays is the automatic cleanup; 0 keeps logs forever.
	RetentionDays int64 `json:"retention_days"`
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	var oldest, newest sql.NullString
	err := s.DB.QueryRowContext(ctx, `
		SELECT COUNT(*),
			COALESCE(SUM(LENGTH(id) + LENGTH(created_at) + LENGTH(level) + LENGTH(category) + LENGTH(message) + LENGTH(details) + COALESCE(LENGTH(actor_id), 0)), 0),
			MIN(created_at), MAX(created_at)
		FROM operational_logs
	`).Scan(&st.Rows, &st.Bytes, &oldest, &newest)
	st.Oldest, st.Newest = oldest.String, newest.String
	st.RetentionDays = s.retainDays.Load()
	return st, err
}

// Prune deletes the logs written before the given time, or every log when
// before is zero, and returns how many it removed.
func (s *Store) Prune(ctx context.Context, before time.Time) (int64, error) {
	var res sql.Result
	var err error
	if before.IsZero() {
		res, err = s.DB.ExecContext(ctx, `DELETE FROM operational_logs`)
	} else {
		res, err = s.DB.ExecContext(ctx, `DELETE FROM operational_logs WHERE created_at < ?`, before.UTC().Format(time.RFC3339Nano))
	}
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func New(database db.Queryer) *Store {
	s := &Store{DB: database, ch: make(chan Entry, 256)}
	go s.loop()
	return s
}

func (s *Store) loop() {
	for e := range s.ch {
		s.insert(context.Background(), e)
	}
}

func (s *Store) Write(ctx context.Context, e Entry) {
	if s == nil || s.DB == nil {
		return
	}
	e.Level = normalizeLevel(e.Level)
	e.Message = Redact(strings.TrimSpace(e.Message))
	e.Category = strings.TrimSpace(e.Category)
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.CreatedAt == "" {
		e.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if e.Details != nil {
		e.Details = redactDetails(e.Details)
	}
	select {
	case s.ch <- e:
	default:
		s.insert(ctx, e)
	}
}

func (s *Store) insert(ctx context.Context, e Entry) {
	raw := "{}"
	if e.Details != nil {
		if b, err := json.Marshal(e.Details); err == nil {
			raw = string(b)
		}
	}
	_, _ = s.DB.ExecContext(ctx, `
		INSERT INTO operational_logs(id, created_at, level, category, message, details, actor_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, e.ID, e.CreatedAt, e.Level, e.Category, e.Message, raw, nullStr(e.ActorID))
}

func (s *Store) List(ctx context.Context, f Filter) ([]Entry, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT id, created_at, level, category, message, details, COALESCE(actor_id,'')
		FROM operational_logs WHERE 1=1`
	args := []any{}
	if f.Level != "" {
		q += ` AND level = ?`
		args = append(args, normalizeLevel(f.Level))
	}
	if f.Category != "" {
		q += ` AND category = ?`
		args = append(args, f.Category)
	}
	if f.Q != "" {
		q += ` AND (message LIKE ? OR category LIKE ?)`
		like := "%" + f.Q + "%"
		args = append(args, like, like)
	}
	if f.Actor != "" {
		q += ` AND actor_id = ?`
		args = append(args, f.Actor)
	}
	if f.After != "" {
		q += ` AND created_at < ?`
		args = append(args, f.After)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var details string
		if err := rows.Scan(&e.ID, &e.CreatedAt, &e.Level, &e.Category, &e.Message, &details, &e.ActorID); err != nil {
			return nil, err
		}
		if details != "" && details != "{}" {
			_ = json.Unmarshal([]byte(details), &e.Details)
		}
		out = append(out, e)
	}
	if out == nil {
		out = []Entry{}
	}
	return out, rows.Err()
}

// Sweep applies the automatic cleanup, when one is set. Logs are otherwise
// kept until an administrator prunes them.
func (s *Store) Sweep(ctx context.Context) {
	if s == nil || s.DB == nil {
		return
	}
	days := s.retainDays.Load()
	if days <= 0 {
		return
	}
	_, _ = s.Prune(ctx, time.Now().Add(-time.Duration(days)*24*time.Hour))
}

func (s *Store) FromRecord(r slog.Record) {
	e := Entry{Level: levelName(r.Level), Message: r.Message, Details: map[string]any{}}
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "category" {
			e.Category = a.Value.String()
			return true
		}
		if a.Key == "actor_id" {
			e.ActorID = a.Value.String()
			return true
		}
		e.Details[a.Key] = a.Value.Any()
		return true
	})
	if e.Category == "" {
		e.Category = "app"
	}
	s.Write(context.Background(), e)
}

var (
	reKVSecret = regexp.MustCompile(`(?i)\b(password|passwd|secret|token|api[_-]?key|authorization|bearer|stoken|vd_[a-z0-9]+)\s*[=:]\s*([^\s,;]+)`)
	reBearer   = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._\-+/=]+`)
	reStoken   = regexp.MustCompile(`(?i)stoken=[^&\s]+`)
	// Media source stream URLs carry a grant token as a path segment.
	reStreamGrant = regexp.MustCompile(`/media-sources/stream/[^/\s?#"]+`)
)

func Redact(s string) string {
	if s == "" {
		return s
	}
	out := reBearer.ReplaceAllString(s, "Bearer [redacted]")
	out = reStoken.ReplaceAllString(out, "stoken=[redacted]")
	out = reStreamGrant.ReplaceAllString(out, "/media-sources/stream/[redacted]")
	out = reKVSecret.ReplaceAllString(out, "${1}=[redacted]")
	return out
}

func redactDetails(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		ks := strings.ToLower(k)
		if strings.Contains(ks, "token") || strings.Contains(ks, "secret") || strings.Contains(ks, "password") || strings.Contains(ks, "stoken") {
			out[k] = "[redacted]"
			continue
		}
		if t, ok := v.(string); ok {
			out[k] = Redact(t)
			continue
		}
		out[k] = v
	}
	return out
}

func normalizeLevel(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug", "info", "warn", "error":
		return strings.ToLower(strings.TrimSpace(s))
	default:
		return "info"
	}
}

func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warn"
	case l <= slog.LevelDebug:
		return "debug"
	default:
		return "info"
	}
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
