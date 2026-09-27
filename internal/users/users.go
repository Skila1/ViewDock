package users

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/library"
)

type API struct {
	DB       *sql.DB
	Auth     *auth.Service
	Postgres bool

	maxGuestHours atomic.Int64
}

func New(db *sql.DB, a *auth.Service) *API { return &API{DB: db, Auth: a} }

func NewWithDialect(sqlDB *sql.DB, a *auth.Service, dialect db.Dialect) *API {
	return &API{DB: sqlDB, Auth: a, Postgres: dialect == db.DialectPostgres}
}

func (a *API) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequirePerm(auth.PermUsersManage))
		r.Get("/users", a.list)
		r.Post("/users", a.create)
		r.Get("/users/{id}", a.get)
		r.Patch("/users/{id}", a.patch)
		r.Delete("/users/{id}", a.remove)
		r.Get("/users/{id}/grants", a.listGrants)
		r.Post("/users/{id}/grants", a.setGrant)
		r.Delete("/users/{id}/grants", a.deleteGrant)
		r.Get("/invites", a.listInvites)
		r.Post("/invites", a.createInvite)
		a.householdAdminRoutes(r)
	})
	r.Post("/invites/accept", a.acceptInvite)
	a.householdRoutes(r)
	a.guestRoutes(r)
}

func (a *API) query(query string) string {
	if !a.Postgres {
		return query
	}
	return db.RewritePlaceholders(query)
}

func (a *API) row(r *http.Request, query string, args ...any) *sql.Row {
	return a.DB.QueryRowContext(r.Context(), a.query(query), args...)
}

func (a *API) queryRows(r *http.Request, query string, args ...any) (*sql.Rows, error) {
	return a.DB.QueryContext(r.Context(), a.query(query), args...)
}

func (a *API) queryExec(r *http.Request, query string, args ...any) (sql.Result, error) {
	return a.DB.ExecContext(r.Context(), a.query(query), args...)
}

func (a *API) userJSON(r *http.Request, id, un, dn string, admin, dis, ageLimit int) map[string]any {
	return map[string]any{
		"id": id, "username": un, "display_name": dn,
		"is_admin": admin == 1, "disabled": dis == 1,
		"content_age_limit": ageLimit,
		"is_superadmin":     a.Auth.IsSuperadmin(r.Context(), id),
		"protected":         a.Auth.IsProtectedUser(r.Context(), id),
		"roles":             a.Auth.RoleNamesFor(r.Context(), id),
		"role_ids":          a.Auth.RoleIDsFor(r.Context(), id),
	}
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.QueryContext(r.Context(), `SELECT id, username, display_name, is_admin, disabled, content_age_limit FROM users ORDER BY username`)
	if err != nil {
		httpapi.WriteErr(w, 500, "users", err.Error())
		return
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, un, dn string
		var admin, dis, ageLimit int
		_ = rows.Scan(&id, &un, &dn, &admin, &dis, &ageLimit)
		out = append(out, a.userJSON(r, id, un, dn, admin, dis, ageLimit))
	}
	if out == nil {
		out = []map[string]any{}
	}
	httpapi.WriteJSON(w, 200, out)
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var un, dn string
	var admin, dis, ageLimit int
	err := a.DB.QueryRowContext(r.Context(), `SELECT username, display_name, is_admin, disabled, content_age_limit FROM users WHERE id = ?`, id).
		Scan(&un, &dn, &admin, &dis, &ageLimit)
	if err != nil {
		httpapi.WriteErr(w, 404, "users", "not found")
		return
	}
	out := a.userJSON(r, id, un, dn, admin, dis, ageLimit)
	if rest, err := library.RestrictionFor(r.Context(), a.DB, id); err == nil {
		out["content_restriction"] = rest
	}
	grants, _ := a.Auth.Grants.ListForUser(r.Context(), id)
	out["grants"] = grants
	out["discord_id"] = a.Auth.DiscordUserID(r.Context(), id)
	httpapi.WriteJSON(w, 200, out)
}

