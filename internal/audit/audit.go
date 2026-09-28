package audit

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/db"
)

type Log struct{ DB db.Queryer }

func New(database db.Queryer) *Log { return &Log{DB: database} }

func (l *Log) Event(ctx context.Context, actorID, action, target, ip, detail string) {
	if l == nil || l.DB == nil {
		return
	}
	_, _ = l.DB.ExecContext(ctx, `
		INSERT INTO audit_events(id, at, actor_id, action, target, ip, detail)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, uuid.NewString(), time.Now().UTC().Format(time.RFC3339), actorID, action, target, ip, detail)
}

type Event struct {
	ID            string `json:"id"`
	At            string `json:"at"`
	ActorID       string `json:"actor_id,omitempty"`
	ActorUsername string `json:"actor_username,omitempty"`
	Action        string `json:"action"`
	Target        string `json:"target,omitempty"`
	IP            string `json:"ip,omitempty"`
	Detail        string `json:"detail,omitempty"`
}

type Filter struct {
	Action string
	Actor  string
	Q      string
	Before string
	Limit  int
}

// List returns audit events newest first. Action matches as a prefix, so
// "backup." lists every backup action.
func (l *Log) List(ctx context.Context, f Filter) ([]Event, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT a.id, a.at, a.actor_id, COALESCE(u.username, ''), a.action, a.target, a.ip, a.detail
		FROM audit_events a LEFT JOIN users u ON u.id = a.actor_id WHERE 1=1`
	args := []any{}
	if f.Action != "" {
		q += ` AND a.action LIKE ?`
		args = append(args, f.Action+"%")
	}
	if f.Actor != "" {
		q += ` AND a.actor_id = ?`
		args = append(args, f.Actor)
	}
	if f.Q != "" {
		like := "%" + f.Q + "%"
		q += ` AND (a.action LIKE ? OR a.target LIKE ? OR a.detail LIKE ? OR u.username LIKE ?)`
		args = append(args, like, like, like, like)
	}
	if f.Before != "" {
		q += ` AND a.at < ?`
		args = append(args, f.Before)
	}
	q += ` ORDER BY a.at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := l.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.At, &e.ActorID, &e.ActorUsername, &e.Action, &e.Target, &e.IP, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
