package users

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
)

const maxGuestHours = 24 * 30

// SetMaxGuestHours lowers the longest guest lifetime; 0 restores the default.
func (a *API) SetMaxGuestHours(h int) { a.maxGuestHours.Store(int64(h)) }

func (a *API) guestHourLimit() int {
	if v := a.maxGuestHours.Load(); v > 0 && v <= maxGuestHours {
		return int(v)
	}
	return maxGuestHours
}

// Temporary guest accounts expire, may be limited to a number of concurrent
// sessions, and are scoped either to specific libraries (administrators only)
// or to watch parties they are invited into (administrators and household owners).
func (a *API) guestRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireUser)
		r.Use(auth.RateLimit(a.Auth.Cfg, 30, time.Minute))
		r.Get("/guests", a.listGuests)
		r.Post("/guests", a.createGuest)
		r.Delete("/guests/{id}", a.revokeGuest)
	})
}

func (a *API) canIssueGuests(r *http.Request) (admin bool, owner bool) {
	p := auth.FromRequest(r)
	if p == nil || p.Temporary {
		return false, false
	}
	if p.IsAdmin || p.HasPerm(auth.PermUsersManage) {
		return true, true
	}
	_, owner = a.ownerHousehold(r)
	return false, owner
}

func (a *API) createGuest(w http.ResponseWriter, r *http.Request) {
	admin, owner := a.canIssueGuests(r)
	if !admin && !owner {
		httpapi.WriteErr(w, http.StatusForbidden, "guests", "administrator or household owner required")
		return
	}
	var body struct {
		Username    string   `json:"username"`
		DisplayName string   `json:"display_name"`
		Hours       int      `json:"expires_in_hours"`
		MaxSessions int      `json:"max_sessions"`
		PartyOnly   bool     `json:"party_only"`
		LibraryIDs  []string `json:"library_ids"`
	}
	if err := httpapi.ReadJSON(r, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	if limit := a.guestHourLimit(); body.Hours <= 0 || body.Hours > limit {
		httpapi.WriteErr(w, http.StatusBadRequest, "guests", fmt.Sprintf("expires_in_hours must be between 1 and %d", limit))
		return
	}
	if body.MaxSessions < 0 || body.MaxSessions > 10 {
		httpapi.WriteErr(w, http.StatusBadRequest, "guests", "max_sessions must be between 0 and 10")
		return
	}
	if !admin {
		// Household owners cannot grant library access they do not administer.
		body.PartyOnly = true
		body.LibraryIDs = nil
	}
	if body.MaxSessions == 0 {
		body.MaxSessions = 2
	}
	username := strings.TrimSpace(body.Username)
	if username == "" {
		suffix, _ := auth.RandomToken(4)
		username = "guest-" + strings.ToLower(suffix)
	}
	display := strings.TrimSpace(body.DisplayName)
	if display == "" {
		display = username
	}
	password, err := auth.RandomToken(12)
	if err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "guests", "could not generate credentials")
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "guests", "could not generate credentials")
		return
	}
	p := auth.FromRequest(r)
	id := uuid.NewString()
	now := time.Now().UTC()
	expires := now.Add(time.Duration(body.Hours) * time.Hour)
	partyOnly := 0
	if body.PartyOnly {
		partyOnly = 1
	}
	if _, err := a.queryExec(r, `
		INSERT INTO users(id, username, password_hash, display_name, email, is_admin, disabled, pin_hash, created_at, updated_at, has_password,
			is_temporary, expires_at, max_sessions, party_only, created_by)
		VALUES (?, ?, ?, ?, '', 0, 0, '', ?, ?, 1, 1, ?, ?, ?, ?)
	`, id, username, hash, display, now.Format(time.RFC3339), now.Format(time.RFC3339), expires.Format(time.RFC3339), body.MaxSessions, partyOnly, p.UserID); err != nil {
		httpapi.WriteErr(w, http.StatusConflict, "guests", "username is already taken")
		return
	}
	// Guests receive no role: the default user role grants every library plus
	// upload and sharing permissions.
	for _, lib := range body.LibraryIDs {
		if strings.TrimSpace(lib) != "" {
			if err := a.Auth.Grants.Set(r.Context(), id, lib, false); err != nil {
				httpapi.WriteErr(w, http.StatusBadRequest, "guests", "unknown library")
				return
			}
		}
	}
	a.Auth.Audit.Event(r.Context(), p.UserID, "guest.create", username, httpapi.ClientIPString(r, a.Auth.Cfg),
		"expires="+expires.Format(time.RFC3339))
	httpapi.WriteJSON(w, http.StatusCreated, map[string]any{
		"id": id, "username": username, "display_name": display, "password": password,
		"expires_at": expires.Format(time.RFC3339), "max_sessions": body.MaxSessions,
		"party_only": body.PartyOnly, "library_ids": body.LibraryIDs,
	})
}

