// Package mesh connects the control plane to media workers: it places new
// playback sessions on workers and relays session traffic to them.
package mesh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/backend"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/nodeauth"
	"github.com/viewdock/viewdock/internal/reliability"
)

// CreatePath is the signed worker endpoint that creates a session for an
// asserted principal.
const CreatePath = "/api/v1/node/playback/sessions"

const (
	maxAttempts   = 3
	maxRelayBody  = 1 << 20
	sessionMemory = 20000
	sessionTTL    = 12 * time.Hour
	createTimeout = 60 * time.Second
)

var playbackRoles = []string{backend.RoleMediaWorker, backend.RoleTranscodeWorker}

// Relayed paths, matched against the path after /mesh/{node}.
var (
	sessionPathRE = regexp.MustCompile(`^/api/v1/playback/sessions/[A-Za-z0-9-]{1,64}(/(file|progress|subtitles|download|telemetry))?$`)
	hlsPathRE     = regexp.MustCompile(`^/hls/[A-Za-z0-9-]{1,64}/[A-Za-z0-9._-]{1,128}$`)
	nodeIDRE      = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
)

// Assertion is what the control plane vouches for when it asks a worker to
// create a session. It is only accepted with a valid node signature.
type Assertion struct {
	Principal   auth.Principal  `json:"principal"`
	ClientIP    string          `json:"client_ip"`
	PartyAccess bool            `json:"party_access"`
	Request     json.RawMessage `json:"request"`
}

type Dispatcher struct {
	Router *backend.Router
	Cfg    config.Config
	Log    *slog.Logger
	// Enabled reports whether playback should be placed on workers.
	Enabled func() bool
	// Reliability, when set, receives placement and relay outcomes per node.
	Reliability reliability.Recorder
	// Ranker, when set, reorders workers within a priority tier by measured
	// reliability and administrator overrides.
	Ranker Ranker

	api   *http.Client
	relay *http.Client

	mu       sync.Mutex
	sessions map[string]placed
}

type placed struct {
	node string
	at   time.Time
}

func NewDispatcher(router *backend.Router, cfg config.Config, logger *slog.Logger) *Dispatcher {
	tr := backend.NewTransport()
	return &Dispatcher{
		Router: router, Cfg: cfg, Log: logger,
		api:      &http.Client{Transport: tr, Timeout: createTimeout},
		relay:    &http.Client{Transport: tr},
		sessions: map[string]placed{},
	}
}

func (d *Dispatcher) Active() bool { return d != nil && d.Enabled != nil && d.Enabled() }

func (d *Dispatcher) remember(sessionID, nodeID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.sessions) >= sessionMemory {
		cutoff := time.Now().Add(-sessionTTL)
		for id, p := range d.sessions {
			if p.at.Before(cutoff) || len(d.sessions) >= sessionMemory {
				delete(d.sessions, id)
			}
		}
	}
	d.sessions[sessionID] = placed{node: nodeID, at: time.Now()}
}

func (d *Dispatcher) placedOn(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sessions[sessionID].node
}

func (d *Dispatcher) observe(o reliability.Outcome) {
	if d.Reliability != nil {
		d.Reliability.Observe(o)
	}
}

func (d *Dispatcher) warn(msg string, args ...any) {
	if d.Log != nil {
		d.Log.Warn(msg, append([]any{"category", "mesh"}, args...)...)
	}
}

