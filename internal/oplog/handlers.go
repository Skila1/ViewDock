package oplog

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
)

func (s *Store) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequirePerm(auth.PermLogsRead))
		r.Get("/admin/logs", s.handleList)
	})
}

func (s *Store) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := s.List(r.Context(), Filter{
		Level:    q.Get("level"),
		Category: q.Get("category"),
		Q:        q.Get("q"),
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