func (a *API) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username    string   `json:"username"`
		Password    string   `json:"password"`
		DisplayName string   `json:"display_name"`
		Admin       bool     `json:"is_admin"`
		RoleIDs     []string `json:"role_ids"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	actor := auth.FromRequest(r)
	if body.Admin && (actor == nil || !actor.CanManageAdministrators()) {
		httpapi.WriteErr(w, 403, "users", auth.ErrAdministratorsManage.Error())
		return
	}
	assign := body.RoleIDs
	if len(assign) == 0 && !body.Admin {
		assign = []string{auth.RoleUser}
	}
	if body.Admin {
		assign = append(assign, auth.RoleAdministrator)
	}
	if err := a.Auth.AssertCanAssignRoles(r.Context(), actor, "", assign); err != nil {
		httpapi.WriteErr(w, auth.CeilingHTTPStatus(err), "users", err.Error())
		return
	}
	u, err := a.Auth.CreateUser(r.Context(), body.Username, body.Password, body.DisplayName, body.Admin)
	if err != nil {
		writeCreateUserErr(w, "users", err)
		return
	}
	if len(body.RoleIDs) > 0 {
		if err := a.Auth.SetUserRolesAs(r.Context(), actor, u.ID, body.RoleIDs); err != nil {
			httpapi.WriteErr(w, auth.CeilingHTTPStatus(err), "users", err.Error())
			return
		}
	}
	httpapi.WriteJSON(w, 201, map[string]any{"id": u.ID, "username": u.Username})
}

func writeCreateUserErr(w http.ResponseWriter, code string, err error) {
	switch {
	case errors.Is(err, auth.ErrUsernameTaken):
		httpapi.WriteErr(w, http.StatusConflict, "username_taken", err.Error())
	case errors.Is(err, auth.ErrInvalidNewUser), errors.Is(err, auth.ErrLocalSignupDisabled):
		httpapi.WriteErr(w, auth.CeilingHTTPStatus(err), code, err.Error())
	default:
		slog.Error("create user failed", "category", "users", "err", err)
		httpapi.WriteErr(w, http.StatusInternalServerError, code, "the account could not be created")
	}
}

func (a *API) patch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p := auth.FromRequest(r)
	var body struct {
		DisplayName *string  `json:"display_name"`
		Disabled    *bool    `json:"disabled"`
		RoleIDs     []string `json:"role_ids"`
		Password    *string  `json:"password"`
		AgeLimit    *int     `json:"content_age_limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteErr(w, 400, "bad_request", "invalid json")
		return
	}
	if body.AgeLimit != nil && (*body.AgeLimit < 0 || *body.AgeLimit > library.MaxRatingAge) {
		httpapi.WriteErr(w, 400, "users", "content_age_limit must be between 0 and 21")
		return
	}
	if err := a.Auth.AssertCanModifyUser(r.Context(), p, id); err != nil {
		httpapi.WriteErr(w, auth.CeilingHTTPStatus(err), "users", err.Error())
		return
	}
	if body.AgeLimit != nil {
		res, err := a.queryExec(r, `UPDATE users SET content_age_limit = ?, updated_at = ? WHERE id = ?`, *body.AgeLimit, time.Now().UTC().Format(time.RFC3339), id)
		if err != nil {
			slog.Error("set content age limit", "category", "users", "err", err)
			httpapi.WriteErr(w, http.StatusServiceUnavailable, "users", "the content restriction could not be saved")
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			httpapi.WriteErr(w, 404, "users", "not found")
			return
		}
		a.Auth.Audit.Event(r.Context(), p.UserID, "user.content_restriction", id, httpapi.ClientIPString(r, a.Auth.Cfg), "max_age="+strconv.Itoa(*body.AgeLimit))
	}
	if body.DisplayName != nil {
		if err := a.Auth.UpdateDisplayName(r.Context(), id, *body.DisplayName); err != nil {
			httpapi.WriteErr(w, 400, "users", err.Error())
			return
		}
	}
	if body.Disabled != nil {
		if err := a.Auth.SetDisabled(r.Context(), p, id, *body.Disabled); err != nil {
			httpapi.WriteErr(w, auth.CeilingHTTPStatus(err), "users", err.Error())
			return
		}
	}
	if body.RoleIDs != nil {
		if err := a.Auth.SetUserRolesAs(r.Context(), p, id, body.RoleIDs); err != nil {
			httpapi.WriteErr(w, auth.CeilingHTTPStatus(err), "users", err.Error())
			return
		}
	}
	if body.Password != nil && *body.Password != "" {
		if err := a.Auth.SetPassword(r.Context(), id, *body.Password); err != nil {
			httpapi.WriteErr(w, 400, "users", err.Error())
			return
		}
	}
	a.get(w, r)
}

