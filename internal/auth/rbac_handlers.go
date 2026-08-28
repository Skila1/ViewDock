package auth

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/httpapi"
)

func (s *Service) handleListPermissions(w http.ResponseWriter, r *http.Request) {
	list, err := s.ListPermissions(r.Context())
	if err != nil {
		httpapi.WriteErr(w, 500, "roles", err.Error())
		return
	}
	httpapi.WriteJSON(w, 200, list)
}

func (s *Service) handleListRoles(w http.ResponseWriter, r *http.Request) {
	list, err := s.ListRoles(r.Context())
	if err != nil {
		httpapi.WriteErr(w, 500, "roles", err.Error())
		return
	}
	httpapi.WriteJSON(w, 200, list)
}

func (s *Service) handleGetRole(w http.ResponseWriter, r *http.Request) {
	role, err := s.GetRole(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpapi.WriteErr(w, 404, "roles", "not found")
		return
	}
	members, _ := s.RoleMembers(r.Context(), role.ID)
	httpapi.WriteJSON(w, 200, map[string]any{"role": role, "members": members})
}

func (s *Service) handleCreateRole(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Permissions []string `json:"permissions"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Permissions == nil {
		body.Permissions = []string{PermMediaUpload, PermSharesCreate}
	}
	role, err := s.CreateRole(r.Context(), body.Name, body.Description, body.Permissions)
	if err != nil {
		httpapi.WriteErr(w, 400, "roles", err.Error())
		return
	}
	httpapi.WriteJSON(w, 201, role)
}

func (s *Service) handlePatchRole(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Description *string  `json:"description"`
		Permissions []string `json:"permissions"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	role, err := s.GetRole(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpapi.WriteErr(w, 404, "roles", "not found")
		return
	}
	desc := role.Description
	if body.Description != nil {
		desc = *body.Description
	}
	role, err = s.UpdateRole(r.Context(), role.ID, desc, body.Permissions)
	if err != nil {
		httpapi.WriteErr(w, 400, "roles", err.Error())
		return
	}
	httpapi.WriteJSON(w, 200, role)
}

func (s *Service) handleDeleteRole(w http.ResponseWriter, r *http.Request) {
	if err := s.DeleteRole(r.Context(), chi.URLParam(r, "id")); err != nil {
		httpapi.WriteErr(w, 400, "roles", err.Error())
		return
	}
	httpapi.WriteOK(w)
}

func (s *Service) handleAddMembers(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserIDs []string `json:"user_ids"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := s.AddRoleMembers(r.Context(), chi.URLParam(r, "id"), body.UserIDs); err != nil {
		httpapi.WriteErr(w, 400, "roles", err.Error())
		return
	}
	httpapi.WriteOK(w)
}

func (s *Service) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	if err := s.RemoveRoleMember(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "userID")); err != nil {
		httpapi.WriteErr(w, 400, "roles", err.Error())
		return
	}
	httpapi.WriteOK(w)
}

func (s *Service) handleListLibGrants(w http.ResponseWriter, r *http.Request) {
	users, roles, err := s.Grants.ListForLibrary(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpapi.WriteErr(w, 500, "grants", err.Error())
		return
	}
	httpapi.WriteJSON(w, 200, map[string]any{"users": users, "roles": roles})
}

func (s *Service) handleSetLibGrant(w http.ResponseWriter, r *http.Request) {
	libID := chi.URLParam(r, "id")
	var body struct {
		UserID      string `json:"user_id"`
		RoleID      string `json:"role_id"`
		CanDownload bool   `json:"can_download"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	switch {
	case strings.TrimSpace(body.UserID) != "":
		if err := s.Grants.Set(r.Context(), body.UserID, libID, body.CanDownload); err != nil {
			httpapi.WriteErr(w, 400, "grants", err.Error())
			return
		}
	case strings.TrimSpace(body.RoleID) != "":
		if err := s.Grants.SetRole(r.Context(), body.RoleID, libID, body.CanDownload); err != nil {
			httpapi.WriteErr(w, 400, "grants", err.Error())
			return
		}
	default:
		httpapi.WriteErr(w, 400, "grants", "user_id or role_id required")
		return
	}
	httpapi.WriteOK(w)
}

func (s *Service) handleDeleteLibGrant(w http.ResponseWriter, r *http.Request) {
	libID := chi.URLParam(r, "id")
	userID := r.URL.Query().Get("user_id")
	roleID := r.URL.Query().Get("role_id")
	switch {
	case userID != "":
		_ = s.Grants.Delete(r.Context(), userID, libID)
	case roleID != "":
		_ = s.Grants.DeleteRole(r.Context(), roleID, libID)
	default:
		httpapi.WriteErr(w, 400, "grants", "user_id or role_id required")
		return
	}
	httpapi.WriteOK(w)
}
