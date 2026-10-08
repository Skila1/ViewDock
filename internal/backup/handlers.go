package backup

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
)

// Routes mounts the admin backup API under /api/v1.
func (s *Service) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/admin/backups", s.handleList)
		r.Post("/admin/backups", s.handleCreate)
		r.Post("/admin/backups/destination/check", s.handleCheck)
		r.Get("/admin/backups/{id}/download", s.handleDownload)
		r.Post("/admin/backups/{id}/validate", s.handleValidate)
		r.Delete("/admin/backups/{id}", s.handleDelete)
	})
}

// Summary is one backup in the admin list.
type Summary struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	Trigger       string    `json:"trigger"`
	AppVersion    string    `json:"app_version"`
	Dialect       string    `json:"dialect"`
	SchemaVersion int       `json:"schema_version"`
	CreatedAt     time.Time `json:"created_at"`
	CreatedBy     string    `json:"created_by,omitempty"`
	MasterKeyID   string    `json:"master_key_id,omitempty"`
	Size          int64     `json:"size"`
	Files         int       `json:"files"`
	Tables        int       `json:"tables"`
	Rows          int64     `json:"rows"`
}

func summarize(m Manifest) Summary {
	out := Summary{
		ID: m.ID, Kind: m.Kind, Trigger: m.Trigger, AppVersion: m.AppVersion, Dialect: m.Dialect,
		SchemaVersion: m.SchemaVersion, CreatedAt: m.CreatedAt, CreatedBy: m.CreatedBy, MasterKeyID: m.MasterKeyID,
		Size: m.TotalSize(), Files: len(m.Files), Tables: len(m.Tables),
	}
	for _, t := range m.Tables {
		out.Rows += t.Rows
	}
	return out
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	status := s.Status()
	body := map[string]any{"status": status, "items": []Summary{}}
	list, err := s.List(r.Context())
	if err != nil {
		s.log().Warn("backup list failed", "category", "backup", "err", err)
		body["destination_error"] = "The backup destination could not be read. Check the backup settings and server logs."
		httpapi.WriteJSON(w, http.StatusOK, body)
		return
	}
	items := make([]Summary, 0, len(list))
	for _, m := range list {
		items = append(items, summarize(m))
	}
	body["items"] = items
	httpapi.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	p := auth.FromRequest(r)
	// The backup finishes even if the browser disconnects.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), createTimeout)
	defer cancel()
	m, err := s.Create(ctx, TriggerManual, p.UserID, httpapi.ClientIPString(r, s.Cfg))
	if err != nil {
		s.writeErr(w, err, "create")
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, summarize(m))
}

func (s *Service) handleCheck(w http.ResponseWriter, r *http.Request) {
	if err := s.CheckDestination(r.Context()); err != nil {
		s.log().Warn("backup destination check failed", "category", "backup", "err", err)
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "The backup destination is unreachable or misconfigured. See server logs for details."})
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "The backup destination is reachable."})
}

func (s *Service) handleDownload(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.Manifest(r.Context(), id); err != nil {
		s.writeErr(w, err, "download")
		return
	}
	p := auth.FromRequest(r)
	s.Audit.Event(r.Context(), p.UserID, "backup.download", id, httpapi.ClientIPString(r, s.Cfg), "")
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.tar.gz"`)
	w.Header().Set("Cache-Control", "no-store")
	if err := s.WriteArchive(r.Context(), id, w); err != nil {
		// Headers are already sent; the truncated archive fails to extract.
		s.log().Error("backup download failed", "category", "backup", "id", id, "err", err)
	}
}

func (s *Service) handleValidate(w http.ResponseWriter, r *http.Request) {
	p := auth.FromRequest(r)
	rep, err := s.Validate(r.Context(), chi.URLParam(r, "id"), p.UserID, httpapi.ClientIPString(r, s.Cfg))
	if err != nil {
		s.writeErr(w, err, "validation")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, rep)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	p := auth.FromRequest(r)
	if err := s.Delete(r.Context(), chi.URLParam(r, "id"), p.UserID, httpapi.ClientIPString(r, s.Cfg)); err != nil {
		s.writeErr(w, err, "delete")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) writeErr(w http.ResponseWriter, err error, op string) {
	switch {
	case errors.Is(err, ErrInvalidID):
		httpapi.WriteErr(w, http.StatusBadRequest, "invalid_id", "invalid backup id")
	case errors.Is(err, ErrNotFound):
		httpapi.WriteErr(w, http.StatusNotFound, "not_found", "backup not found")
	case errors.Is(err, ErrBusy):
		httpapi.WriteErr(w, http.StatusConflict, "backup_running", "a backup is already running")
	case errors.Is(err, ErrManifest):
		s.log().Warn("backup manifest rejected", "category", "backup", "op", op, "err", err)
		httpapi.WriteErr(w, http.StatusUnprocessableEntity, "invalid_backup", "the backup manifest is invalid")
	case errors.Is(err, ErrDestination):
		s.log().Error("backup destination unavailable", "category", "backup", "op", op, "err", err)
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "destination_unavailable", "the backup destination is not configured correctly")
	default:
		s.log().Error("backup operation failed", "category", "backup", "op", op, "err", err)
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "backup_failed", "backup "+op+" failed; see server logs")
	}
}
