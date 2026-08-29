package oplog

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const retain = 14 * 24 * time.Hour
const maxRows = 20000

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
	Limit    int
	After    string
}

type Store struct {
	DB *sql.DB
	ch chan Entry
}

func New(db *sql.DB) *Store {
	s := &Store{DB: db, ch: make(chan Entry, 256)}
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

func (s *Store) Sweep(ctx context.Context) {
	if s == nil || s.DB == nil {
		return
	}
	cut := time.Now().UTC().Add(-retain).Format(time.RFC3339)
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM operational_logs WHERE created_at < ?`, cut)
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM operational_logs`).Scan(&n)
	if n > maxRows {
		_, _ = s.DB.ExecContext(ctx, `
			DELETE FROM operational_logs WHERE id IN (
				SELECT id FROM operational_logs ORDER BY created_at ASC LIMIT ?
			)
		`, n-maxRows)
	}
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
)

func Redact(s string) string {
	if s == "" {
		return s
	}
	out := reBearer.ReplaceAllString(s, "Bearer [redacted]")
	out = reStoken.ReplaceAllString(out, "stoken=[redacted]")
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
