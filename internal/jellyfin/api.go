package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		r.Get("/admin/media-sources/{id}/events", s.handleEvents)
		r.Get("/admin/media-sources/{id}/activity", s.handleActivity)
	})
}

type sourceBody struct {
	Name       *string   `json:"name"`
	URL        *string   `json:"url"`
	AuthMode   *string   `json:"auth_mode"`
	Username   *string   `json:"username"`
	Password   *string   `json:"password"`
	APIKey     *string   `json:"api_key"`
	RemoteUser *string   `json:"remote_user_id"`
	Views      *[]string `json:"libraries"`
	Enabled    *bool     `json:"enabled"`
	Policy     *Policy   `json:"policy"`
}

func trimmed(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

func secret(p *string) string {
	if p == nil {
		return ""
	}
	return *p
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
		httpapi.WriteErr(w, http.StatusBadRequest, "auth_failed", "Jellyfin rejected the credentials")
	case errors.Is(err, errBlocked):
		httpapi.WriteErr(w, http.StatusForbidden, "blocked", err.Error())
	case errors.Is(err, errBadInput):
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", strings.TrimPrefix(err.Error(), errBadInput.Error()+": "))
	default:
		httpapi.WriteErr(w, http.StatusBadGateway, "source_unavailable", err.Error())
	}
}

var errBadInput = errors.New("invalid media source")

func badInput(msg string) error { return fmt.Errorf("%w: %s", errBadInput, msg) }

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := s.list(r.Context())
	if err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "db", "could not list media sources")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

type testResult struct {
	ServerName string       `json:"server_name"`
	Version    string       `json:"version"`
	Libraries  []View       `json:"libraries"`
	Users      []RemoteUser `json:"users,omitempty"`
	Account    *RemoteUser  `json:"account,omitempty"`
}

// credential is what an admin supplied for a source.
type credential struct {
	mode, username, password, apiKey, remoteUser string
}

// verify checks a credential against the server without storing anything.
// An account sign in is logged out again. An API key is never logged out;
// with no user chosen yet it only lists the users it can browse as.
func (s *Service) verify(ctx context.Context, base string, cred credential) (testResult, error) {
	c := s.newClient(base, "test-"+uuid.NewString(), DefaultPolicy())
	info, err := c.publicInfo(ctx)
	if err != nil {
		return testResult{}, err
	}
	res := testResult{ServerName: info.ServerName, Version: info.Version, Libraries: []View{}}
	var user RemoteUser
	switch cred.mode {
	case AuthAPIKey:
		if cred.apiKey == "" {
			return testResult{}, badInput("an API key is required")
		}
		c.token, c.apiKey = cred.apiKey, true
		users, err := c.users(ctx)
		if err != nil {
			return testResult{}, err
		}
		res.Users = users
		if cred.remoteUser == "" {
			return res, nil
		}
		found := false
		for _, u := range users {
			if sameID(u.ID, cred.remoteUser) {
				user, found = u, true
			}
		}
		if !found {
			return testResult{}, badInput("the selected Jellyfin user does not exist")
		}
	case AuthPassword:
		if cred.username == "" || cred.password == "" {
			return testResult{}, badInput("username and password are required")
		}
		_, user, err = c.authenticate(ctx, cred.username, cred.password)
		if err != nil {
			return testResult{}, err
		}
		defer c.logout(context.Background())
	default:
		return testResult{}, badInput("auth_mode must be password or api_key")
	}
	views, err := c.views(ctx, user.ID)
	if err != nil {
		return testResult{}, err
	}
	res.Libraries, res.Account = views, &user
	return res, nil
}

