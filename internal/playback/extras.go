package playback

import (
	"context"
	"database/sql"
)

func (a *API) intro(ctx context.Context, itemKind, itemID string) any {
	if itemKind != "episode" || a.DB == nil {
		return nil
	}
	var start, end sql.NullInt64
	err := a.DB.QueryRowContext(ctx, `SELECT intro_start_ms, intro_end_ms FROM episodes WHERE id = ?`, itemID).Scan(&start, &end)
	if err != nil || !start.Valid {
		return nil
	}
	return map[string]int64{"start_ms": start.Int64, "end_ms": end.Int64}
}

func (a *API) credits(ctx context.Context, itemKind, itemID string) any {
	if itemKind != "episode" || a.DB == nil {
		return nil
	}
	var start sql.NullInt64
	err := a.DB.QueryRowContext(ctx, `SELECT credits_start_ms FROM episodes WHERE id = ?`, itemID).Scan(&start)
	if err != nil || !start.Valid {
		return nil
	}
	return map[string]int64{"start_ms": start.Int64}
}

func (a *API) nextEpisode(ctx context.Context, itemKind, itemID string) any {
	if itemKind != "episode" || a.DB == nil {
		return nil
	}
	var id, title string
	var season, number int
	err := a.DB.QueryRowContext(ctx, `
		SELECT e2.id, e2.title, e2.season, e2.number FROM episodes e
		JOIN episodes e2 ON e2.series_id = e.series_id
		WHERE e.id = ? AND (e2.season > e.season OR (e2.season = e.season AND e2.number > e.number))
		ORDER BY e2.season, e2.number LIMIT 1
	`, itemID).Scan(&id, &title, &season, &number)
	if err != nil {
		return nil
	}
	// id is what the player reads; item_kind and item_id are kept for
	// existing clients.
	return map[string]any{"id": id, "item_kind": "episode", "item_id": id, "title": title, "season": season, "number": number}
}
