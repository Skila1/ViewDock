package audit

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type Log struct{ DB *sql.DB }

func New(db *sql.DB) *Log { return &Log{DB: db} }

func (l *Log) Event(ctx context.Context, actorID, action, target, ip, detail string) {
	if l == nil || l.DB == nil {
		return
	}
	_, _ = l.DB.ExecContext(ctx, `
		INSERT INTO audit_events(id, at, actor_id, action, target, ip, detail)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, uuid.NewString(), time.Now().UTC().Format(time.RFC3339), actorID, action, target, ip, detail)
}