func (s *Service) handleTest(w http.ResponseWriter, r *http.Request) {
	var body sourceBody
	var id string
	raw := struct {
		ID string `json:"id"`
		sourceBody
	}{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	id, body = raw.ID, raw.sourceBody
	var base string
	var cred credential
	if id != "" {
		src, err := s.get(r.Context(), id)
		if err != nil {
			writeSourceErr(w, err)
			return
		}
		base, cred, err = s.mergeCredential(r.Context(), src, body)
		if err != nil {
			writeSourceErr(w, err)
			return
		}
	} else {
		var err error
		if base, err = ParseServerURL(trimmed(body.URL)); err != nil {
			httpapi.WriteErr(w, http.StatusBadRequest, "bad_url", err.Error())
			return
		}
		cred = credential{mode: modeOf(body.AuthMode, AuthPassword), username: trimmed(body.Username),
			password: secret(body.Password), apiKey: trimmed(body.APIKey), remoteUser: trimmed(body.RemoteUser)}
	}
	res, err := s.verify(r.Context(), base, cred)
	if id != "" {
		detail := "connection test passed"
		if err != nil {
			detail = "connection test failed"
		}
		s.event(r.Context(), id, "test", err == nil, detail)
	}
	if err != nil {
		writeSourceErr(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, res)
}

func modeOf(p *string, fallback string) string {
	if m := trimmed(p); m != "" {
		return m
	}
	return fallback
}

// mergeCredential combines a stored source with changed fields, loading
// stored secrets only when the auth mode is unchanged.
func (s *Service) mergeCredential(ctx context.Context, src Source, body sourceBody) (string, credential, error) {
	base := src.URL
	if body.URL != nil {
		var err error
		if base, err = ParseServerURL(*body.URL); err != nil {
			return "", credential{}, badInput(err.Error())
		}
	}
	cred := credential{mode: modeOf(body.AuthMode, src.AuthMode), username: src.Username, remoteUser: src.RemoteUser}
	if u := trimmed(body.Username); u != "" {
		cred.username = u
	}
	if u := trimmed(body.RemoteUser); u != "" {
		cred.remoteUser = u
	}
	cred.password, cred.apiKey = secret(body.Password), trimmed(body.APIKey)
	sameMode := cred.mode == src.AuthMode
	var err error
	if cred.mode == AuthPassword && cred.password == "" && sameMode {
		cred.password, err = s.open(ctx, "password", src.ID)
	}
	if cred.mode == AuthAPIKey && cred.apiKey == "" && sameMode {
		cred.apiKey, err = s.open(ctx, "api_key", src.ID)
	}
	return base, cred, err
}

func (s *Service) handleLibraries(w http.ResponseWriter, r *http.Request) {
	src, err := s.get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeSourceErr(w, err)
		return
	}
	var views []View
	err = s.withClient(r.Context(), src, func(c *client, userID string) error {
		views, err = c.views(r.Context(), userID)
		return err
	})
	if err != nil {
		writeSourceErr(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, views)
}

// storeCredential saves a verified credential and clears the other mode's
// secrets, so a source only ever holds the credential it uses.
func (s *Service) storeCredential(ctx context.Context, id, base string, cred credential, account *RemoteUser) error {
	var pwEnc, keyEnc, remote, remoteName, username string
	var err error
	switch cred.mode {
	case AuthPassword:
		if pwEnc, err = s.seal("password", id, cred.password); err != nil {
			return err
		}
		username = cred.username
	case AuthAPIKey:
		if keyEnc, err = s.seal("api_key", id, cred.apiKey); err != nil {
			return err
		}
		if account != nil {
			remote, remoteName = account.ID, account.Name
		}
	}
	_, err = s.DB.ExecContext(ctx, `
		UPDATE media_sources SET url = ?, auth_mode = ?, username = ?, password_enc = ?, api_key_enc = ?, token_enc = '',
			remote_user = ?, remote_user_name = ? WHERE id = ?
	`, base, cred.mode, username, pwEnc, keyEnc, remote, remoteName, id)
	return err
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var body sourceBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	base, err := ParseServerURL(trimmed(body.URL))
	if err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_url", err.Error())
		return
	}
	cred := credential{mode: modeOf(body.AuthMode, AuthPassword), username: trimmed(body.Username),
		password: secret(body.Password), apiKey: trimmed(body.APIKey), remoteUser: trimmed(body.RemoteUser)}
	if cred.mode == AuthAPIKey && cred.remoteUser == "" {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "choose the Jellyfin user ViewDock browses as")
		return
	}
	if _, err := s.cipher(); err != nil {
		writeSourceErr(w, err)
		return
	}
	res, err := s.verify(r.Context(), base, cred)
	if err != nil {
		writeSourceErr(w, err)
		return
	}
	name := trimmed(body.Name)
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
	policy := DefaultPolicy()
	if body.Policy != nil {
		policy = body.Policy.normalized()
	}
	rawViews, _ := json.Marshal(views)
	rawPolicy, _ := json.Marshal(policy)
	id, libID := uuid.NewString(), uuid.NewString()
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
		INSERT INTO media_sources(id, library_id, name, url, username, views, policy, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, '', ?, ?, ?, ?, ?)
	`, id, libID, name, base, string(rawViews), string(rawPolicy), boolInt(enabled), now, now); err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "db", "could not save the media source")
		return
	}
	if err := tx.Commit(); err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "db", "could not save the media source")
		return
	}
	if err := s.storeCredential(r.Context(), id, base, cred, res.Account); err != nil {
		_, _ = s.DB.ExecContext(r.Context(), `DELETE FROM libraries WHERE id = ?`, libID)
		writeSourceErr(w, err)
		return
	}
	s.audit(r, "media_source.create", id)
	s.event(r.Context(), id, "settings", true, "source connected with "+describe(cred.mode, policy))
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

func describe(mode string, p Policy) string {
	on := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	max := "unlimited"
	if p.MaxStreams > 0 {
		max = fmt.Sprint(p.MaxStreams)
	}
	return fmt.Sprintf("%s; posters %s, streaming %s, transcoding %s, activity log %s, max streams %s",
		strings.ReplaceAll(mode, "_", " "), on(p.Images), on(p.Stream), on(p.Transcode), on(p.ActivityLog), max)
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
	base, cred, err := s.mergeCredential(r.Context(), src, body)
	if err != nil {
		writeSourceErr(w, err)
		return
	}
	credsChanged := base != src.URL || cred.mode != src.AuthMode || body.Password != nil && *body.Password != "" ||
		trimmed(body.APIKey) != "" || cred.username != src.Username && cred.mode == AuthPassword ||
		cred.mode == AuthAPIKey && !sameID(cred.remoteUser, src.RemoteUser)
	if credsChanged {
		res, err := s.verify(r.Context(), base, cred)
		if err != nil {
			writeSourceErr(w, err)
			return
		}
		if err := s.storeCredential(r.Context(), id, base, cred, res.Account); err != nil {
			writeSourceErr(w, err)
			return
		}
		s.revokeSource(id)
		s.event(r.Context(), id, "settings", true, "credentials changed ("+strings.ReplaceAll(cred.mode, "_", " ")+")")
	}
	if n := trimmed(body.Name); n != "" {
		src.Name = n
	}
	if body.Views != nil {
		src.Views = *body.Views
	}
	if body.Enabled != nil {
		src.Enabled = *body.Enabled
	}
	policyChanged := false
	if body.Policy != nil {
		next := body.Policy.normalized()
		policyChanged = next != src.Policy
		if policyChanged {
			src.Policy = next
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rawViews, _ := json.Marshal(src.Views)
	rawPolicy, _ := json.Marshal(src.Policy)
	if _, err := s.DB.ExecContext(r.Context(), `
		UPDATE media_sources SET name = ?, views = ?, policy = ?, enabled = ?, updated_at = ? WHERE id = ?
	`, src.Name, string(rawViews), string(rawPolicy), boolInt(src.Enabled), now, id); err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "db", "could not save the media source")
		return
	}
	_, _ = s.DB.ExecContext(r.Context(), `UPDATE libraries SET name = ?, updated_at = ? WHERE id = ?`, src.Name, now, src.LibraryID)
	if policyChanged {
		// Active streams keep the policy they started with, so end them.
		s.revokeSource(id)
		s.event(r.Context(), id, "settings", true, "usage restrictions: "+describe(cred.mode, src.Policy))
	}
	if body.Enabled != nil && !src.Enabled {
		s.revokeSource(id)
	}
	s.audit(r, "media_source.update", id)
	if src.Enabled && (credsChanged || body.Views != nil || body.Enabled != nil || policyChanged) {
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
	s.revokeSource(id)
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

func (s *Service) handleEvents(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.get(r.Context(), id); err != nil {
		writeSourceErr(w, err)
		return
	}
	list, err := s.events(r.Context(), id, 200)
	if err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "db", "could not read the usage log")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// handleActivity returns the last week of Jellyfin activity for the account
// ViewDock uses, when the source policy allows reading the activity log.
func (s *Service) handleActivity(w http.ResponseWriter, r *http.Request) {
	src, err := s.get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeSourceErr(w, err)
		return
	}
	if !src.Policy.ActivityLog {
		httpapi.WriteErr(w, http.StatusConflict, "activity_disabled", "reading the Jellyfin activity log is not allowed for this source")
		return
	}
	var entries []ActivityEntry
	err = s.withClient(r.Context(), src, func(c *client, userID string) error {
		entries, err = c.activity(r.Context(), userID, time.Now().Add(-7*24*time.Hour))
		return err
	})
	s.event(r.Context(), src.ID, "activity_read", err == nil, "read the Jellyfin activity log")
	if errors.Is(err, errUnauthorized) {
		httpapi.WriteErr(w, http.StatusBadGateway, "activity_denied", "Jellyfin only shares its activity log with administrator accounts and API keys")
		return
	}
	if err != nil {
		writeSourceErr(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, entries)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