// CreateSession places a session on the best available worker, trying the
// next candidate when a worker is unreachable, failing or full.
func (d *Dispatcher) CreateSession(w http.ResponseWriter, r *http.Request, p *auth.Principal, body []byte, partyAccess bool) {
	payload, err := json.Marshal(Assertion{Principal: *p, ClientIP: httpapi.ClientIPString(r, d.Cfg), PartyAccess: partyAccess, Request: body})
	if err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "mesh", "could not prepare the request")
		return
	}
	cands, err := d.Router.Candidates(r.Context(), backend.RouteRequest{Roles: playbackRoles})
	if err != nil || len(cands) == 0 {
		w.Header().Set("Retry-After", "5")
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "no_worker_available", "no media worker is available")
		return
	}
	cands = d.rankCandidates(r, cands)
	if len(cands) == 0 {
		w.Header().Set("Retry-After", "5")
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "no_worker_available", "no media worker is available")
		return
	}
	var replace struct {
		ReplaceSessionID string `json:"replace_session_id"`
	}
	_ = json.Unmarshal(body, &replace)
	if prev := d.placedOn(replace.ReplaceSessionID); prev != "" {
		for i, n := range cands {
			if n.ID == prev && i > 0 {
				cands = append([]backend.Node{n}, append(cands[:i:i], cands[i+1:]...)...)
				break
			}
		}
	}

	var lastStatus int
	var lastBody []byte
	for i, node := range cands {
		if i >= maxAttempts {
			break
		}
		started := time.Now()
		status, respBody, err := d.createOn(r, node, payload)
		outcome := reliability.Outcome{Source: "node:" + node.ID, Region: node.Region, Kind: reliability.KindStart,
			Success: err == nil && status == http.StatusOK, LatencyMS: time.Since(started).Milliseconds(),
			RequestID: middleware.GetReqID(r.Context())}
		if !outcome.Success {
			outcome.Reason = "placement failed"
			if err == nil {
				outcome.Reason = "worker answered " + strconv.Itoa(status)
			}
		}
		if err != nil || status == http.StatusOK || status >= 500 {
			d.observe(outcome)
		}
		if err != nil {
			if backend.IsDialError(err) || errors.Is(err, context.DeadlineExceeded) {
				d.Router.MarkDown(context.WithoutCancel(r.Context()), node.ID, err)
			}
			d.warn("session placement failed", "node", node.ID, "err", err.Error())
			continue
		}
		if status == http.StatusOK {
			out, sid, err := rewriteSession(respBody, node)
			if err != nil {
				d.warn("worker returned an unreadable session", "node", node.ID)
				continue
			}
			d.remember(sid, node.ID)
			httpapi.WriteJSON(w, http.StatusOK, out)
			return
		}
		lastStatus, lastBody = status, respBody
		if status == http.StatusTooManyRequests || status >= 500 {
			continue
		}
		break
	}
	if lastStatus != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(lastStatus)
		_, _ = w.Write(lastBody)
		return
	}
	w.Header().Set("Retry-After", "5")
	httpapi.WriteErr(w, http.StatusServiceUnavailable, "no_worker_available", "no media worker is available")
}

func (d *Dispatcher) createOn(r *http.Request, node backend.Node, payload []byte) (int, []byte, error) {
	return d.signedCall(r.Context(), node, http.MethodPost, CreatePath, payload, r.UserAgent())
}

// signedCall sends a request signed with the node credential and returns the
// status and a bounded body.
func (d *Dispatcher) signedCall(ctx context.Context, node backend.Node, method, path string, payload []byte, userAgent string) (int, []byte, error) {
	secret, err := d.Router.Secret(ctx, node.ID)
	if err != nil {
		return 0, nil, err
	}
	if secret == nil {
		return 0, nil, errors.New("node has no credential")
	}
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, node.BaseURL()+path, body)
	if err != nil {
		return 0, nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	if id := middleware.GetReqID(ctx); id != "" {
		req.Header.Set("X-Request-Id", id)
	}
	if err := nodeauth.Sign(req, node.ID, secret, payload); err != nil {
		return 0, nil, err
	}
	resp, err := d.api.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxRelayBody))
	return resp.StatusCode, b, err
}

