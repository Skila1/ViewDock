package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/httpapi"
)

const APIKeyPrefix = "vd_"

type APIKey struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	CreatedAt  string   `json:"created_at"`
	LastUsedAt string   `json:"last_used_at,omitempty"`
	Secret     string   `json:"secret,omitempty"`
	Note       string   `json:"note,omitempty"`
}

var apiKeyScopes = []struct {
	Name string `json:"name"`
	Desc string `json:"description"`
}{
	{Name: "admin", Desc: "Full administration, including keys and settings"},
	{Name: "logs.read", Desc: "Read operational logs"},
	{Name: "streams.inspect", Desc: "Inspect live playback sessions"},
}

func knownAPIKeyScope(name string) bool {
	for _, s := range apiKeyScopes {
		if s.Name == name {
			return true
		}
	}
	return false
}

func normalizeAPIKeyScopes(in []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, raw := range in {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if !knownAPIKeyScope(s) {
			return nil, errors.New("unknown scope " + s)
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, errors.New("at least one scope is required")
	}
	return out, nil
}

func (s *Service) mountAPIKeys(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(RequireAdmin)
		r.Get("/admin/api-keys", s.handleListAPIKeys)
		r.Get("/admin/api-keys/scopes", s.handleAPIKeyScopes)
		r.Post("/admin/api-keys", s.handleCreateAPIKey)
		r.Delete("/admin/api-keys/{id}", s.handleRevokeAPIKey)
	})
}

func (s *Service) handleAPIKeyScopes(w http.ResponseWriter, _ *http.Request) {
	httpapi.WriteJSON(w, 200, apiKeyScopes)
}

func (s *Service) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.QueryContext(r.Context(), `
		SELECT id, name, prefix, scopes, created_at, COALESCE(last_used_at,'')
		FROM api_keys WHERE revoked_at IS NULL OR revoked_at = ''
		ORDER BY created_at DESC
	`)
	if err != nil {
		httpapi.WriteErr(w, 500, "api_keys", err.Error())
		return
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		var k APIKey
		var raw string
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &raw, &k.CreatedAt, &k.LastUsedAt); err != nil {
			httpapi.WriteErr(w, 500, "api_keys", err.Error())
			return
		}
		_ = json.Unmarshal([]byte(raw), &k.Scopes)
		out = append(out, k)
	}
	if out == nil {
		out = []APIKey{}
	}
	httpapi.WriteJSON(w, 200, out)
}

func (s *Service) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	p := FromRequest(r)
	var body struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteErr(w, 400, "bad_request", "invalid json")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		httpapi.WriteErr(w, 400, "bad_request", "name required")
		return
	}
	scopes, err := normalizeAPIKeyScopes(body.Scopes)
	if err != nil {
		httpapi.WriteErr(w, 400, "bad_request", err.Error())
		return
	}
	secret, err := RandomToken(24)
	if err != nil {
		httpapi.WriteErr(w, 500, "api_keys", err.Error())
		return
	}
	plain := APIKeyPrefix + secret
	now := time.Now().UTC().Format(time.RFC3339)
	id := uuid.NewString()
	scopeJSON, _ := json.Marshal(scopes)
	prefix := plain
	if len(prefix) > 10 {
		prefix = prefix[:10]
	}
	_, err = s.DB.ExecContext(r.Context(), `
		INSERT INTO api_keys(id, name, prefix, token_hash, scopes, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, id, name, prefix, HashToken(plain), string(scopeJSON), p.UserID, now)
	if err != nil {
		httpapi.WriteErr(w, 500, "api_keys", err.Error())
		return
	}
	if s.Audit != nil {
		s.Audit.Event(r.Context(), p.UserID, "api_key.create", name, httpapi.ClientIPString(r, s.Cfg), strings.Join(scopes, ","))
	}
	httpapi.WriteJSON(w, 201, APIKey{
		ID: id, Name: name, Prefix: prefix, Scopes: scopes, CreatedAt: now,
		Secret: plain, Note: "shown once",
	})
}

func (s *Service) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.ExecContext(r.Context(), `
		UPDATE api_keys SET revoked_at = ? WHERE id = ? AND (revoked_at IS NULL OR revoked_at = '')
	`, now, id)
	if err != nil {
		httpapi.WriteErr(w, 500, "api_keys", err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpapi.WriteErr(w, 404, "not_found", "not found")
		return
	}
	if p := FromRequest(r); p != nil && s.Audit != nil {
		s.Audit.Event(r.Context(), p.UserID, "api_key.revoke", id, httpapi.ClientIPString(r, s.Cfg), "")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) lookupAPIKey(ctx context.Context, raw string) *Principal {
	if !strings.HasPrefix(raw, APIKeyPrefix) {
		return nil
	}
	var id, name, scopeJSON, createdBy, revoked string
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, name, scopes, created_by, COALESCE(revoked_at,'')
		FROM api_keys WHERE token_hash = ?
	`, HashToken(raw)).Scan(&id, &name, &scopeJSON, &createdBy, &revoked)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return nil
	}
	if revoked != "" {
		return nil
	}
	var scopes []string
	_ = json.Unmarshal([]byte(scopeJSON), &scopes)
	p := &Principal{
		Kind: KindUser, UserID: createdBy, SessionID: "apikey:" + id,
		DisplayName: "API key: " + name, Username: "apikey",
		APIKey: true, Permissions: scopesToPerms(scopes),
	}
	for _, sc := range scopes {
		if sc == "admin" {
			p.IsAdmin = true
			p.Permissions = append(p.Permissions, PermAdmin)
		}
	}
	go s.touchAPIKey(id)
	return p
}

func (s *Service) touchAPIKey(id string) {
	_, _ = s.DB.Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), id)
}

func scopesToPerms(scopes []string) []string {
	var out []string
	for _, sc := range scopes {
		switch sc {
		case "logs.read":
			out = append(out, PermLogsRead)
		case "streams.inspect":
			out = append(out, PermStreamsInspect)
		case "admin":
			out = append(out, PermAdmin)
		}
	}
	return out
}
