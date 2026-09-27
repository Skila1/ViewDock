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
