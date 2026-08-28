package progress

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("progress not found")

type SQLite struct {
	DB      *sql.DB
	TitleOf func(ctx context.Context, itemKind, itemID string) string
}

func New(db *sql.DB) *SQLite { return &SQLite{DB: db} }

func (s *SQLite) Get(ctx context.Context, userID, itemKind, itemID string) (Record, error) {
	var r Record
	var completed int
	err := s.DB.QueryRowContext(ctx, `
		SELECT item_kind, item_id, media_file_id, position_ms, duration_ms, completed, updated_at
		FROM playback_progress
		WHERE user_id = ? AND item_kind = ? AND item_id = ?
	`, userID, itemKind, itemID).Scan(&r.ItemKind, &r.ItemID, &r.MediaFileID, &r.PositionMS, &r.DurationMS, &completed, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	r.Completed = completed == 1
	r.ResumeMS = resumeMS(r.PositionMS, r.DurationMS, r.Completed)
	if s.TitleOf != nil {
		r.Title = s.TitleOf(ctx, r.ItemKind, r.ItemID)
	}
	return r, nil
}

func (s *SQLite) Put(ctx context.Context, userID, itemKind, itemID, mediaFileID string, positionMS, durationMS int64) error {
	if userID == "" {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	done := completed(positionMS, durationMS)
	di := 0
	if done {
		di = 1
	}
	var existed int
	_ = s.DB.QueryRowContext(ctx, `
		SELECT 1 FROM playback_progress WHERE user_id = ? AND item_kind = ? AND item_id = ?
	`, userID, itemKind, itemID).Scan(&existed)
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO playback_progress(user_id, item_kind, item_id, media_file_id, position_ms, duration_ms, completed, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, item_kind, item_id) DO UPDATE SET
			media_file_id = excluded.media_file_id,
			position_ms = excluded.position_ms,
			duration_ms = excluded.duration_ms,
			completed = excluded.completed,
			updated_at = excluded.updated_at
	`, userID, itemKind, itemID, mediaFileID, positionMS, durationMS, di, now)
	if err != nil {
		return err
	}
	if existed == 0 || done {
		_, _ = s.DB.ExecContext(ctx, `
			INSERT INTO watch_history(id, user_id, item_kind, item_id, watched_at, position_ms)
			VALUES (?, ?, ?, ?, ?, ?)
		`, uuid.NewString(), userID, itemKind, itemID, now, positionMS)
	}
	return nil
}

func (s *SQLite) Continue(ctx context.Context, userID string, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT item_kind, item_id, media_file_id, position_ms, duration_ms, completed, updated_at
		FROM playback_progress
		WHERE user_id = ? AND completed = 0 AND position_ms > 5000
		ORDER BY updated_at DESC
		LIMIT ?
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		var completed int
		if err := rows.Scan(&r.ItemKind, &r.ItemID, &r.MediaFileID, &r.PositionMS, &r.DurationMS, &completed, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Completed = completed == 1
		r.ResumeMS = resumeMS(r.PositionMS, r.DurationMS, r.Completed)
		if s.TitleOf != nil {
			r.Title = s.TitleOf(ctx, r.ItemKind, r.ItemID)
		}
		out = append(out, r)
	}
	if out == nil {
		out = []Record{}
	}
	return out, rows.Err()
}

func completed(pos, dur int64) bool {
	if dur <= 0 || pos <= 0 {
		return false
	}
	if dur-pos <= 30_000 {
		return true
	}
	return float64(pos)/float64(dur) >= 0.92
}

func resumeMS(pos, dur int64, done bool) int64 {
	if done {
		return 0
	}
	if pos < 5000 {
		return 0
	}
	if dur > 0 && pos > dur {
		return dur
	}
	return pos
}
