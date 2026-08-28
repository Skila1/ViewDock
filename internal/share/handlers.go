package share

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
)

type API struct {
	Svc *Service
	Cfg interface { /* unused */
	}
	Auth *auth.Service
}

func NewAPI(svc *Service, a *auth.Service) *API { return &API{Svc: svc, Auth: a} }

func (a *API) Routes(r chi.Router) {
	r.Get("/share/{token}", a.meta)
	r.Post("/share/{token}/unlock", a.unlock)
	r.With(auth.RequirePerm(auth.PermSharesManage)).Get("/shares", a.list)
	r.With(auth.RequirePerm(auth.PermSharesCreate)).Post("/shares", a.create)
	r.With(auth.RequirePerm(auth.PermSharesManage)).Delete("/shares/{id}", a.revoke)
}

func (a *API) meta(w http.ResponseWriter, r *http.Request) {
	_, sh, _, _, err := a.Svc.LookupByToken(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		httpapi.WriteErr(w, 404, "not_found", "not found")
		return
	}
	title := ""
	if a.Svc.Catalog != nil {
		title, _ = a.Svc.Catalog.ItemTitle(r.Context(), sh.ItemKind, sh.ItemID)
	}
	httpapi.WriteJSON(w, 200, map[string]any{
		"item_kind": sh.ItemKind, "needs_password": sh.HasPassword,
		"title": title, "allow_download": sh.AllowDownload,
	})
}

func (a *API) unlock(w http.ResponseWriter, r *http.Request) {
	id, sh, passHash, _, err := a.Svc.LookupByToken(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		httpapi.WriteErr(w, 404, "not_found", "not found")
		return
	}
	if sh.HasPassword {
		var body struct {
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !auth.VerifyPassword(passHash, body.Password) {
			httpapi.WriteErr(w, 401, "unauthorized", "invalid password")
			return
		}
	}
	if sh.MaxConcurrent > 0 && a.Svc.activeCount(r.Context(), id) >= sh.MaxConcurrent {
		if c, err := r.Cookie(auth.GuestCookie); err != nil || c.Value == "" {
			httpapi.WriteErr(w, 429, "share_busy", "too many viewers")
			return
		}
	}
	raw, exp, err := a.Svc.MintGuest(r.Context(), id, httpapi.ClientIPString(r, a.Auth.Cfg))
	if err != nil {
		httpapi.WriteErr(w, 500, "share", err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: auth.GuestCookie, Value: raw, Path: "/", HttpOnly: true,
		Secure: httpapi.CookieSecure(r, a.Auth.Cfg), SameSite: http.SameSiteLaxMode, Expires: exp,
	})
	httpapi.WriteJSON(w, 200, map[string]any{"ok": true, "item_kind": sh.ItemKind, "item_id": sh.ItemID})
}

func (a *API) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ItemKind      string `json:"item_kind"`
		ItemID        string `json:"item_id"`
		Password      string `json:"password"`
		Quality       string `json:"quality"`
		MaxConcurrent int    `json:"max_concurrent"`
		AllowDownload bool   `json:"allow_download"`
		Hours         int    `json:"hours"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var ttl time.Duration
	if body.Hours > 0 {
		ttl = time.Duration(body.Hours) * time.Hour
	}
	raw, sh, err := a.Svc.Create(r.Context(), auth.FromRequest(r), body.ItemKind, body.ItemID, body.Password, body.MaxConcurrent, body.Quality, body.AllowDownload, ttl)
	if err != nil {
		httpapi.WriteErr(w, 400, "share", err.Error())
		return
	}
	httpapi.WriteJSON(w, 201, map[string]any{"id": sh.ID, "token": raw})
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	rows, err := a.Svc.DB.QueryContext(r.Context(), `SELECT id, item_kind, item_id, revoked_at, created_at FROM shares ORDER BY created_at DESC`)
	if err != nil {
		httpapi.WriteErr(w, 500, "share", err.Error())
		return
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, kind, item, created string
		var rev sql.NullString
		_ = rows.Scan(&id, &kind, &item, &rev, &created)
		out = append(out, map[string]any{"id": id, "item_kind": kind, "item_id": item, "revoked": rev.Valid && rev.String != "", "created_at": created})
	}
	if out == nil {
		out = []map[string]any{}
	}
	httpapi.WriteJSON(w, 200, out)
}

func (a *API) revoke(w http.ResponseWriter, r *http.Request) {
	if err := a.Svc.Revoke(r.Context(), chi.URLParam(r, "id")); err != nil {
		httpapi.WriteErr(w, 400, "share", err.Error())
		return
	}
	httpapi.WriteOK(w)
}
