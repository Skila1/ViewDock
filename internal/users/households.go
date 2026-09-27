package users

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
)

func (a *API) householdRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireUser)
		r.Get("/household", a.household)
		r.Post("/household", a.createHousehold)
		r.Post("/household/members", a.addHouseholdMember)
		r.Delete("/household/members/{id}", a.removeHouseholdMember)
		r.Post("/household/invites", a.createHouseholdInvite)
		r.Post("/household/invites/accept", a.acceptHouseholdInvite)
		r.Get("/content-restriction", a.contentRestriction)
	})
}

func (a *API) household(w http.ResponseWriter, r *http.Request) {
	p := auth.FromRequest(r)
	var id, name, owner, created string
	err := a.row(r, `SELECT h.id, h.name, h.owner_id, h.created_at FROM households h JOIN household_members m ON m.household_id = h.id WHERE m.user_id = ? AND (m.expires_at IS NULL OR m.expires_at > ?)`, p.UserID, time.Now().UTC().Format(time.RFC3339)).Scan(&id, &name, &owner, &created)
	if err != nil {
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"household": nil, "members": []any{}})
		return
	}
	rows, err := a.queryRows(r, `SELECT u.id, u.username, u.display_name, m.role, m.age_limit, m.expires_at FROM household_members m JOIN users u ON u.id = m.user_id WHERE m.household_id = ? ORDER BY u.username`, id)
	if err != nil {
		httpapi.WriteErr(w, 500, "household", err.Error())
		return
	}
	defer rows.Close()
	members := []map[string]any{}
	for rows.Next() {
		var uid, username, display, role string
		var age int
		var expires *string
		if rows.Scan(&uid, &username, &display, &role, &age, &expires) == nil {
			members = append(members, map[string]any{"id": uid, "username": username, "display_name": display, "role": role, "age_limit": age, "expires_at": expires})
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"household": map[string]any{"id": id, "name": name, "owner_id": owner, "created_at": created}, "members": members})
}

func (a *API) createHousehold(w http.ResponseWriter, r *http.Request) {
	p := auth.FromRequest(r)
	if p.Temporary {
		httpapi.WriteErr(w, 403, "household", "guest accounts cannot create households")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		httpapi.WriteErr(w, 400, "household", "name is required")
		return
	}
	var existing string
	if err := a.row(r, `SELECT household_id FROM household_members WHERE user_id = ?`, p.UserID).Scan(&existing); err == nil {
		httpapi.WriteErr(w, 409, "household", "user already belongs to a household")
		return
	}
	id := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := a.queryExec(r, `INSERT INTO households(id, name, owner_id, created_at) VALUES (?, ?, ?, ?)`, id, strings.TrimSpace(body.Name), p.UserID, now); err != nil {
		httpapi.WriteErr(w, 500, "household", err.Error())
		return
	}
	if _, err := a.queryExec(r, `INSERT INTO household_members(household_id, user_id, role, joined_at) VALUES (?, ?, 'owner', ?)`, id, p.UserID, now); err != nil {
		httpapi.WriteErr(w, 500, "household", err.Error())
		return
	}
	a.household(w, r)
}

func (a *API) ownerHousehold(r *http.Request) (string, bool) {
	p := auth.FromRequest(r)
	var id string
	err := a.row(r, `SELECT h.id FROM households h JOIN household_members m ON m.household_id = h.id WHERE m.user_id = ? AND h.owner_id = ? AND m.role = 'owner'`, p.UserID, p.UserID).Scan(&id)
	return id, err == nil
}

// validHouseholdRole restricts assignable roles; ownership is never assignable.
func validHouseholdRole(role string) (string, bool) {
	switch role {
	case "":
		return "member", true
	case "adult", "member", "child":
		return role, true
	}
	return "", false
}

