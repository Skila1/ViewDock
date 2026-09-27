package users

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/library"
)

// Administrator household management. Routes are mounted inside the
// users.manage group. Administrators may reorganise existing memberships
// (move, remove, change roles and age limits, transfer ownership) but new
// people still join through invitations they accept. Every action touching
// another account passes the privilege ceiling for that account.
func (a *API) householdAdminRoutes(r chi.Router) {
	r.Get("/admin/households", a.adminListHouseholds)
	r.Post("/admin/households", a.adminCreateHousehold)
	r.Get("/admin/households/{id}", a.adminGetHousehold)
	r.Patch("/admin/households/{id}", a.adminPatchHousehold)
	r.Delete("/admin/households/{id}", a.adminDeleteHousehold)
	r.Post("/admin/households/{id}/invites", a.adminCreateHouseholdInvite)
	r.Patch("/admin/households/{id}/members/{userID}", a.adminPatchHouseholdMember)
	r.Post("/admin/households/{id}/members/{userID}/move", a.adminMoveHouseholdMember)
	r.Delete("/admin/households/{id}/members/{userID}", a.adminRemoveHouseholdMember)
}

var errHouseholdNotFound = errors.New("household not found")

type householdRecord struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	OwnerID   string `json:"owner_id"`
	OwnerName string `json:"owner_username"`
	CreatedAt string `json:"created_at"`
	Members   int    `json:"member_count"`
}

type householdMember struct {
	UserID      string  `json:"id"`
	Username    string  `json:"username"`
	DisplayName string  `json:"display_name"`
	Role        string  `json:"role"`
	AgeLimit    int     `json:"age_limit"`
	ExpiresAt   *string `json:"expires_at"`
	JoinedAt    string  `json:"joined_at"`
	Temporary   bool    `json:"temporary"`
}

func (a *API) auditHousehold(r *http.Request, action, target, detail string) {
	a.Auth.Audit.Event(r.Context(), auth.FromRequest(r).UserID, action, target, httpapi.ClientIPString(r, a.Auth.Cfg), detail)
}

func householdUnavailable(w http.ResponseWriter, op string, err error) {
	slog.Error("household admin", "category", "users", "op", op, "err", err)
	httpapi.WriteErr(w, http.StatusServiceUnavailable, "household", "the household could not be updated")
}

func (a *API) loadHousehold(r *http.Request, id string) (householdRecord, error) {
	var h householdRecord
	err := a.row(r, `
		SELECT h.id, h.name, h.owner_id, COALESCE(u.username, ''), h.created_at,
			(SELECT COUNT(*) FROM household_members m WHERE m.household_id = h.id)
		FROM households h LEFT JOIN users u ON u.id = h.owner_id WHERE h.id = ?
	`, id).Scan(&h.ID, &h.Name, &h.OwnerID, &h.OwnerName, &h.CreatedAt, &h.Members)
	if errors.Is(err, sql.ErrNoRows) {
		return h, errHouseholdNotFound
	}
	return h, err
}

