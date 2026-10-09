package oplog

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
)

func (s *Store) Routes(r chi.Router) {
	r.Post("/client-logs", s.handleIngest)
	r.Post("/error-reports", s.handleReport)
	r.Group(func(r chi.Router) {
		r.Use(auth.RequirePerm(auth.PermLogsRead))
		r.Get("/admin/logs", s.handleList)
		r.Get("/admin/logs/stats", s.handleStats)
		r.Get("/admin/audit", s.handleAudit)
	})
	r.With(auth.RequirePerm(auth.PermSettingsManage)).Delete("/admin/logs", s.handlePrune)
}

func (s *Store) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := s.List(r.Context(), Filter{
		Level:    q.Get("level"),
		Category: q.Get("category"),
		Q:        q.Get("q"),
		Actor:    q.Get("actor"),
		Limit:    limit,
		After:    q.Get("after"),
	})
	if err != nil {
		httpapi.WriteErr(w, 500, "logs", err.Error())
		return
	}
	next := ""
	if len(list) > 0 {
		next = list[len(list)-1].CreatedAt
	}
	httpapi.WriteJSON(w, 200, map[string]any{"items": list, "next": next})
}

func (s *Store) handleAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := audit.New(s.DB).List(r.Context(), audit.Filter{
		Action: q.Get("action"),
		Actor:  q.Get("actor"),
		Q:      q.Get("q"),
		Before: q.Get("before"),
		Limit:  limit,
	})
	if err != nil {
		httpapi.WriteErr(w, 500, "audit", err.Error())
		return
	}
	for i := range list {
		list[i].Target = Redact(list[i].Target)
		list[i].Detail = Redact(list[i].Detail)
	}
	next := ""
	if len(list) > 0 {
		next = list[len(list)-1].At
	}
	httpapi.WriteJSON(w, 200, map[string]any{"items": list, "next": next})
}

func (s *Store) handleStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.Stats(r.Context())
	if err != nil {
		httpapi.WriteErr(w, 500, "logs", err.Error())
		return
	}
	httpapi.WriteJSON(w, 200, st)
}

// handlePrune deletes logs: those before ?before=<RFC 3339 time>, or all of
// them with ?all=true. The prune itself is kept in the audit log.
func (s *Store) handlePrune(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var before time.Time
	switch {
	case q.Get("all") == "true":
	case q.Get("before") != "":
		t, err := time.Parse(time.RFC3339, q.Get("before"))
		if err != nil {
			httpapi.WriteErr(w, 400, "bad_request", "before must be an RFC 3339 time")
			return
		}
		before = t
	default:
		httpapi.WriteErr(w, 400, "bad_request", "give before=<time> or all=true")
		return
	}
	n, err := s.Prune(r.Context(), before)
	if err != nil {
		httpapi.WriteErr(w, 500, "logs", err.Error())
		return
	}
	actor, ip := "", ""
	if p := auth.FromRequest(r); p != nil {
		actor = p.ID()
	}
	if s.ClientIP != nil {
		ip = s.ClientIP(r)
	}
	target, detail := "all", fmt.Sprintf("%d rows", n)
	if !before.IsZero() {
		target = "before " + before.UTC().Format(time.RFC3339)
	}
	audit.New(s.DB).Event(r.Context(), actor, "logs.prune", target, ip, detail)
	httpapi.WriteJSON(w, 200, map[string]any{"deleted": n})
}
