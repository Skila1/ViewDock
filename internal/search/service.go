package search

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/library"
)

type Service struct {
	DB *sql.DB
}

func New(db *sql.DB) *Service { return &Service{DB: db} }

func (s *Service) Routes(r chi.Router) {
	r.Get("/search", s.handle)
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
	hits, err := s.Query(r.Context(), q, library.GrantedIDsFrom(r.Context()))
	if err != nil {
		httpapi.WriteErr(w, 400, "search", err.Error())
		return
	}
	httpapi.WriteJSON(w, 200, hits)
}

func (s *Service) Query(ctx context.Context, q string, grantedIDs []string) ([]Hit, error) {
	match := ftsQuery(q)
	rows, err := s.DB.QueryContext(ctx, `
		SELECT item_kind, item_id, title, year FROM media_fts WHERE media_fts MATCH ? LIMIT 50
	`, match)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Hit{}
	filter := map[string]bool{}
	for _, id := range grantedIDs {
		filter[id] = true
	}
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.ItemKind, &h.ItemID, &h.Title, &h.Year); err != nil {
			return nil, err
		}
		if len(filter) > 0 {
			libID, err := s.libraryOf(ctx, h.ItemKind, h.ItemID)
			if err != nil || !filter[libID] {
				continue
			}
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Service) libraryOf(ctx context.Context, kind, id string) (string, error) {
	var lib string
	var err error
	switch kind {
	case "movie":
		err = s.DB.QueryRowContext(ctx, `SELECT library_id FROM movies WHERE id = ?`, id).Scan(&lib)
	case "series":
		err = s.DB.QueryRowContext(ctx, `SELECT library_id FROM series WHERE id = ?`, id).Scan(&lib)
	case "episode":
		err = s.DB.QueryRowContext(ctx, `
			SELECT se.library_id FROM episodes e JOIN series se ON se.id = e.series_id WHERE e.id = ?
		`, id).Scan(&lib)
	default:
		err = sql.ErrNoRows
	}
	return lib, err
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
