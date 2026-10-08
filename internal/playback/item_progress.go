package playback

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/progress"
)

// handleItemProgress records watch progress without a live playback session.
// Offline playback and queued reconnect replays use it. A newer server record
// that differs materially is reported as a conflict instead of overwritten.
func (a *API) handleItemProgress(w http.ResponseWriter, r *http.Request) {
	p := auth.FromRequest(r)
	kind, id := chi.URLParam(r, "kind"), chi.URLParam(r, "id")
	if kind != "movie" && kind != "episode" {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "kind must be movie or episode")
		return
	}
	var body struct {
		PositionMS      int64  `json:"position_ms"`
		DurationMS      int64  `json:"duration_ms"`
		ClientUpdatedAt string `json:"client_updated_at"`
		Force           bool   `json:"force"`
	}
	if err := httpapi.ReadJSON(r, &body); err != nil || body.PositionMS < 0 {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid progress")
		return
	}
	loc, err := a.locate(r.Context(), p, kind, id, "")
	if err != nil {
		writeHidden(w, err)
		return
	}
	if a.Progress == nil {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "progress", "progress store unavailable")
		return
	}
	clientAt, _ := time.Parse(time.RFC3339, body.ClientUpdatedAt)
	if !body.Force && !clientAt.IsZero() {
		cur, err := a.Progress.Get(r.Context(), p.UserID, kind, id)
		if err != nil && !errors.Is(err, progress.ErrNotFound) {
			httpapi.WriteErr(w, http.StatusServiceUnavailable, "progress", "progress store unavailable")
			return
		}
		serverAt, _ := time.Parse(time.RFC3339, cur.UpdatedAt)
		if err == nil && serverAt.After(clientAt) && abs(cur.PositionMS-body.PositionMS) > 30_000 {
			httpapi.WriteJSON(w, http.StatusConflict, map[string]any{
				"code": "progress_conflict", "message": "a newer position exists on the server",
				"server": cur, "client": map[string]any{"position_ms": body.PositionMS, "updated_at": body.ClientUpdatedAt},
			})
			return
		}
	}
	if err := a.Progress.Put(r.Context(), p.UserID, kind, id, loc.ID, body.PositionMS, body.DurationMS); err != nil {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "progress", "progress store unavailable")
		return
	}
	a.progressStats.writes.Add(1)
	httpapi.WriteOK(w)
}