func (a *API) listGuests(w http.ResponseWriter, r *http.Request) {
	admin, owner := a.canIssueGuests(r)
	if !admin && !owner {
		httpapi.WriteErr(w, http.StatusForbidden, "guests", "administrator or household owner required")
		return
	}
	q := `SELECT id, username, display_name, expires_at, max_sessions, party_only, created_by, disabled FROM users WHERE is_temporary = 1`
	args := []any{}
	if !admin {
		q += ` AND created_by = ?`
		args = append(args, auth.FromRequest(r).UserID)
	}
	rows, err := a.queryRows(r, q+` ORDER BY expires_at`, args...)
	if err != nil {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "guests", "guest list unavailable")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, username, display, expires, by string
		var maxSessions, party, disabled int
		if rows.Scan(&id, &username, &display, &expires, &maxSessions, &party, &by, &disabled) != nil {
			continue
		}
		when, _ := time.Parse(time.RFC3339, expires)
		out = append(out, map[string]any{
			"id": id, "username": username, "display_name": display, "expires_at": expires,
			"expired": !when.IsZero() && time.Now().After(when), "max_sessions": maxSessions,
			"party_only": party == 1, "created_by": by, "disabled": disabled == 1,
		})
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (a *API) revokeGuest(w http.ResponseWriter, r *http.Request) {
	admin, owner := a.canIssueGuests(r)
	if !admin && !owner {
		httpapi.WriteErr(w, http.StatusForbidden, "guests", "administrator or household owner required")
		return
	}
	id := chi.URLParam(r, "id")
	q := `SELECT created_by FROM users WHERE id = ? AND is_temporary = 1`
	var by string
	if err := a.row(r, q, id).Scan(&by); err != nil || (!admin && by != auth.FromRequest(r).UserID) {
		httpapi.WriteErr(w, http.StatusNotFound, "guests", "guest not found")
		return
	}
	a.Auth.Sessions.DeleteAllForUser(r.Context(), id)
	if _, err := a.queryExec(r, `DELETE FROM users WHERE id = ? AND is_temporary = 1`, id); err != nil {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "guests", "could not revoke guest")
		return
	}
	a.Auth.Audit.Event(r.Context(), auth.FromRequest(r).UserID, "guest.revoke", id, httpapi.ClientIPString(r, a.Auth.Cfg), "")
	httpapi.WriteOK(w)
}

// SweepExpiredGuests disables expired temporary accounts and ends their sessions.
func (a *API) SweepExpiredGuests(ctx context.Context) int {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := a.DB.QueryContext(ctx, a.query(`SELECT id FROM users WHERE is_temporary = 1 AND disabled = 0 AND expires_at <> '' AND expires_at < ?`), now)
	if err != nil {
		return 0
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	for _, id := range ids {
		_, _ = a.DB.ExecContext(ctx, a.query(`UPDATE users SET disabled = 1, updated_at = ? WHERE id = ?`), now, id)
		a.Auth.Sessions.DeleteAllForUser(ctx, id)
	}
	if len(ids) > 0 {
		a.Auth.InvalidatePrincipals()
	}
	return len(ids)
}
