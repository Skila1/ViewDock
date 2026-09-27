package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/httpapi"
)

const CSRFCookie = "vd_csrf"

func SafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

func IssueCSRF(w http.ResponseWriter, r *http.Request, cfg config.Config) (string, error) {
	tok, err := RandomToken(32)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name: CSRFCookie, Value: tok, Path: "/", HttpOnly: false,
		Secure: httpapi.CookieSecure(r, cfg), SameSite: http.SameSiteLaxMode,
	})
	return tok, nil
}

func CheckCSRF(r *http.Request) bool {
	if SafeMethod(r.Method) {
		return true
	}
	c, err := r.Cookie(CSRFCookie)
	if err != nil || c.Value == "" {
		return false
	}
	hdr := r.Header.Get("X-CSRF-Token")
	if hdr == "" {
		hdr = r.FormValue("csrf")
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(hdr)) == 1
}

func CSRFHeaderOK(r *http.Request) bool {
	return CheckCSRF(r)
}

func WantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json") ||
		strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
}
