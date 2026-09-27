package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
)

// Routes mounts the admin API and the stream proxy.
func (s *Service) Routes(r chi.Router) {
	r.Get("/media-sources/stream/{grant}/*", s.handleStream)
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/admin/media-sources", s.handleList)
		r.Post("/admin/media-sources", s.handleCreate)
		r.Post("/admin/media-sources/test", s.handleTest)
		r.Patch("/admin/media-sources/{id}", s.handleUpdate)
		r.Delete("/admin/media-sources/{id}", s.handleDelete)
		r.Get("/admin/media-sources/{id}/libraries", s.handleLibraries)
		r.Post("/admin/media-sources/{id}/sync", s.handleSync)
	})
}

type sourceBody struct {
	Name     *string   `json:"name"`
	URL      *string   `json:"url"`
	Username *string   `json:"username"`
	Password *string   `json:"password"`
	Views    *[]string `json:"libraries"`
	Enabled  *bool     `json:"enabled"`
}

func (s *Service) audit(r *http.Request, action, target string) {
	if s.Audit == nil {
		return
	}
	actor := ""
	if p := auth.FromRequest(r); p != nil {
		actor = p.UserID
	}
	s.Audit.Event(r.Context(), actor, action, target, httpapi.ClientIPString(r, s.Cfg), "")
}

func writeSourceErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotExist):
		httpapi.WriteErr(w, http.StatusNotFound, "not_found", "media source not found")
	case errors.Is(err, errNoCipher):
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "no_master_key", err.Error())
	case errors.Is(err, errUnauthorized):
		httpapi.WriteErr(w, http.StatusBadRequest, "auth_failed", "Jellyfin rejected the username or password")
	default:
		httpapi.WriteErr(w, http.StatusBadGateway, "source_unavailable", err.Error())
	}
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := s.list(r.Context())
	if err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "db", "could not list media sources")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

type testResult struct {
	ServerName string `json:"server_name"`
	Version    string `json:"version"`
	Libraries  []View `json:"libraries"`
}

// probe signs in with the given credentials and lists the account's views.
// The session token it creates is released again.
func (s *Service) probe(ctx context.Context, base, username, password string) (testResult, error) {
	c := s.newClient(base, "test-"+uuid.NewString())
	info, err := c.publicInfo(ctx)
	if err != nil {
		return testResult{}, err
	}
	_, userID, err := c.authenticate(ctx, username, password)
	if err != nil {
		return testResult{}, err
	}
	defer c.logout(context.Background())
	views, err := c.views(ctx, userID)
	if err != nil {
		return testResult{}, err
	}
	return testResult{ServerName: info.ServerName, Version: info.Version, Libraries: views}, nil
}

