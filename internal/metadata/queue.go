package metadata

import (
	"context"
	"time"

	"github.com/google/uuid"
)

func newID() string { return uuid.NewString() }

func (s *Service) DrainQueue(ctx context.Context) error {
	if !s.HasKey(ctx) {
		return nil
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, item_kind, item_id FROM match_queue ORDER BY created_at LIMIT 50
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type item struct{ id, kind, itemID string }
	var list []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.kind, &it.itemID); err != nil {
			return err
		}
		list = append(list, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, it := range list {
		err := s.TryAutoMatch(ctx, it.kind, it.itemID)
		now := time.Now().UTC().Format(time.RFC3339)
		if err != nil {
			_, _ = s.DB.ExecContext(ctx, `
				UPDATE match_queue SET attempts = attempts + 1, last_error = ? WHERE id = ?
			`, err.Error(), it.id)
			continue
		}
		var unmatched int
		table := "movies"
		if it.kind == "series" {
			table = "series"
		}
		_ = s.DB.QueryRowContext(ctx, `SELECT unmatched FROM `+table+` WHERE id = ?`, it.itemID).Scan(&unmatched)
		if unmatched == 0 {
			_, _ = s.DB.ExecContext(ctx, `DELETE FROM match_queue WHERE id = ?`, it.id)
		} else {
			_, _ = s.DB.ExecContext(ctx, `
				UPDATE match_queue SET attempts = attempts + 1, last_error = '', created_at = ? WHERE id = ?
			`, now, it.id)
		}
	}
	return nil
}

func (s *Service) Enqueue(ctx context.Context, itemKind, itemID, query string, year int) error {
	var y any
	if year > 0 {
		y = year
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT OR IGNORE INTO match_queue(id, item_kind, item_id, query, year, attempts, last_error, created_at)
		VALUES (?, ?, ?, ?, ?, 0, '', ?)
	`, newID(), itemKind, itemID, query, y, time.Now().UTC().Format(time.RFC3339))
	return err
}
