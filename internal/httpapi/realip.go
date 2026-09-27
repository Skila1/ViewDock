package httpapi

import (
	"net"
	"net/http"
	"strings"

	"github.com/viewdock/viewdock/internal/config"
)

// TrustedRealIP rewrites r.RemoteAddr from True-Client-IP, X-Real-IP or the
// first X-Forwarded-For entry (in that order, as chi's RealIP does), but only
// when the connecting peer is inside cfg.TrustedProxies. Requests from any
// other peer keep their socket address, so a client cannot choose the IP used
// for rate limits and audit records by sending forwarding headers.
func TrustedRealIP(cfg config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if peer := peerIP(r.RemoteAddr); peer != nil && cfg.TrustedContains(peer) {
				if rip := forwardedClientIP(r.Header); rip != "" {
					r.RemoteAddr = rip
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) realIP(next http.Handler) http.Handler {
	return TrustedRealIP(s.Cfg)(next)
}

func peerIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	return net.ParseIP(host)
}

func forwardedClientIP(h http.Header) string {
	var ip string
	if v := h.Get("True-Client-IP"); v != "" {
		ip = v
	} else if v := h.Get("X-Real-IP"); v != "" {
		ip = v
	} else if v := h.Get("X-Forwarded-For"); v != "" {
		ip, _, _ = strings.Cut(v, ",")
	}
	ip = strings.TrimSpace(ip)
	if ip == "" || net.ParseIP(ip) == nil {
		return ""
	}
	return ip
}
