package auth

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/viewdock/viewdock/internal/httpapi"
)

func cookieOrBearer(r *http.Request, name string) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

func GuestAllowlisted(path string) bool {
	return strings.HasPrefix(path, "/api/v1/share/") ||
		strings.HasPrefix(path, "/api/v1/playback/") ||
		strings.HasPrefix(path, "/api/v1/watch-together/") ||
		strings.HasPrefix(path, "/hls/")
}

func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tok := cookieOrBearer(r, SessionCookie); tok != "" {
			if strings.HasPrefix(tok, APIKeyPrefix) {
				if p := s.lookupAPIKey(r.Context(), tok); p != nil {
					next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
					return
				}
			}
			row, err := s.Sessions.Lookup(r.Context(), tok)
			if err == nil {
				u, err := s.GetUser(r.Context(), row.UserID)
				if err == nil && !u.Disabled {
					if time.Since(row.LastSeen) > 45*time.Second {
						s.Sessions.Touch(r.Context(), row.ID)
					}
					p := &Principal{
						Kind: KindUser, UserID: u.ID, SessionID: row.ID, IsAdmin: u.IsAdmin,
						DisplayName: u.DisplayName, Username: u.Username,
						Permissions: u.Permissions, Roles: u.Roles,
					}
					if u.PINHash != "" && time.Since(row.LastSeen) > PINIdle {
						p.PINLocked = true
					}
					next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
					return
				}
			}
		}
		if tok := cookieOrBearer(r, GuestCookie); tok != "" {
			if p := s.lookupGuest(r, tok); p != nil {
				next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) lookupGuest(r *http.Request, raw string) *Principal {
	var id, shareID, kind, itemID, exp string
	var allowDL int
	err := s.DB.QueryRowContext(r.Context(), `
		SELECT gs.id, gs.share_id, s.item_kind, s.item_id, gs.expires_at, s.allow_download, s.revoked_at, s.expires_at
		FROM guest_sessions gs
		JOIN shares s ON s.id = gs.share_id
		WHERE gs.token_hash = ?
	`, HashToken(raw)).Scan(&id, &shareID, &kind, &itemID, &exp, &allowDL, new(sql.NullString), new(sql.NullString))
	if err != nil {
		return nil
	}
	if t, _ := time.Parse(time.RFC3339, exp); time.Now().UTC().After(t) {
		return nil
	}
	var revoked, shareExp sql.NullString
	_ = s.DB.QueryRowContext(r.Context(), `SELECT revoked_at, expires_at FROM shares WHERE id = ?`, shareID).Scan(&revoked, &shareExp)
	if revoked.Valid && revoked.String != "" {
		return nil
	}
	if shareExp.Valid && shareExp.String != "" {
		if t, err := time.Parse(time.RFC3339, shareExp.String); err == nil && time.Now().UTC().After(t) {
			return nil
		}
	}
	return &Principal{
		Kind: KindGuestShare, GuestSessionID: id, ShareID: shareID,
		MediaKind: kind, MediaID: itemID, DisplayName: "Guest",
		CanDownload: allowDL == 1,
	}
}

func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := FromRequest(r)
		if p == nil || !p.IsUser() {
			httpapi.WriteErr(w, http.StatusUnauthorized, "unauthorized", "login required")
			return
		}
		if p.PINLocked && !strings.HasPrefix(r.URL.Path, " /api/v1/me/pin") && !strings.HasPrefix(r.URL.Path, "/api/v1/me/pin") {
			httpapi.WriteErr(w, http.StatusLocked, "pin_locked", "pin required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func RequireAdmin(next http.Handler) http.Handler {
	return RequirePerm(PermAdmin)(next)
}

func RequirePerm(name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := FromRequest(r)
			if p == nil || !p.IsUser() {
				httpapi.WriteErr(w, http.StatusUnauthorized, "unauthorized", "login required")
				return
			}
			if p.PINLocked {
				httpapi.WriteErr(w, http.StatusLocked, "pin_locked", "pin required")
				return
			}
			if !p.HasPerm(name) {
				httpapi.WriteErr(w, http.StatusForbidden, "forbidden", "permission required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func RequireUserOrGuest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := FromRequest(r)
		if p == nil {
			httpapi.WriteErr(w, http.StatusUnauthorized, "unauthorized", "login required")
			return
		}
		if p.IsGuest() && !GuestAllowlisted(r.URL.Path) {
			httpapi.WriteErr(w, http.StatusForbidden, "forbidden", "guest not allowed")
			return
		}
		if p.IsUser() && p.PINLocked {
			httpapi.WriteErr(w, http.StatusLocked, "pin_locked", "pin required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) SetupGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/api/v1/setup") || path == "/api/v1/system" ||
			path == "/healthz" || path == "/readyz" ||
			path == "/api/v1/auth/csrf" || path == "/api/v1/auth/login" ||
			path == "/api/v1/client-logs" ||
			strings.HasPrefix(path, "/api/v1/auth/discord") ||
			path == "/api/v1/invites/accept" {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && !s.SetupComplete(r.Context()) {
			httpapi.WriteErr(w, http.StatusServiceUnavailable, "setup_required", "complete first-run setup")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if SafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if p := FromRequest(r); p != nil && p.APIKey {
			next.ServeHTTP(w, r)
			return
		}
		if !CheckCSRF(r) {
			httpapi.WriteErr(w, http.StatusForbidden, "csrf", "csrf required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func SetSessionCookie(w http.ResponseWriter, r *http.Request, cfg interface { /* placeholder */
}, raw string, exp time.Time, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: raw, Path: "/", HttpOnly: true,
		Secure: secure, SameSite: http.SameSiteLaxMode, Expires: exp,
	})
}
