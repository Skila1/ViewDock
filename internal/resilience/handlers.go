package resilience

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/diagnostics"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/reliability"
)

// API exposes the dashboard and reliability administration.
type API struct {
	Service *Service
	// Overrides persists administrator overrides; nil keeps them in memory
	// only, which the API reports as persisted=false.
	Overrides reliability.OverrideStore
	Audit     *audit.Log
	Cfg       config.Config
}

const (
	maxCandidates    = 50
	sessionListLimit = 200
)

// Routes mounts under /api/v1. Every route requires an administrator.
func (a *API) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/admin/resilience", a.handleDashboard)
		r.Get("/admin/resilience/sessions", a.handleSessions)
		r.Get("/admin/resilience/reliability", a.handleReliability)
		r.Get("/admin/resilience/reliability/rank", a.handleRank)
		r.Put("/admin/resilience/reliability/overrides", a.handlePutOverride)
		r.Delete("/admin/resilience/reliability/overrides", a.handleDeleteOverride)
	})
}

func (a *API) handleDashboard(w http.ResponseWriter, r *http.Request) {
	httpapi.WriteJSON(w, http.StatusOK, a.Service.Snapshot(r.Context()))
}

func (a *API) handleSessions(w http.ResponseWriter, r *http.Request) {
	local := a.Service.deps.Flight.Sessions(sessionListLimit)
	var remote []diagnostics.SessionSummary
	if a.Service.deps.RemoteSessions != nil {
		remote = a.Service.deps.RemoteSessions(r.Context())
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"items": diagnostics.MergeSessions(local, remote, sessionListLimit)})
}

func (a *API) tracker(w http.ResponseWriter) *reliability.Tracker {
	tr := a.Service.deps.Reliability
	if tr == nil {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "reliability_unavailable", errNoTracker.Error())
	}
	return tr
}

func (a *API) handleReliability(w http.ResponseWriter, r *http.Request) {
	if a.tracker(w) == nil {
		return
	}
	d, _ := a.Service.reliabilityData()
	httpapi.WriteJSON(w, http.StatusOK, d)
}

// handleRank explains how candidates would be ordered for a viewer, for
// example ?candidates=node-a,node-b&region=eu&device_class=tv.
func (a *API) handleRank(w http.ResponseWriter, r *http.Request) {
	tr := a.tracker(w)
	if tr == nil {
		return
	}
	q := r.URL.Query()
	var cands []string
	for _, c := range strings.Split(q.Get("candidates"), ",") {
		if c = strings.TrimSpace(c); c != "" {
			cands = append(cands, c)
		}
	}
	if len(cands) == 0 {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "candidates required")
		return
	}
	if len(cands) > maxCandidates {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "at most 50 candidates")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, tr.Rank(reliability.RankRequest{
		Candidates: cands, Region: q.Get("region"), DeviceClass: q.Get("device_class"),
	}))
}

func (a *API) actor(r *http.Request) string {
	if p := auth.FromRequest(r); p != nil {
		return p.UserID
	}
	return ""
}

func (a *API) audit(r *http.Request, action, target, detail string) {
	if a.Audit != nil {
		a.Audit.Event(context.WithoutCancel(r.Context()), a.actor(r), action, target, httpapi.ClientIPString(r, a.Cfg), detail)
	}
}

func (a *API) handlePutOverride(w http.ResponseWriter, r *http.Request) {
	tr := a.tracker(w)
	if tr == nil {
		return
	}
	var body struct {
		Source string `json:"source"`
		Mode   string `json:"mode"`
		Note   string `json:"note"`
	}
	if err := httpapi.ReadJSON(r, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	o, err := reliability.Override{Source: body.Source, Mode: body.Mode, Note: body.Note, ActorID: a.actor(r)}.Normalize()
	if err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "invalid_override", err.Error())
		return
	}
	o.UpdatedAt = a.Service.now().UTC()
	if a.Overrides != nil {
		if err := a.Overrides.Put(r.Context(), o); err != nil {
			a.Service.logErr("override store", err)
			writeStoreErr(w, err)
			return
		}
	}
	o, _ = tr.SetOverride(o)
	a.audit(r, "reliability.override", o.Source, o.Mode)
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"override": o, "persisted": a.Overrides != nil})
}

func (a *API) handleDeleteOverride(w http.ResponseWriter, r *http.Request) {
	tr := a.tracker(w)
	if tr == nil {
		return
	}
	source := strings.TrimSpace(r.URL.Query().Get("source"))
	if source == "" {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "source required")
		return
	}
	if a.Overrides != nil {
		if err := a.Overrides.Delete(r.Context(), source); err != nil {
			a.Service.logErr("override store", err)
			writeStoreErr(w, err)
			return
		}
	}
	tr.ClearOverride(source)
	a.audit(r, "reliability.override_clear", source, "")
	w.WriteHeader(http.StatusNoContent)
}

func writeStoreErr(w http.ResponseWriter, err error) {
	if errors.Is(err, reliability.ErrInvalidMode) || errors.Is(err, reliability.ErrInvalidSource) {
		httpapi.WriteErr(w, http.StatusBadRequest, "invalid_override", err.Error())
		return
	}
	w.Header().Set("Retry-After", "5")
	httpapi.WriteErr(w, http.StatusServiceUnavailable, "database_unavailable", "the override could not be saved; try again shortly")
}
