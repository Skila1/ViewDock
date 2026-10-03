package library

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/httpapi"
)

// Reactions a viewer gives a movie or series. Not interested hides a title
// from recommendations without counting against its genres, unlike dislike.
const (
	ReactionLike          = 1
	ReactionDislike       = -1
	ReactionNotInterested = -2
)

func (s *Service) preferenceRoutes(r chi.Router) {
	r.Put("/titles/{kind}/{id}/watched", s.handleSetWatched)
	r.Put("/titles/{kind}/{id}/reaction", s.handleSetReaction)
	s.profileRoutes(r)
}

// visibleTitle answers 404 unless the caller may see the title.
func (s *Service) visibleTitle(w http.ResponseWriter, r *http.Request, kind, id string) bool {
	v, err := NewVisibility(r.Context(), s.DB)
	if err != nil {
		writeListErr(w, err)
		return false
	}
	ok, err := v.Item(r.Context(), kind, id)
	if err != nil {
		writeListErr(w, err)
		return false
	}
	if !ok {
		httpapi.WriteErr(w, http.StatusNotFound, "not_found", "not found")
	}
	return ok
}

func (s *Service) handleSetWatched(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFrom(r.Context())
	kind, id := chi.URLParam(r, "kind"), chi.URLParam(r, "id")
	if userID == "" {
		httpapi.WriteErr(w, http.StatusForbidden, "forbidden", "only signed-in users keep watch history")
		return
	}
	if kind != "movie" && kind != "series" && kind != "episode" {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "kind must be movie, series or episode")
		return
	}
	var body struct {
		Watched bool `json:"watched"`
	}
	if err := httpapi.ReadJSON(r, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	if !s.visibleTitle(w, r, kind, id) {
		return
	}
	if err := s.SetWatched(r.Context(), userID, kind, id, body.Watched); err != nil {
		writeListErr(w, err)
		return
	}
	httpapi.WriteOK(w)
}

func (s *Service) handleSetReaction(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFrom(r.Context())
	kind, id := chi.URLParam(r, "kind"), chi.URLParam(r, "id")
	if userID == "" {
		httpapi.WriteErr(w, http.StatusForbidden, "forbidden", "only signed-in users keep preferences")
		return
	}
	if kind != "movie" && kind != "series" {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "kind must be movie or series")
		return
	}
	var body struct {
		Reaction string `json:"reaction"`
	}
	if err := httpapi.ReadJSON(r, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	value := 0
	switch body.Reaction {
	case "like":
		value = ReactionLike
	case "dislike":
		value = ReactionDislike
	case "not_interested":
		value = ReactionNotInterested
	case "":
	default:
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", `reaction must be "like", "dislike", "not_interested" or ""`)
		return
	}
	if !s.visibleTitle(w, r, kind, id) {
		return
	}
	if err := s.SetReaction(r.Context(), userID, kind, id, value); err != nil {
		writeListErr(w, err)
		return
	}
	httpapi.WriteOK(w)
}

// SetReaction stores a like (1) or dislike (-1) of a movie or series; 0 clears it.
func (s *Service) SetReaction(ctx context.Context, userID, kind, id string, value int) error {
	if value == 0 {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM title_reactions WHERE user_id = ? AND item_kind = ? AND item_id = ?`, userID, kind, id)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO title_reactions(user_id, item_kind, item_id, reaction, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id, item_kind, item_id) DO UPDATE SET reaction = excluded.reaction, updated_at = excluded.updated_at
	`, userID, kind, id, value, time.Now().UTC().Format(time.RFC3339))
	return err
}

// SetWatched marks a movie, an episode or every episode of a series as
// watched (completed progress, so it leaves Continue Watching) or unwatched
// (its progress is removed, so it can be recommended again).
func (s *Service) SetWatched(ctx context.Context, userID, kind, id string, watched bool) error {
	items := []string{id}
	itemKind := kind
	if kind == "series" {
		itemKind = "episode"
		rows, err := s.DB.QueryContext(ctx, `SELECT id FROM episodes WHERE series_id = ?`, id)
		if err != nil {
			return err
		}
		items = items[:0]
		for rows.Next() {
			var ep string
			if err := rows.Scan(&ep); err != nil {
				rows.Close()
				return err
			}
			items = append(items, ep)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, item := range items {
		if !watched {
			if _, err := tx.ExecContext(ctx, `DELETE FROM playback_progress WHERE user_id = ? AND item_kind = ? AND item_id = ?`, userID, itemKind, item); err != nil {
				return err
			}
			continue
		}
		dur, existed, err := knownDuration(ctx, tx, userID, itemKind, item)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO playback_progress(user_id, item_kind, item_id, media_file_id, position_ms, duration_ms, completed, updated_at)
			VALUES (?, ?, ?, '', ?, ?, 1, ?)
			ON CONFLICT(user_id, item_kind, item_id) DO UPDATE SET
				position_ms = excluded.position_ms, duration_ms = excluded.duration_ms,
				completed = 1, updated_at = excluded.updated_at
		`, userID, itemKind, item, dur, dur, now); err != nil {
			return err
		}
		if !existed {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO watch_history(id, user_id, item_kind, item_id, watched_at, position_ms) VALUES (?, ?, ?, ?, ?, ?)
			`, uuid.NewString(), userID, itemKind, item, now, dur); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// knownDuration is the item's length from its progress, a local file or an
// external source; 1 ms when nothing knows it, which still reads as finished.
func knownDuration(ctx context.Context, tx *sql.Tx, userID, kind, id string) (int64, bool, error) {
	var dur int64
	err := tx.QueryRowContext(ctx, `SELECT duration_ms FROM playback_progress WHERE user_id = ? AND item_kind = ? AND item_id = ?`, userID, kind, id).Scan(&dur)
	existed := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	if dur > 0 {
		return dur, existed, nil
	}
	q := `SELECT COALESCE(MAX(duration_ms), 0) FROM media_files WHERE movie_id = ?`
	if kind == "episode" {
		q = `SELECT COALESCE(MAX(f.duration_ms), 0) FROM media_files f JOIN media_file_episodes mfe ON mfe.media_file_id = f.id WHERE mfe.episode_id = ?`
	}
	if err := tx.QueryRowContext(ctx, q, id).Scan(&dur); err != nil {
		return 0, false, err
	}
	if dur <= 0 {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(duration_ms), 0) FROM remote_items WHERE item_kind = ? AND item_id = ?`, kind, id).Scan(&dur); err != nil {
			return 0, false, err
		}
	}
	if dur <= 0 {
		dur = 1
	}
	return dur, existed, nil
}