func (s *Service) handleTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID       string `json:"id"`
		URL      string `json:"url"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	if body.ID != "" && body.Password == "" {
		src, err := s.get(r.Context(), body.ID)
		if err != nil {
			writeSourceErr(w, err)
			return
		}
		res, err := s.testStored(r.Context(), src)
		if err != nil {
			writeSourceErr(w, err)
			return
		}
		httpapi.WriteJSON(w, http.StatusOK, res)
		return
	}
	base, err := ParseServerURL(body.URL)
	if err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_url", err.Error())
		return
	}
	res, err := s.probe(r.Context(), base, strings.TrimSpace(body.Username), body.Password)
	if err != nil {
		writeSourceErr(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, res)
}

func (s *Service) testStored(ctx context.Context, src Source) (testResult, error) {
	var res testResult
	err := s.withClient(ctx, src, func(c *client, userID string) error {
		info, err := c.publicInfo(ctx)
		if err != nil {
			return err
		}
		views, err := c.views(ctx, userID)
		if err != nil {
			return err
		}
		res = testResult{ServerName: info.ServerName, Version: info.Version, Libraries: views}
		return nil
	})
	return res, err
}

func (s *Service) handleLibraries(w http.ResponseWriter, r *http.Request) {
	src, err := s.get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeSourceErr(w, err)
		return
	}
	res, err := s.testStored(r.Context(), src)
	if err != nil {
		writeSourceErr(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, res.Libraries)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var body sourceBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return strings.TrimSpace(*p)
	}
	name, username := str(body.Name), str(body.Username)
	password := ""
	if body.Password != nil {
		password = *body.Password
	}
	base, err := ParseServerURL(str(body.URL))
	if err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_url", err.Error())
		return
	}
	if username == "" || password == "" {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "username and password are required")
		return
	}
	if _, err := s.cipher(); err != nil {
		writeSourceErr(w, err)
		return
	}
	res, err := s.probe(r.Context(), base, username, password)
	if err != nil {
		writeSourceErr(w, err)
		return
	}
	if name == "" {
		name = res.ServerName
	}
	if name == "" {
		name = "Jellyfin"
	}
	views := []string{}
	if body.Views != nil {
		views = *body.Views
	}
	id, libID := uuid.NewString(), uuid.NewString()
	pwEnc, err := s.seal("password", id, password)
	if err != nil {
		writeSourceErr(w, err)
		return
	}
	rawViews, _ := json.Marshal(views)
	enabled := body.Enabled == nil || *body.Enabled
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "db", "could not save the media source")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO libraries(id, name, root_path, content_type, uploads_enabled, created_at, updated_at)
		VALUES (?, ?, ?, 'mixed', 0, ?, ?)
	`, libID, name, "jellyfin://"+id, now, now); err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "db", "could not save the media source")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO media_sources(id, library_id, name, url, username, password_enc, views, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, libID, name, base, username, pwEnc, string(rawViews), boolInt(enabled), now, now); err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "db", "could not save the media source")
		return
	}
	if err := tx.Commit(); err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "db", "could not save the media source")
		return
	}
	s.audit(r, "media_source.create", id)
	if enabled {
		s.StartSync(id)
	}
	src, err := s.get(r.Context(), id)
	if err != nil {
		writeSourceErr(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, src)
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	src, err := s.get(r.Context(), id)
	if err != nil {
		writeSourceErr(w, err)
		return
	}
	var body sourceBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	credsChanged := false
	if body.Name != nil {
		if n := strings.TrimSpace(*body.Name); n != "" {
			src.Name = n
		}
	}
	if body.URL != nil {
		base, err := ParseServerURL(*body.URL)
		if err != nil {
			httpapi.WriteErr(w, http.StatusBadRequest, "bad_url", err.Error())
			return
		}
		credsChanged = credsChanged || base != src.URL
		src.URL = base
	}
	if body.Username != nil && strings.TrimSpace(*body.Username) != "" {
		u := strings.TrimSpace(*body.Username)
		credsChanged = credsChanged || u != src.Username
		src.Username = u
	}
	password := ""
	if body.Password != nil && *body.Password != "" {
		password = *body.Password
		credsChanged = true
	}
	if credsChanged && password == "" {
		stored, err := s.open(r.Context(), "password", id)
		if err != nil {
			writeSourceErr(w, err)
			return
		}
		password = stored
	}
	if body.Views != nil {
		src.Views = *body.Views
	}
	if body.Enabled != nil {
		src.Enabled = *body.Enabled
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rawViews, _ := json.Marshal(src.Views)
	if credsChanged {
		if _, err := s.probe(r.Context(), src.URL, src.Username, password); err != nil {
			writeSourceErr(w, err)
			return
		}
		pwEnc, err := s.seal("password", id, password)
		if err != nil {
			writeSourceErr(w, err)
			return
		}
		if _, err := s.DB.ExecContext(r.Context(), `
			UPDATE media_sources SET url = ?, username = ?, password_enc = ?, token_enc = '', remote_user = '' WHERE id = ?
		`, src.URL, src.Username, pwEnc, id); err != nil {
			httpapi.WriteErr(w, http.StatusInternalServerError, "db", "could not save the media source")
			return
		}
	}
	if _, err := s.DB.ExecContext(r.Context(), `
		UPDATE media_sources SET name = ?, views = ?, enabled = ?, updated_at = ? WHERE id = ?
	`, src.Name, string(rawViews), boolInt(src.Enabled), now, id); err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "db", "could not save the media source")
		return
	}
	_, _ = s.DB.ExecContext(r.Context(), `UPDATE libraries SET name = ?, updated_at = ? WHERE id = ?`, src.Name, now, src.LibraryID)
	s.audit(r, "media_source.update", id)
	if src.Enabled && (credsChanged || body.Views != nil || body.Enabled != nil) {
		s.StartSync(id)
	}
	out, err := s.get(r.Context(), id)
	if err != nil {
		writeSourceErr(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	src, err := s.get(r.Context(), id)
	if err != nil {
		writeSourceErr(w, err)
		return
	}
	ctx := r.Context()
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM artwork WHERE EXISTS (
		SELECT 1 FROM remote_items ri WHERE ri.source_id = ? AND ri.item_kind = artwork.item_kind AND ri.item_id = artwork.item_id)`, id)
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM media_sources WHERE id = ?`, id); err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "db", "could not delete the media source")
		return
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM libraries WHERE id = ?`, src.LibraryID); err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "db", "could not delete the media source library")
		return
	}
	s.mu.Lock()
	for tok, g := range s.grants {
		if g.sourceID == id {
			delete(s.grants, tok)
		}
	}
	s.mu.Unlock()
	s.audit(r, "media_source.delete", id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleSync(w http.ResponseWriter, r *http.Request) {
	src, err := s.get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeSourceErr(w, err)
		return
	}
	if !src.Enabled {
		httpapi.WriteErr(w, http.StatusConflict, "disabled", "enable the media source before syncing")
		return
	}
	if !s.StartSync(src.ID) {
		httpapi.WriteErr(w, http.StatusConflict, "syncing", "a sync is already running")
		return
	}
	httpapi.WriteJSON(w, http.StatusAccepted, map[string]bool{"started": true})
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
