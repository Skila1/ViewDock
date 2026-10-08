package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const (
	frontendDialTimeout  = 5 * time.Second
	frontendProbeTimeout = 2 * time.Second
)

// ParseControlURL validates the upstream control plane address of a frontend
// process. It is deployment configuration, never user input.
func ParseControlURL(raw string) (*url.URL, error) {
	return ParseUpstreamURL("VD_CONTROL_URL", raw)
}

// ParseUpstreamURL validates an internal service address named by the
// environment variable name: http or https, a host and an optional port.
func ParseUpstreamURL(name, raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New(name + " must be an absolute http or https URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New(name + " must be a scheme, host and optional port only")
	}
	u.Path = ""
	return u, nil
}

// CoordinatorRelay forwards public watch party requests (including the room
// WebSocket) from a control plane to a separate party coordinator, which
// authenticates them against the shared database. A coordinator outage
// answers 503 coordinator_unavailable; the player keeps its last known
// state and reconnects.
func (s *Server) CoordinatorRelay(target *url.URL) http.Handler {
	return s.upstreamProxy(target, "coordinator", http.StatusServiceUnavailable, "coordinator_unavailable",
		"the watch party service is unreachable; reconnecting")
}

// FrontendHandler serves an independently hosted web frontend: the embedded
// PWA shell and assets locally, and API, HLS and relayed media traffic
// (including WebSocket upgrades) proxied to the control plane. It needs no
// database, so the shell keeps loading while the control plane or a media
// worker is down; API calls then fail with 502 and clients fall back to
// their cached data.
func (s *Server) FrontendHandler(control *url.URL) http.Handler {
	proxy := s.controlProxy(control)
	probe := &http.Client{Timeout: frontendProbeTimeout, Transport: proxy.Transport}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(s.accessLog)
	r.Use(middleware.Recoverer)
	r.Use(secureHeaders)
	r.Use(noStoreAPI)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "role": "frontend"})
	})
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "role": "frontend", "control": probeControl(r.Context(), probe, control)})
	})
	r.Handle("/api/*", proxy)
	r.Handle("/hls/*", proxy)
	r.Handle("/mesh/*", proxy)
	r.NotFound(s.spa().ServeHTTP)
	return r
}

func probeControl(ctx context.Context, c *http.Client, control *url.URL) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, control.String()+"/healthz", nil)
	if err != nil {
		return "unavailable"
	}
	resp, err := c.Do(req)
	if err != nil {
		return "unavailable"
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "unavailable"
	}
	return "ok"
}

func (s *Server) controlProxy(control *url.URL) *httputil.ReverseProxy {
	return s.upstreamProxy(control, "frontend", http.StatusBadGateway, "control_unavailable",
		"the ViewDock server is unreachable; showing saved data where available")
}

func (s *Server) upstreamProxy(target *url.URL, category string, status int, code, message string) *httputil.ReverseProxy {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: frontendDialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   frontendDialTimeout,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
		ExpectContinueTimeout: time.Second,
	}
	return &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			// The upstream checks origins and builds links against the
			// public host the browser used.
			pr.Out.Host = pr.In.Host
			proto := ""
			if peer := peerIP(pr.In.RemoteAddr); peer != nil && s.Cfg.TrustedContains(peer) {
				// A trusted proxy in front of this process already recorded
				// the client; keep its chain and protocol.
				if v := pr.In.Header.Get("X-Forwarded-For"); v != "" {
					pr.Out.Header.Set("X-Forwarded-For", v)
				}
				proto = pr.In.Header.Get("X-Forwarded-Proto")
			}
			pr.SetXForwarded()
			if proto != "" {
				pr.Out.Header.Set("X-Forwarded-Proto", proto)
			}
			pr.Out.Header.Del("Forwarded")
			pr.Out.Header.Del("X-Real-Ip")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(r.Context().Err(), context.Canceled) {
				return
			}
			if s.Log != nil {
				s.Log.Warn("upstream unreachable", "category", category, "path", r.URL.Path, "err", err.Error())
			}
			w.Header().Set("Retry-After", "5")
			WriteErr(w, status, code, message)
		},
	}
}
