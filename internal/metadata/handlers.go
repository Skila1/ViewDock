package metadata

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/httpapi"
)

func (s *Service) Routes(r chi.Router) {
	r.Post("/movies/{id}/match", s.handleMatchMovie)
	r.Post("/series/{id}/match", s.handleMatchSeries)
	r.Post("/movies/{id}/refresh", s.handleRefresh("movie"))
	r.Post("/series/{id}/refresh", s.handleRefresh("series"))
	r.Post("/metadata/drain", s.handleDrain)
}

func (s *Service) handleMatchMovie(w http.ResponseWriter, r *http.Request) {
	s.handleMatch(w, r, "movie", chi.URLParam(r, "id"))
}

func (s *Service) handleMatchSeries(w http.ResponseWriter, r *http.Request) {
	s.handleMatch(w, r, "series", chi.URLParam(r, "id"))
}

func (s *Service) handleMatch(w http.ResponseWriter, r *http.Request, kind, id string) {
	var body struct {
		TMDBID int `json:"tmdb_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TMDBID <= 0 {
		httpapi.WriteErr(w, 400, "bad_request", "tmdb_id required")
		return
	}
	if err := s.ApplyMatch(r.Context(), kind, id, body.TMDBID, true); err != nil {
		httpapi.WriteErr(w, 400, "match", err.Error())
		return
	}
	httpapi.WriteOK(w)
}

// handleRefresh fetches a title's metadata again: from its external source
// for titles that came from one, otherwise from TMDB (by its match, or by
// looking it up again when it has none).
func (s *Service) handleRefresh(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		table := map[string]string{"movie": "movies", "series": "series"}[kind]
		var title, source string
		var year, tmdb sql.NullInt64
		err := s.DB.QueryRowContext(r.Context(), `SELECT title, year, tmdb_id, metadata_source FROM `+table+` WHERE id = ?`, id).Scan(&title, &year, &tmdb, &source)
		if errors.Is(err, sql.ErrNoRows) {
			httpapi.WriteErr(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		if err != nil {
			httpapi.WriteErr(w, http.StatusInternalServerError, "metadata", err.Error())
			return
		}
		switch {
		case source == "jellyfin" && s.RefreshRemote != nil:
			err = s.RefreshRemote(r.Context(), kind, id)
		case tmdb.Valid && tmdb.Int64 > 0:
			err = s.ApplyMatch(r.Context(), kind, id, int(tmdb.Int64), false)
		default:
			err = s.Enqueue(r.Context(), kind, id, title, int(year.Int64))
		}
		if err != nil {
			httpapi.WriteErr(w, http.StatusBadGateway, "metadata", err.Error())
			return
		}
		httpapi.WriteJSON(w, http.StatusAccepted, map[string]any{"ok": true})
	}
}

func (s *Service) handleDrain(w http.ResponseWriter, r *http.Request) {
	if err := s.RunOnce(r.Context()); err != nil {
		httpapi.WriteErr(w, 500, "metadata", err.Error())
		return
	}
	httpapi.WriteOK(w)
}