func (a *API) setGrant(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		LibraryID string `json:"library_id"`
		Download  bool   `json:"can_download"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := a.Auth.Grants.Set(r.Context(), id, body.LibraryID, body.Download); err != nil {
		httpapi.WriteErr(w, 400, "grants", err.Error())
		return
	}
	httpapi.WriteOK(w)
}

func (a *API) listGrants(w http.ResponseWriter, r *http.Request) {
	list, err := a.Auth.Grants.ListForUser(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpapi.WriteErr(w, 500, "grants", err.Error())
		return
	}
	httpapi.WriteJSON(w, 200, list)
}

func (a *API) deleteGrant(w http.ResponseWriter, r *http.Request) {
	_ = a.Auth.Grants.Delete(r.Context(), chi.URLParam(r, "id"), r.URL.Query().Get("library_id"))
	httpapi.WriteOK(w)
}

func (a *API) remove(w http.ResponseWriter, r *http.Request) {
	p := auth.FromRequest(r)
	if err := a.Auth.DeleteUser(r.Context(), p, chi.URLParam(r, "id")); err != nil {
		httpapi.WriteErr(w, auth.CeilingHTTPStatus(err), "users", err.Error())
		return
	}
	httpapi.WriteOK(w)
}

func (a *API) listInvites(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.QueryContext(r.Context(), `SELECT id, expires_at, used_at, is_admin FROM invites ORDER BY created_at DESC`)
	if err != nil {
		httpapi.WriteErr(w, 500, "invites", err.Error())
		return
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, exp string
		var used *string
		var admin int
		_ = rows.Scan(&id, &exp, &used, &admin)
		out = append(out, map[string]any{"id": id, "expires_at": exp, "used": used != nil, "is_admin": admin == 1})
	}
	if out == nil {
		out = []map[string]any{}
	}
	httpapi.WriteJSON(w, 200, out)
}

func (a *API) createInvite(w http.ResponseWriter, r *http.Request) {
	if !a.Auth.LocalSignupAllowed(r.Context()) {
		httpapi.WriteErr(w, 403, "invite", auth.ErrLocalSignupDisabled.Error())
		return
	}
	p := auth.FromRequest(r)
	raw, err := auth.RandomToken(24)
	if err != nil {
		httpapi.WriteErr(w, 500, "invite", err.Error())
		return
	}
	var body struct {
		Days     int      `json:"days"`
		Admin    bool     `json:"is_admin"`
		Libs     []string `json:"library_ids"`
		Download bool     `json:"can_download"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Days <= 0 {
		body.Days = 7
	}
	now := time.Now().UTC()
	id := uuid.NewString()
	ai := 0
	if body.Admin {
		if p == nil || !p.CanManageAdministrators() {
			httpapi.WriteErr(w, 403, "invite", auth.ErrAdministratorsManage.Error())
			return
		}
		ai = 1
	}
	_, err = a.DB.ExecContext(r.Context(), `
		INSERT INTO invites(id, token_hash, created_by, expires_at, is_admin, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, auth.HashToken(raw), p.UserID, now.Add(time.Duration(body.Days)*24*time.Hour).Format(time.RFC3339), ai, now.Format(time.RFC3339))
	if err != nil {
		httpapi.WriteErr(w, 500, "invite", err.Error())
		return
	}
	for _, lib := range body.Libs {
		d := 0
		if body.Download {
			d = 1
		}
		_, _ = a.DB.ExecContext(r.Context(), `INSERT INTO invite_library_grants(invite_id, library_id, can_download) VALUES (?, ?, ?)`, id, lib, d)
	}
	httpapi.WriteJSON(w, 201, map[string]string{"id": id, "token": raw})
}

func (a *API) acceptInvite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token       string `json:"token"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var id string
	var admin int
	var exp, used sql.NullString
	err := a.DB.QueryRowContext(r.Context(), `
		SELECT id, is_admin, expires_at, used_at FROM invites WHERE token_hash = ?
	`, auth.HashToken(body.Token)).Scan(&id, &admin, &exp, &used)
	if err != nil || (used.Valid && used.String != "") {
		httpapi.WriteErr(w, 404, "invite", "invalid invite")
		return
	}
	if t, e := time.Parse(time.RFC3339, exp.String); e == nil && time.Now().UTC().After(t) {
		httpapi.WriteErr(w, 404, "invite", "invalid invite")
		return
	}
	u, err := a.Auth.CreateUser(r.Context(), body.Username, body.Password, body.DisplayName, admin == 1)
	if err != nil {
		writeCreateUserErr(w, "invite", err)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = a.DB.ExecContext(r.Context(), `UPDATE invites SET used_by = ?, used_at = ? WHERE id = ?`, u.ID, now, id)
	rows, _ := a.DB.QueryContext(r.Context(), `SELECT library_id, can_download FROM invite_library_grants WHERE invite_id = ?`, id)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var lib string
			var dl int
			_ = rows.Scan(&lib, &dl)
			_ = a.Auth.Grants.Set(r.Context(), u.ID, lib, dl == 1)
		}
	}
	httpapi.WriteJSON(w, 201, map[string]string{"id": u.ID, "username": u.Username})
}
