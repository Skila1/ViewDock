package library

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
)

func (s *Service) moveRoutes(r chi.Router) {
	r.Post("/library-moves/preview", s.handleMovePreview)
	r.Post("/library-moves", s.handleMoveStart)
	r.Get("/library-moves/{id}", s.handleMoveJob)
}

func canManage(w http.ResponseWriter, r *http.Request) (*auth.Principal, bool) {
	p := auth.FromRequest(r)
	if p == nil || !p.IsUser() || !p.HasPerm(auth.PermLibrariesManage) {
		httpapi.WriteErr(w, http.StatusForbidden, "forbidden", "permission required")
		return nil, false
	}
	return p, true
}

func readMoveRequest(w http.ResponseWriter, r *http.Request) (MoveRequest, bool) {
	var req MoveRequest
	if err := httpapi.ReadJSON(r, &req); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return req, false
	}
	return req, true
}

func writeMoveErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpapi.WriteErr(w, http.StatusNotFound, "not_found", "library not found")
	case errors.Is(err, ErrMoveBusy), errors.Is(err, ErrScanRunning):
		httpapi.WriteErr(w, http.StatusConflict, "move_busy", err.Error())
	default:
		httpapi.WriteErr(w, http.StatusBadRequest, "move", err.Error())
	}
}

// handleMovePreview shows which titles can move to the destination and why
// the others cannot, without changing anything.
func (s *Service) handleMovePreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := canManage(w, r); !ok {
		return
	}
	req, ok := readMoveRequest(w, r)
	if !ok {
		return
	}
	plan, err := s.Preview(r.Context(), req)
	if err != nil {
		writeMoveErr(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, plan)
}

func (s *Service) handleMoveStart(w http.ResponseWriter, r *http.Request) {
	p, ok := canManage(w, r)
	if !ok {
		return
	}
	req, ok := readMoveRequest(w, r)
	if !ok {
		return
	}
	job, err := s.StartMove(r.Context(), req, p.UserID)
	if err != nil {
		writeMoveErr(w, err)
		return
	}
	s.audit(r, p.UserID, "library.move.start", job.DestinationLibraryID, "job="+job.ID)
	httpapi.WriteJSON(w, http.StatusAccepted, job)
}

func (s *Service) handleMoveJob(w http.ResponseWriter, r *http.Request) {
	if _, ok := canManage(w, r); !ok {
		return
	}
	job, err := s.GetMoveJob(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpapi.WriteErr(w, http.StatusNotFound, "not_found", "move not found")
			return
		}
		httpapi.WriteErr(w, http.StatusInternalServerError, "move", err.Error())
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, job)
}
