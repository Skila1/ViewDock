package runtimecfg

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/httpapi"
)

func (s *Service) Routes(cfg config.Config) func(chi.Router) {
	return func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(auth.RequirePerm(auth.PermSettingsManage))
			r.Get("/admin/config", s.handleGet)
			r.Put("/admin/config", s.handlePut(cfg))
			r.Get("/admin/config/history", s.handleHistory)
			r.Post("/admin/config/rollback", s.handleRollback(cfg))
		})
	}
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	views, version := s.Views()
	cipher := ""
	if c := s.KV.Cipher(); c != nil {
		cipher = c.ID
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"version": version, "settings": views, "master_key_id": cipher})
}

func (s *Service) writeApplyErr(w http.ResponseWriter, err error) {
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		httpapi.WriteJSON(w, http.StatusBadRequest, map[string]any{"code": "invalid_setting", "message": ve.Error(), "key": ve.Key})
	case errors.Is(err, ErrVersionConflict):
		views, version := s.Views()
		httpapi.WriteJSON(w, http.StatusConflict, map[string]any{"code": "version_conflict", "message": err.Error(), "version": version, "settings": views})
	case errors.Is(err, ErrNoCipher):
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "no_master_key", err.Error())
	default:
		slog.Error("runtime configuration save failed", "category", "config", "err", err)
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "config", "configuration could not be saved")
	}
}

func (s *Service) handlePut(cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Version int64              `json:"version"`
			Values  map[string]*string `json:"values"`
			Note    string             `json:"note"`
		}
		if err := httpapi.ReadJSON(r, &body); err != nil {
			httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
			return
		}
		if len(body.Note) > 200 {
			body.Note = body.Note[:200]
		}
		changes := make(map[string]Change, len(body.Values))
		for k, v := range body.Values {
			if v == nil {
				changes[k] = Change{Reset: true}
			} else {
				changes[k] = Change{Value: *v}
			}
		}
		p := auth.FromRequest(r)
		version, err := s.Apply(r.Context(), p.UserID, httpapi.ClientIPString(r, cfg), changes, body.Version, body.Note)
		if err != nil {
			s.writeApplyErr(w, err)
			return
		}
		views, _ := s.Views()
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"version": version, "settings": views})
	}
}

func (s *Service) handleHistory(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.History(r.Context(), limit)
	if err != nil {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "config", "history unavailable")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) handleRollback(cfg config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Target  int64 `json:"target_version"`
			Version int64 `json:"version"`
		}
		if err := httpapi.ReadJSON(r, &body); err != nil {
			httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
			return
		}
		p := auth.FromRequest(r)
		version, err := s.Rollback(r.Context(), p.UserID, httpapi.ClientIPString(r, cfg), body.Target, body.Version)
		if err != nil {
			s.writeApplyErr(w, err)
			return
		}
		views, _ := s.Views()
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"version": version, "settings": views})
	}
}