// rewriteSession points every session URL at this control plane's relay for
// the node, and adds the stream token that authenticates relayed calls.
func rewriteSession(raw []byte, node backend.Node) (map[string]any, string, error) {
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", err
	}
	sid, _ := out["id"].(string)
	tok, _ := out["stoken"].(string)
	if sid == "" || tok == "" {
		return nil, "", errors.New("session id or token missing")
	}
	prefix := "/mesh/" + node.ID
	urls, _ := out["urls"].(map[string]any)
	if urls == nil {
		urls = map[string]any{}
	}
	for k, v := range urls {
		s, ok := v.(string)
		if !ok || !strings.HasPrefix(s, "/") {
			continue
		}
		if !strings.Contains(s, "stoken=") {
			s += "?stoken=" + url.QueryEscape(tok)
		}
		urls[k] = prefix + s
	}
	urls["session"] = prefix + "/api/v1/playback/sessions/" + sid
	out["urls"] = urls
	out["node"] = map[string]any{"id": node.ID, "name": node.Name, "region": node.Region}
	return out, sid, nil
}

// Relay forwards allowlisted session and HLS requests to the named node.
// Stream tokens authenticate them, so no cookie or credential is forwarded.
func (d *Dispatcher) Relay() http.Handler {
	r := chi.NewRouter()
	r.HandleFunc("/{node}/*", d.serveRelay)
	return r
}

func (d *Dispatcher) serveRelay(w http.ResponseWriter, r *http.Request) {
	nodeID := chi.URLParam(r, "node")
	rest := "/" + chi.URLParam(r, "*")
	if !nodeIDRE.MatchString(nodeID) || r.URL.Query().Get("stoken") == "" || !(sessionPathRE.MatchString(rest) || hlsPathRE.MatchString(rest)) {
		httpapi.WriteErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete:
	case http.MethodPost:
		if !strings.HasSuffix(rest, "/telemetry") {
			httpapi.WriteErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
	default:
		httpapi.WriteErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	node, ok := d.Router.Node(r.Context(), nodeID)
	if !ok {
		gone(w)
		return
	}
	secret, err := d.Router.Secret(r.Context(), nodeID)
	if err != nil || secret == nil {
		gone(w)
		return
	}
	var body []byte
	if r.Method == http.MethodPut || r.Method == http.MethodPost {
		body, err = io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err != nil {
			httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "could not read the request")
			return
		}
	}
	target, err := url.Parse(node.BaseURL())
	if err != nil {
		gone(w)
		return
	}
	proxy := &httputil.ReverseProxy{
		Transport: d.relay.Transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = target.Scheme, target.Host
			pr.Out.URL.Path, pr.Out.URL.RawPath = rest, ""
			pr.Out.URL.RawQuery = r.URL.RawQuery
			pr.Out.Host = target.Host
			for _, h := range []string{"Cookie", "Authorization", "X-CSRF-Token", "X-Forwarded-For", "X-Real-Ip", "Forwarded"} {
				pr.Out.Header.Del(h)
			}
			if body != nil {
				pr.Out.Body = io.NopCloser(bytes.NewReader(body))
				pr.Out.ContentLength = int64(len(body))
			}
			_ = nodeauth.Sign(pr.Out, nodeID, secret, body)
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("Set-Cookie")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			if errors.Is(req.Context().Err(), context.Canceled) {
				return
			}
			if backend.IsDialError(err) {
				d.Router.MarkDown(context.WithoutCancel(req.Context()), nodeID, err)
			}
			d.observe(reliability.Outcome{Source: "node:" + nodeID, Region: node.Region, Kind: reliability.KindFailure,
				Reason: "relay failed", RequestID: middleware.GetReqID(req.Context())})
			d.warn("relay to worker failed", "node", nodeID, "err", err.Error())
			gone(w)
		},
	}
	proxy.ServeHTTP(w, r)
}

// gone tells the player its worker is lost; it recreates the session at the
// current position, and placement then skips the failed node.
func gone(w http.ResponseWriter) {
	httpapi.WriteJSON(w, http.StatusGone, map[string]any{"code": "NODE_UNAVAILABLE", "resume_ms": int64(0)})
}
