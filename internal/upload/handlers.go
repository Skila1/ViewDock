package upload

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/library"
)

func (s *Service) Routes(r chi.Router) {
	r.Post("/uploads", s.handleCreate)
	r.Head("/uploads/{id}", s.handleHead)
	r.Put("/uploads/{id}", s.handlePut)
	r.Delete("/uploads/{id}", s.handleDelete)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LibraryID string `json:"library_id"`
		Filename  string `json:"filename"`
		Size      int64  `json:"size"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteErr(w, 400, "bad_request", "invalid json")
		return
	}
	createdBy := library.UserIDFrom(r.Context())
	u, err := s.Create(r.Context(), body.LibraryID, body.Filename, body.Size, createdBy)
	if err != nil {
		writeUpErr(w, err)
		return
	}
	w.Header().Set("Upload-Offset", "0")
	w.Header().Set("Location", "/api/v1/uploads/"+u.ID)
	httpapi.WriteJSON(w, 201, u)
}

func (s *Service) handleHead(w http.ResponseWriter, r *http.Request) {
	u, err := s.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeUpErr(w, err)
		return
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(u.Offset, 10))
	w.Header().Set("Upload-Length", strconv.FormatInt(u.Size, 10))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

func (s *Service) handlePut(w http.ResponseWriter, r *http.Request) {
	off, _ := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	u, err := s.WriteAt(r.Context(), chi.URLParam(r, "id"), off, r.Body)
	if err != nil {
		writeUpErr(w, err)
		return
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(u.Offset, 10))
	httpapi.WriteJSON(w, 200, map[string]any{"offset": u.Offset, "status": u.Status})
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeUpErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeUpErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, library.ErrNotFound):
		httpapi.WriteErr(w, 404, "not_found", "not found")
	case errors.Is(err, ErrUploadsDisabled):
		httpapi.WriteErr(w, 403, "uploads_disabled", err.Error())
	case errors.Is(err, ErrOffset):
		httpapi.WriteErr(w, 409, "offset", err.Error())
	default:
		httpapi.WriteErr(w, 400, "upload", err.Error())
	}
}
