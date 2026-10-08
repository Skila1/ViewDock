package playback

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/diagnostics"
	"github.com/viewdock/viewdock/internal/httpapi"
)

// RemoteTimelines fetches timelines of sessions recorded by media workers.
type RemoteTimelines interface {
	Timeline(ctx context.Context, sessionID string) []diagnostics.Event
}

func (a *API) handleFlightRecorder(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var events []diagnostics.Event
	if a.Flight != nil {
		events = a.Flight.Events(id)
	}
	if a.RemoteDiag != nil {
		remote := a.RemoteDiag.Timeline(r.Context(), id)
		if len(remote) > 0 {
			events = diagnostics.Merge(events, remote)
		}
	}
	if events == nil {
		events = []diagnostics.Event{}
	}
	httpapi.WriteJSON(w, http.StatusOK, events)
}

func (a *API) handleSourceReliability(w http.ResponseWriter, r *http.Request) {
	if a.Flight == nil {
		httpapi.WriteJSON(w, http.StatusOK, []any{})
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, a.Flight.Sources())
}
