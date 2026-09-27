package metadata

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/httpapi"
)

func (s *Service) Routes(r chi.Router) {
	r.Post("/movies/{id}/match", s.handleMatchMovie)
	r.Post("/series/{id}/match", s.handleMatchSeries)
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

func (s *Service) handleDrain(w http.ResponseWriter, r *http.Request) {
	if err := s.RunOnce(r.Context()); err != nil {
		httpapi.WriteErr(w, 500, "metadata", err.Error())
		return
	}
	httpapi.WriteOK(w)
}