func (a *API) householdMembers(r *http.Request, id string) ([]householdMember, error) {
	rows, err := a.queryRows(r, `
		SELECT u.id, u.username, u.display_name, m.role, m.age_limit, m.expires_at, m.joined_at, u.is_temporary
		FROM household_members m JOIN users u ON u.id = m.user_id
		WHERE m.household_id = ? ORDER BY u.username
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []householdMember{}
	for rows.Next() {
		var m householdMember
		var temporary int
		if err := rows.Scan(&m.UserID, &m.Username, &m.DisplayName, &m.Role, &m.AgeLimit, &m.ExpiresAt, &m.JoinedAt, &temporary); err != nil {
			return nil, err
		}
		m.Temporary = temporary == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

func (a *API) memberRole(r *http.Request, householdID, userID string) (string, error) {
	var role string
	err := a.row(r, `SELECT role FROM household_members WHERE household_id = ? AND user_id = ?`, householdID, userID).Scan(&role)
	return role, err
}

// ceiling applies the privilege ceiling for the target account and writes
// the response when it fails.
func (a *API) ceiling(w http.ResponseWriter, r *http.Request, targetUserID string) bool {
	if err := a.Auth.AssertCanModifyUser(r.Context(), auth.FromRequest(r), targetUserID); err != nil {
		httpapi.WriteErr(w, auth.CeilingHTTPStatus(err), "household", err.Error())
		return false
	}
	return true
}

func (a *API) withTx(r *http.Request, fn func(exec func(query string, args ...any) (sql.Result, error)) error) error {
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	exec := func(query string, args ...any) (sql.Result, error) {
		return tx.ExecContext(r.Context(), a.query(query), args...)
	}
	if err := fn(exec); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (a *API) adminListHouseholds(w http.ResponseWriter, r *http.Request) {
	rows, err := a.queryRows(r, `
		SELECT h.id, h.name, h.owner_id, COALESCE(u.username, ''), h.created_at,
			(SELECT COUNT(*) FROM household_members m WHERE m.household_id = h.id)
		FROM households h LEFT JOIN users u ON u.id = h.owner_id
		ORDER BY h.name, h.id
	`)
	if err != nil {
		slog.Error("list households", "category", "users", "err", err)
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "household", "households are unavailable")
		return
	}
	defer rows.Close()
	out := []householdRecord{}
	for rows.Next() {
		var h householdRecord
		if rows.Scan(&h.ID, &h.Name, &h.OwnerID, &h.OwnerName, &h.CreatedAt, &h.Members) == nil {
			out = append(out, h)
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (a *API) writeAdminHousehold(w http.ResponseWriter, r *http.Request, id string, status int) {
	h, err := a.loadHousehold(r, id)
	if errors.Is(err, errHouseholdNotFound) {
		httpapi.WriteErr(w, http.StatusNotFound, "household", "household not found")
		return
	}
	if err != nil {
		slog.Error("load household", "category", "users", "err", err)
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "household", "the household is unavailable")
		return
	}
	members, err := a.householdMembers(r, id)
	if err != nil {
		slog.Error("load household members", "category", "users", "err", err)
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "household", "the household is unavailable")
		return
	}
	httpapi.WriteJSON(w, status, map[string]any{"household": h, "members": members})
}

func (a *API) adminGetHousehold(w http.ResponseWriter, r *http.Request) {
	a.writeAdminHousehold(w, r, chi.URLParam(r, "id"), http.StatusOK)
}

// adminCreateHousehold creates a household owned by an existing account that
// is not yet in a household. Other people still join by invitation.
func (a *API) adminCreateHousehold(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		OwnerID string `json:"owner_id"`
	}
	if err := httpapi.ReadJSON(r, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > 100 || strings.TrimSpace(body.OwnerID) == "" {
		httpapi.WriteErr(w, http.StatusBadRequest, "household", "name (up to 100 characters) and owner_id are required")
		return
	}
	if !a.ceiling(w, r, body.OwnerID) {
		return
	}
	var temporary int
	if err := a.row(r, `SELECT is_temporary FROM users WHERE id = ?`, body.OwnerID).Scan(&temporary); err != nil {
		httpapi.WriteErr(w, http.StatusNotFound, "household", "user not found")
		return
	}
	if temporary == 1 {
		httpapi.WriteErr(w, http.StatusBadRequest, "household", "guest accounts cannot own households")
		return
	}
	var existing string
	if a.row(r, `SELECT household_id FROM household_members WHERE user_id = ?`, body.OwnerID).Scan(&existing) == nil {
		httpapi.WriteErr(w, http.StatusConflict, "household", "user already belongs to a household")
		return
	}
	id := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339)
	err := a.withTx(r, func(exec func(string, ...any) (sql.Result, error)) error {
		if _, err := exec(`INSERT INTO households(id, name, owner_id, created_at) VALUES (?, ?, ?, ?)`, id, name, body.OwnerID, now); err != nil {
			return err
		}
		_, err := exec(`INSERT INTO household_members(household_id, user_id, role, joined_at) VALUES (?, ?, 'owner', ?)`, id, body.OwnerID, now)
		return err
	})
	if err != nil {
		slog.Warn("create household", "category", "users", "err", err)
		httpapi.WriteErr(w, http.StatusConflict, "household", "the household could not be created")
		return
	}
	a.auditHousehold(r, "household.admin.create", id, "owner="+body.OwnerID)
	a.writeAdminHousehold(w, r, id, http.StatusCreated)
}

// adminPatchHousehold renames a household and/or transfers ownership to an
// existing permanent member. The previous owner stays as an adult member.
func (a *API) adminPatchHousehold(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Name    *string `json:"name"`
		OwnerID *string `json:"owner_id"`
	}
	if err := httpapi.ReadJSON(r, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	h, err := a.loadHousehold(r, id)
	if errors.Is(err, errHouseholdNotFound) {
		httpapi.WriteErr(w, http.StatusNotFound, "household", "household not found")
		return
	}
	if err != nil {
		householdUnavailable(w, "load", err)
		return
	}
	var name string
	if body.Name != nil {
		name = strings.TrimSpace(*body.Name)
		if name == "" || len(name) > 100 {
			httpapi.WriteErr(w, http.StatusBadRequest, "household", "name must be 1 to 100 characters")
			return
		}
	}
	newOwner := ""
	if body.OwnerID != nil && *body.OwnerID != h.OwnerID {
		newOwner = strings.TrimSpace(*body.OwnerID)
		if !a.ceiling(w, r, h.OwnerID) || !a.ceiling(w, r, newOwner) {
			return
		}
		var expires sql.NullString
		var temporary int
		err := a.row(r, `
			SELECT m.expires_at, u.is_temporary FROM household_members m JOIN users u ON u.id = m.user_id
			WHERE m.household_id = ? AND m.user_id = ?
		`, id, newOwner).Scan(&expires, &temporary)
		if err != nil {
			httpapi.WriteErr(w, http.StatusBadRequest, "household", "the new owner must already be a member of this household")
			return
		}
		if temporary == 1 || expires.Valid {
			httpapi.WriteErr(w, http.StatusBadRequest, "household", "guest or temporary members cannot own a household")
			return
		}
	}
	err = a.withTx(r, func(exec func(string, ...any) (sql.Result, error)) error {
		if body.Name != nil {
			if _, err := exec(`UPDATE households SET name = ? WHERE id = ?`, name, id); err != nil {
				return err
			}
		}
		if newOwner == "" {
			return nil
		}
		if _, err := exec(`UPDATE household_members SET role = 'adult' WHERE household_id = ? AND user_id = ?`, id, h.OwnerID); err != nil {
			return err
		}
		if _, err := exec(`UPDATE household_members SET role = 'owner' WHERE household_id = ? AND user_id = ?`, id, newOwner); err != nil {
			return err
		}
		_, err := exec(`UPDATE households SET owner_id = ? WHERE id = ?`, newOwner, id)
		return err
	})
	if err != nil {
		householdUnavailable(w, "patch", err)
		return
	}
	if body.Name != nil {
		a.auditHousehold(r, "household.admin.rename", id, "name="+name)
	}
	if newOwner != "" {
		a.auditHousehold(r, "household.admin.owner", id, "from="+h.OwnerID+" to="+newOwner)
	}
	a.writeAdminHousehold(w, r, id, http.StatusOK)
}

func (a *API) adminDeleteHousehold(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	h, err := a.loadHousehold(r, id)
	if errors.Is(err, errHouseholdNotFound) {
		httpapi.WriteErr(w, http.StatusNotFound, "household", "household not found")
		return
	}
	if err != nil {
		householdUnavailable(w, "load", err)
		return
	}
	if !a.ceiling(w, r, h.OwnerID) {
		return
	}
	err = a.withTx(r, func(exec func(string, ...any) (sql.Result, error)) error {
		if _, err := exec(`DELETE FROM household_invites WHERE household_id = ?`, id); err != nil {
			return err
		}
		if _, err := exec(`DELETE FROM household_members WHERE household_id = ?`, id); err != nil {
			return err
		}
		_, err := exec(`DELETE FROM households WHERE id = ?`, id)
		return err
	})
	if err != nil {
		householdUnavailable(w, "delete", err)
		return
	}
	a.auditHousehold(r, "household.admin.delete", id, "name="+h.Name+" members="+strconv.Itoa(h.Members))
	httpapi.WriteOK(w)
}

func (a *API) adminCreateHouseholdInvite(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := a.loadHousehold(r, id); err != nil {
		if errors.Is(err, errHouseholdNotFound) {
			httpapi.WriteErr(w, http.StatusNotFound, "household", "household not found")
			return
		}
		householdUnavailable(w, "load", err)
		return
	}
	if inviteID, ok := a.issueHouseholdInvite(w, r, id); ok {
		a.auditHousehold(r, "household.admin.invite", id, "invite="+inviteID)
	}
}

func (a *API) adminPatchHouseholdMember(w http.ResponseWriter, r *http.Request) {
	id, userID := chi.URLParam(r, "id"), chi.URLParam(r, "userID")
	var body struct {
		Role     *string `json:"role"`
		AgeLimit *int    `json:"age_limit"`
	}
	if err := httpapi.ReadJSON(r, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	current, err := a.memberRole(r, id, userID)
	if err != nil {
		httpapi.WriteErr(w, http.StatusNotFound, "household", "member not found")
		return
	}
	if !a.ceiling(w, r, userID) {
		return
	}
	role := current
	if body.Role != nil {
		if current == "owner" {
			httpapi.WriteErr(w, http.StatusBadRequest, "household", "transfer ownership to change the owner's role")
			return
		}
		next, ok := validHouseholdRole(*body.Role)
		if !ok {
			httpapi.WriteErr(w, http.StatusBadRequest, "household", "role must be adult, member or child")
			return
		}
		role = next
	}
	if body.AgeLimit != nil && (*body.AgeLimit < 0 || *body.AgeLimit > library.MaxRatingAge) {
		httpapi.WriteErr(w, http.StatusBadRequest, "household", "age_limit must be between 0 and 21")
		return
	}
	q := `UPDATE household_members SET role = ?`
	args := []any{role}
	detail := "role=" + role
	if body.AgeLimit != nil {
		q += `, age_limit = ?`
		args = append(args, *body.AgeLimit)
		detail += " age_limit=" + strconv.Itoa(*body.AgeLimit)
	}
	if _, err := a.queryExec(r, q+` WHERE household_id = ? AND user_id = ?`, append(args, id, userID)...); err != nil {
		householdUnavailable(w, "member", err)
		return
	}
	a.auditHousehold(r, "household.admin.member", userID, "household="+id+" "+detail)
	a.writeAdminHousehold(w, r, id, http.StatusOK)
}

// adminMoveHouseholdMember moves a non-owner member into another household,
// keeping their role and age limit. A user is never in two households.
func (a *API) adminMoveHouseholdMember(w http.ResponseWriter, r *http.Request) {
	id, userID := chi.URLParam(r, "id"), chi.URLParam(r, "userID")
	var body struct {
		HouseholdID string `json:"household_id"`
	}
	if err := httpapi.ReadJSON(r, &body); err != nil || strings.TrimSpace(body.HouseholdID) == "" {
		httpapi.WriteErr(w, http.StatusBadRequest, "household", "household_id is required")
		return
	}
	if body.HouseholdID == id {
		httpapi.WriteErr(w, http.StatusBadRequest, "household", "the member is already in this household")
		return
	}
	current, err := a.memberRole(r, id, userID)
	if err != nil {
		httpapi.WriteErr(w, http.StatusNotFound, "household", "member not found")
		return
	}
	if current == "owner" {
		httpapi.WriteErr(w, http.StatusConflict, "household", "transfer ownership before moving the owner")
		return
	}
	if !a.ceiling(w, r, userID) {
		return
	}
	if _, err := a.loadHousehold(r, body.HouseholdID); err != nil {
		if errors.Is(err, errHouseholdNotFound) {
			httpapi.WriteErr(w, http.StatusNotFound, "household", "target household not found")
			return
		}
		householdUnavailable(w, "load", err)
		return
	}
	var ageLimit int
	var expires sql.NullString
	if err := a.row(r, `SELECT age_limit, expires_at FROM household_members WHERE household_id = ? AND user_id = ?`, id, userID).Scan(&ageLimit, &expires); err != nil {
		httpapi.WriteErr(w, http.StatusNotFound, "household", "member not found")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	err = a.withTx(r, func(exec func(string, ...any) (sql.Result, error)) error {
		res, err := exec(`DELETE FROM household_members WHERE household_id = ? AND user_id = ? AND role <> 'owner'`, id, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return sql.ErrNoRows
		}
		var exp any
		if expires.Valid {
			exp = expires.String
		}
		_, err = exec(`INSERT INTO household_members(household_id, user_id, role, age_limit, expires_at, joined_at) VALUES (?, ?, ?, ?, ?, ?)`,
			body.HouseholdID, userID, current, ageLimit, exp, now)
		return err
	})
	if err != nil {
		householdUnavailable(w, "move", err)
		return
	}
	a.auditHousehold(r, "household.admin.move", userID, "from="+id+" to="+body.HouseholdID)
	a.writeAdminHousehold(w, r, body.HouseholdID, http.StatusOK)
}

func (a *API) adminRemoveHouseholdMember(w http.ResponseWriter, r *http.Request) {
	id, userID := chi.URLParam(r, "id"), chi.URLParam(r, "userID")
	current, err := a.memberRole(r, id, userID)
	if err != nil {
		httpapi.WriteErr(w, http.StatusNotFound, "household", "member not found")
		return
	}
	if current == "owner" {
		httpapi.WriteErr(w, http.StatusConflict, "household", "transfer ownership or delete the household to remove the owner")
		return
	}
	if !a.ceiling(w, r, userID) {
		return
	}
	if _, err := a.queryExec(r, `DELETE FROM household_members WHERE household_id = ? AND user_id = ? AND role <> 'owner'`, id, userID); err != nil {
		householdUnavailable(w, "remove", err)
		return
	}
	a.auditHousehold(r, "household.admin.remove", userID, "household="+id)
	a.writeAdminHousehold(w, r, id, http.StatusOK)
}

// contentRestriction reports the caller's effective content restriction.
func (a *API) contentRestriction(w http.ResponseWriter, r *http.Request) {
	rest, err := library.RestrictionFor(r.Context(), a.DB, auth.FromRequest(r).UserID)
	if err != nil {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "household", "the content restriction is unavailable")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, rest)
}