// addHouseholdMember updates an existing member's role and age limit. Adding a
// new person requires an invitation they accept, except for administrators.
func (a *API) addHouseholdMember(w http.ResponseWriter, r *http.Request) {
	id, ok := a.ownerHousehold(r)
	if !ok {
		httpapi.WriteErr(w, 403, "household", "household owner required")
		return
	}
	var body struct {
		UserID   string `json:"user_id"`
		Role     string `json:"role"`
		AgeLimit int    `json:"age_limit"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || strings.TrimSpace(body.UserID) == "" {
		httpapi.WriteErr(w, 400, "household", "user_id is required")
		return
	}
	role, valid := validHouseholdRole(body.Role)
	if !valid || body.AgeLimit < 0 || body.AgeLimit > 21 {
		httpapi.WriteErr(w, 400, "household", "role must be adult, member or child and age_limit between 0 and 21")
		return
	}
	if body.UserID == auth.FromRequest(r).UserID {
		httpapi.WriteErr(w, 400, "household", "the owner role cannot be changed")
		return
	}
	var current string
	err := a.row(r, `SELECT household_id FROM household_members WHERE user_id = ?`, body.UserID).Scan(&current)
	switch {
	case err == nil && current != id:
		httpapi.WriteErr(w, 409, "household", "user already belongs to another household")
		return
	case err != nil:
		p := auth.FromRequest(r)
		if !p.IsAdmin && !p.HasPerm(auth.PermUsersManage) {
			httpapi.WriteErr(w, 403, "household", "invite this user; they must accept to join")
			return
		}
		if _, err := a.queryExec(r, `INSERT INTO household_members(household_id, user_id, role, age_limit, joined_at) VALUES (?, ?, ?, ?, ?)`, id, body.UserID, role, body.AgeLimit, time.Now().UTC().Format(time.RFC3339)); err != nil {
			httpapi.WriteErr(w, 400, "household", "user could not be added")
			return
		}
	default:
		if _, err := a.queryExec(r, `UPDATE household_members SET role = ?, age_limit = ? WHERE household_id = ? AND user_id = ? AND role <> 'owner'`, role, body.AgeLimit, id, body.UserID); err != nil {
			httpapi.WriteErr(w, 400, "household", "member could not be updated")
			return
		}
	}
	a.Auth.Audit.Event(r.Context(), auth.FromRequest(r).UserID, "household.member", body.UserID, httpapi.ClientIPString(r, a.Auth.Cfg), "role="+role)
	a.household(w, r)
}

func (a *API) removeHouseholdMember(w http.ResponseWriter, r *http.Request) {
	id, ok := a.ownerHousehold(r)
	if !ok {
		httpapi.WriteErr(w, 403, "household", "household owner required")
		return
	}
	if chi.URLParam(r, "id") == auth.FromRequest(r).UserID {
		httpapi.WriteErr(w, 400, "household", "owner cannot be removed")
		return
	}
	_, _ = a.queryExec(r, `DELETE FROM household_members WHERE household_id = ? AND user_id = ?`, id, chi.URLParam(r, "id"))
	httpapi.WriteOK(w)
}

func (a *API) createHouseholdInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := a.ownerHousehold(r)
	if !ok {
		httpapi.WriteErr(w, 403, "household", "household owner required")
		return
	}
	a.issueHouseholdInvite(w, r, id)
}

// issueHouseholdInvite creates a single-use invitation into household id and
// writes the response. Joining always requires the invitee to accept.
func (a *API) issueHouseholdInvite(w http.ResponseWriter, r *http.Request, id string) (string, bool) {
	var body struct {
		Days     int    `json:"days"`
		Role     string `json:"role"`
		AgeLimit int    `json:"age_limit"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Days <= 0 {
		body.Days = 7
	}
	if body.Days > 30 {
		httpapi.WriteErr(w, 400, "household", "invites expire after at most 30 days")
		return "", false
	}
	role, valid := validHouseholdRole(body.Role)
	if !valid || body.AgeLimit < 0 || body.AgeLimit > 21 {
		httpapi.WriteErr(w, 400, "household", "role must be adult, member or child and age_limit between 0 and 21")
		return "", false
	}
	body.Role = role
	raw, err := auth.RandomToken(24)
	if err != nil {
		httpapi.WriteErr(w, 500, "household", err.Error())
		return "", false
	}
	now := time.Now().UTC()
	inviteID := uuid.NewString()
	_, err = a.queryExec(r, `INSERT INTO household_invites(id, household_id, token_hash, role, age_limit, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, inviteID, id, auth.HashToken(raw), body.Role, body.AgeLimit, now.Add(time.Duration(body.Days)*24*time.Hour).Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		slog.Error("create household invite", "category", "users", "err", err)
		httpapi.WriteErr(w, 500, "household", "the invite could not be created")
		return "", false
	}
	httpapi.WriteJSON(w, 201, map[string]any{"id": inviteID, "token": raw, "expires_at": now.Add(time.Duration(body.Days) * 24 * time.Hour).Format(time.RFC3339)})
	return inviteID, true
}

func (a *API) acceptHouseholdInvite(w http.ResponseWriter, r *http.Request) {
	p := auth.FromRequest(r)
	var body struct {
		Token string `json:"token"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || strings.TrimSpace(body.Token) == "" {
		httpapi.WriteErr(w, 400, "household", "token is required")
		return
	}
	var inviteID, householdID, role, expires string
	var age int
	err := a.row(r, `SELECT id, household_id, role, age_limit, expires_at FROM household_invites WHERE token_hash = ? AND used_at IS NULL`, auth.HashToken(body.Token)).Scan(&inviteID, &householdID, &role, &age, &expires)
	if err != nil {
		httpapi.WriteErr(w, 404, "household", "invalid invite")
		return
	}
	when, _ := time.Parse(time.RFC3339, expires)
	if time.Now().UTC().After(when) {
		httpapi.WriteErr(w, 404, "household", "invite expired")
		return
	}
	var existing string
	if a.row(r, `SELECT household_id FROM household_members WHERE user_id = ?`, p.UserID).Scan(&existing) == nil {
		httpapi.WriteErr(w, 409, "household", "user already belongs to a household")
		return
	}
	if p.Temporary {
		httpapi.WriteErr(w, 403, "household", "guest accounts cannot join households")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	claim, err := a.queryExec(r, `UPDATE household_invites SET used_by = ?, used_at = ? WHERE id = ? AND used_at IS NULL`, p.UserID, now, inviteID)
	if err != nil {
		httpapi.WriteErr(w, 503, "household", "invite could not be accepted")
		return
	}
	if n, _ := claim.RowsAffected(); n != 1 {
		httpapi.WriteErr(w, 404, "household", "invalid invite")
		return
	}
	if _, err := a.queryExec(r, `INSERT INTO household_members(household_id, user_id, role, age_limit, joined_at) VALUES (?, ?, ?, ?, ?)`, householdID, p.UserID, role, age, now); err != nil {
		_, _ = a.queryExec(r, `UPDATE household_invites SET used_by = NULL, used_at = NULL WHERE id = ?`, inviteID)
		httpapi.WriteErr(w, 400, "household", "could not join household")
		return
	}
	a.Auth.Audit.Event(r.Context(), p.UserID, "household.join", householdID, httpapi.ClientIPString(r, a.Auth.Cfg), "role="+role)
	a.household(w, r)
}
