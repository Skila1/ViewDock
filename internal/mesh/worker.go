package mesh

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/nodeauth"
	"github.com/viewdock/viewdock/internal/playback"
	"github.com/viewdock/viewdock/internal/version"
)

const maxSignedBody = 1 << 20

// Worker serves the control plane on a media worker.
type Worker struct {
	Verifier *nodeauth.Verifier
	Play     *playback.API
	Log      *slog.Logger
	Draining func() bool
}

// Guard admits only requests signed with this node's credential.
func (wk *Worker) Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := wk.Verifier.VerifyRequest(r, maxSignedBody); err != nil {
			if errors.Is(err, nodeauth.ErrTooLarge) {
				httpapi.WriteErr(w, http.StatusRequestEntityTooLarge, "too_large", "request body too large")
				return
			}
			if wk.Log != nil {
				wk.Log.Warn("rejected unsigned or invalid node request", "category", "mesh", "path", r.URL.Path, "reason", err.Error())
			}
			httpapi.WriteErr(w, http.StatusUnauthorized, "node_auth", "node authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Routes mounts the worker API under /api/v1.
func (wk *Worker) Routes(r chi.Router) {
	r.Get("/node/status", wk.handleStatus)
	r.Post("/node/playback/sessions", wk.handleCreate)
	r.Get("/node/diagnostics/sessions", wk.handleDiagSessions)
	r.Get("/node/diagnostics/flight-recorder/{id}", wk.handleDiagTimeline)
	wk.Play.SessionRoutes(r)
}

func (wk *Worker) handleDiagSessions(w http.ResponseWriter, _ *http.Request) {
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"items": wk.Play.Flight.Sessions(remoteSessionLimit)})
}

func (wk *Worker) handleDiagTimeline(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !sessionIDRE.MatchString(id) {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid session id")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, wk.Play.Flight.Events(id))
}

func (wk *Worker) handleStatus(w http.ResponseWriter, _ *http.Request) {
	draining := wk.Draining != nil && wk.Draining()
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"role":             "worker",
		"version":          version.Version,
		"sessions":         len(wk.Play.Reg.List()),
		"transcode_slots":  wk.Play.Lim.Capacity(),
		"transcode_active": wk.Play.Lim.Active(),
		"hw_available":     wk.Play.HW.Available,
		"draining":         draining,
	})
}

func (wk *Worker) handleCreate(w http.ResponseWriter, r *http.Request) {
	var a Assertion
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil || len(a.Request) == 0 {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid assertion")
		return
	}
	p := a.Principal
	switch {
	case p.Kind == "user" && p.UserID != "":
	case p.Kind == "guest_share" && p.GuestSessionID != "":
	default:
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid principal")
		return
	}
	ctx := auth.WithPrincipal(r.Context(), &p)
	if a.PartyAccess {
		ctx = playback.WithPartyAccess(ctx)
	}
	inner := r.Clone(ctx)
	inner.Body = io.NopCloser(bytes.NewReader(a.Request))
	inner.ContentLength = int64(len(a.Request))
	for _, h := range []string{"X-Forwarded-For", "X-Real-Ip", "Forwarded", "Cf-Connecting-Ip"} {
		inner.Header.Del(h)
	}
	if ip := net.ParseIP(a.ClientIP); ip != nil {
		inner.RemoteAddr = net.JoinHostPort(ip.String(), "0")
	}
	wk.Play.ServeCreate(w, inner)
}
