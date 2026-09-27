package search

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/library"
)

type Service struct {
	DB *sql.DB
}

func New(db *sql.DB) *Service { return &Service{DB: db} }

func (s *Service) Routes(r chi.Router) {
	r.Get("/search", s.handle)
	r.Get("/search/smart", s.handleSmart)
}

func (s *Service) handleSmart(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		httpapi.WriteJSON(w, 200, map[string]any{"items": []Hit{}})
		return
	}
	p := auth.FromRequest(r)
	userID := ""
	if p != nil && p.IsUser() {
		userID = p.UserID
	}
	hits, err := s.Smart(viewerContext(r), q, library.GrantedIDsFrom(r.Context()), userID)
	if err != nil {
		writeSearchErr(w, err)
		return
	}
	httpapi.WriteJSON(w, 200, map[string]any{"items": hits})
}

type Hit struct {
	ItemKind string `json:"item_kind"`
	ItemID   string `json:"item_id"`
	Title    string `json:"title"`
	Year     string `json:"year,omitempty"`
}

func (s *Service) handle(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		httpapi.WriteJSON(w, 200, []Hit{})
		return
	}
	hits, err := s.Query(viewerContext(r), q, library.GrantedIDsFrom(r.Context()))
	if err != nil {
		writeSearchErr(w, err)
		return
	}
	httpapi.WriteJSON(w, 200, hits)
}

// viewerContext guarantees the signed-in user is visible to content
// restriction checks even when the catalogue middleware did not set it.
func viewerContext(r *http.Request) context.Context {
	ctx := r.Context()
	if p := auth.FromRequest(r); p.IsUser() && library.UserIDFrom(ctx) == "" {
		ctx = library.WithUserID(ctx, p.UserID)
	}
	return ctx
}

// Query searches the catalogue. grantedIDs nil means unrestricted (admin); a
// non-nil slice, including an empty one, restricts hits to those libraries.
func (s *Service) Query(ctx context.Context, q string, grantedIDs []string) ([]Hit, error) {
	if grantedIDs != nil && len(grantedIDs) == 0 {
		return []Hit{}, nil
	}
	var rows *sql.Rows
	var err error
	if db.IsPostgres(s.DB) {
		words := strings.Fields(q)
		if len(words) == 0 {
			return []Hit{}, nil
		}
		where := make([]string, 0, len(words))
		args := make([]any, 0, len(words))
		for _, word := range words {
			where = append(where, `(f.title LIKE ? OR f.extra LIKE ?)`)
			pattern := "%" + escapeLike(word) + "%"
			args = append(args, pattern, pattern)
		}
		rows, err = s.DB.QueryContext(ctx, `SELECT f.item_kind, f.item_id, f.title, f.year FROM media_fts f WHERE `+strings.Join(where, " AND ")+` ORDER BY f.title LIMIT 200`, args...)
	} else {
		rows, err = s.DB.QueryContext(ctx, `
			SELECT item_kind, item_id, title, year FROM media_fts WHERE media_fts MATCH ? LIMIT 200
		`, ftsQuery(q))
	}
	if err != nil {
		return nil, err
	}
	var hits []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.ItemKind, &h.ItemID, &h.Title, &h.Year); err != nil {
			rows.Close()
			return nil, err
		}
		hits = append(hits, h)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rest, err := library.RestrictionFor(ctx, s.DB, library.UserIDFrom(ctx))
	if err != nil {
		return nil, err
	}
	out := []Hit{}
	filter := map[string]bool{}
	for _, id := range grantedIDs {
		filter[id] = true
	}
	for _, h := range hits {
		if grantedIDs != nil || rest.Active() {
			libID, age, err := library.ItemRating(ctx, s.DB, h.ItemKind, h.ItemID)
			if err != nil || (grantedIDs != nil && !filter[libID]) || !rest.Permits(age) {
				continue
			}
		}
		out = append(out, h)
		if len(out) == 50 {
			break
		}
	}
	return out, nil
}

func writeSearchErr(w http.ResponseWriter, err error) {
	if errors.Is(err, library.ErrUnavailable) {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "catalogue_unavailable", library.ErrUnavailable.Error())
		return
	}
	slog.Error("search failed", "category", "search", "err", err)
	httpapi.WriteErr(w, http.StatusBadRequest, "search", "the search could not be completed")
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func ftsQuery(q string) string {
	var parts []string
	for _, p := range strings.Fields(q) {
		p = strings.ReplaceAll(p, `"`, "")
		p = strings.ReplaceAll(p, `'`, "")
		if p == "" {
			continue
		}
		parts = append(parts, `"`+p+`"*`)
	}
	if len(parts) == 0 {
		return `""`
	}
	return strings.Join(parts, " ")
}
