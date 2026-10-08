package backend

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/viewdock/viewdock/internal/nodeauth"
)

var ErrNoHealthyNode = errors.New("no healthy backend node available")

const (
	nodeCacheTTL = 2 * time.Second
	// softFailures is how many consecutive timeouts or error responses mark a
	// node unhealthy. A refused connection marks it unhealthy at once.
	softFailures = 2
	probeTimeout = 3 * time.Second
	// StatusPath is the signed worker endpoint used for health probes.
	StatusPath = "/api/v1/node/status"
)

type RouteRequest struct {
	Role       string
	Roles      []string
	Capability string
	Region     string
}

func (q RouteRequest) roleOK(role string) bool {
	if q.Role != "" && role != q.Role {
		return false
	}
	if len(q.Roles) == 0 {
		return true
	}
	for _, r := range q.Roles {
		if r == role {
			return true
		}
	}
	return false
}

type Router struct {
	Store  *Store
	Client *http.Client

	mu      sync.Mutex
	cur     map[string]int
	nodes   []Node
	loaded  time.Time
	fails   map[string]int
	secrets map[string][]byte
}

type Monitor struct {
	router   *Router
	interval time.Duration
	stop     chan struct{}
	done     chan struct{}
}

func NewMonitor(router *Router, interval time.Duration) *Monitor {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Monitor{router: router, interval: interval, stop: make(chan struct{}), done: make(chan struct{})}
}

func (m *Monitor) Start() {
	if m == nil || m.router == nil {
		return
	}
	go func() {
		defer close(m.done)
		m.check(context.Background())
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.check(context.Background())
			case <-m.stop:
				return
			}
		}
	}()
}

func (m *Monitor) Close() {
	if m == nil || m.router == nil {
		return
	}
	select {
	case <-m.stop:
	default:
		close(m.stop)
	}
	<-m.done
}

func (m *Monitor) check(ctx context.Context) {
	nodes, err := m.router.Store.List(ctx)
	if err != nil {
		return
	}
	var wg sync.WaitGroup
	for _, node := range nodes {
		if !node.Enabled {
			continue
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_ = m.router.Probe(ctx, id)
		}(node.ID)
	}
	wg.Wait()
}

// NewTransport dials only addresses allowed for nodes, re-checked after DNS
// resolution so a hostname cannot later be pointed at a blocked address.
func NewTransport() *http.Transport {
	d := &net.Dialer{
		Timeout:   3 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("unresolved node address %q", host)
			}
			return checkNodeIP(ip)
		},
	}
	return &http.Transport{
		Proxy:               nil,
		DialContext:         d.DialContext,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 5 * time.Second,
		MaxIdleConnsPerHost: 64,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
	}
}

func NewRouter(store *Store) *Router {
	return &Router{
		Store:   store,
		Client:  &http.Client{Transport: NewTransport(), Timeout: probeTimeout},
		cur:     map[string]int{},
		fails:   map[string]int{},
		secrets: map[string][]byte{},
	}
}

// Invalidate drops cached nodes and credentials after an admin change.
func (r *Router) Invalidate() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.loaded = time.Time{}
	r.secrets = map[string][]byte{}
	r.mu.Unlock()
}

// cached returns recently loaded nodes. When the database is unreachable it
// keeps serving the last known set so active routing survives the outage.
func (r *Router) cached(ctx context.Context) ([]Node, error) {
	r.mu.Lock()
	if time.Since(r.loaded) < nodeCacheTTL && r.nodes != nil {
		out := append([]Node(nil), r.nodes...)
		r.mu.Unlock()
		return out, nil
	}
	r.mu.Unlock()
	nodes, err := r.Store.List(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		if r.nodes != nil {
			return append([]Node(nil), r.nodes...), nil
		}
		return nil, err
	}
	r.nodes, r.loaded = nodes, time.Now()
	return append([]Node(nil), nodes...), nil
}

// Nodes returns every registered node from the cache.
func (r *Router) Nodes(ctx context.Context) ([]Node, error) {
	if r == nil || r.Store == nil {
		return nil, ErrNoHealthyNode
	}
	return r.cached(ctx)
}

// Node returns a registered node from the cache.
func (r *Router) Node(ctx context.Context, id string) (Node, bool) {
	nodes, err := r.cached(ctx)
	if err != nil {
		return Node{}, false
	}
	for _, n := range nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

func (r *Router) Select(ctx context.Context, req RouteRequest) (Node, error) {
	list, err := r.Candidates(ctx, req)
	if err != nil {
		return Node{}, err
	}
	return list[0], nil
}

// Candidates lists eligible nodes in routing order: highest priority tier
// first (primary before standby), with weighted rotation inside a tier.
func (r *Router) Candidates(ctx context.Context, req RouteRequest) ([]Node, error) {
	if r == nil || r.Store == nil {
		return nil, ErrNoHealthyNode
	}
	nodes, err := r.cached(ctx)
	if err != nil {
		return nil, err
	}
	tiers := map[int][]Node{}
	var prios []int
	for _, node := range nodes {
		if !node.Enabled || node.Draining || node.Status != "healthy" || !req.roleOK(node.Role) {
			continue
		}
		if req.Region != "" && node.Region != req.Region {
			continue
		}
		if req.Capability != "" && !hasCapability(node.Capabilities, req.Capability) {
			continue
		}
		if _, ok := tiers[node.Priority]; !ok {
			prios = append(prios, node.Priority)
		}
		tiers[node.Priority] = append(tiers[node.Priority], node)
	}
	if len(prios) == 0 {
		return nil, ErrNoHealthyNode
	}
	sort.Sort(sort.Reverse(sort.IntSlice(prios)))
	out := make([]Node, 0, len(nodes))
	for i, p := range prios {
		tier := tiers[p]
		if i == 0 {
			first := r.weighted(tier)
			out = append(out, first)
			for _, n := range byWeight(tier) {
				if n.ID != first.ID {
					out = append(out, n)
				}
			}
			continue
		}
		out = append(out, byWeight(tier)...)
	}
	return out, nil
}

func byWeight(nodes []Node) []Node {
	out := append([]Node(nil), nodes...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Weight > out[j].Weight })
	return out
}

func (r *Router) weighted(nodes []Node) Node {
	r.mu.Lock()
	defer r.mu.Unlock()
	total, selected := 0, 0
	best := -1 << 30
	for i := range nodes {
		weight := nodes[i].Weight
		if weight <= 0 {
			weight = 1
		}
		r.cur[nodes[i].ID] += weight
		total += weight
		if r.cur[nodes[i].ID] > best {
			best = r.cur[nodes[i].ID]
			selected = i
		}
	}
	r.cur[nodes[selected].ID] -= total
	return nodes[selected]
}

// Secret returns the node credential, cached after the first decrypt.
func (r *Router) Secret(ctx context.Context, id string) ([]byte, error) {
	r.mu.Lock()
	if s, ok := r.secrets[id]; ok {
		r.mu.Unlock()
		return s, nil
	}
	r.mu.Unlock()
	plain, err := r.Store.Secret(ctx, id)
	if err != nil {
		return nil, err
	}
	if plain == "" {
		return nil, nil
	}
	r.mu.Lock()
	r.secrets[id] = []byte(plain)
	r.mu.Unlock()
	return []byte(plain), nil
}

func (r *Router) setCachedStatus(id, status string) {
	r.mu.Lock()
	for i := range r.nodes {
		if r.nodes[i].ID == id {
			r.nodes[i].Status = status
		}
	}
	r.mu.Unlock()
}

// MarkDown takes a node out of rotation after a failed request, such as a
// refused connection while relaying playback.
func (r *Router) MarkDown(ctx context.Context, id string, cause error) {
	msg := "unreachable"
	if cause != nil {
		msg = cause.Error()
	}
	r.setCachedStatus(id, "unhealthy")
	r.mu.Lock()
	r.fails[id] = softFailures
	r.mu.Unlock()
	_ = r.Store.RecordFailure(ctx, id, msg)
	_ = r.Store.SetHealth(ctx, id, "unhealthy", 0, msg)
}

// IsDialError reports failures to reach the node at all.
func IsDialError(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	return errors.Is(err, syscall.ECONNREFUSED)
}

func (r *Router) Probe(ctx context.Context, id string) error {
	node, err := r.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := ValidateEndpoint(node.Scheme, node.Host, node.Port); err != nil {
		r.probeFailed(ctx, node, err, true)
		return err
	}
	secret, err := r.Secret(ctx, id)
	if err != nil {
		r.probeFailed(ctx, node, fmt.Errorf("node credential: %w", err), true)
		return err
	}
	path := "/healthz"
	if secret != nil {
		path = StatusPath
	}
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, node.BaseURL()+path, nil)
	if err != nil {
		r.probeFailed(ctx, node, err, true)
		return err
	}
	if secret != nil {
		if err := nodeauth.Sign(req, id, secret, nil); err != nil {
			return err
		}
	}
	started := time.Now()
	resp, err := r.Client.Do(req)
	if err != nil {
		r.probeFailed(ctx, node, err, IsDialError(err))
		return err
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err = fmt.Errorf("health endpoint returned %s", resp.Status)
		r.probeFailed(ctx, node, err, resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden)
		return err
	}
	health := "ok"
	if secret != nil {
		var buf bytes.Buffer
		if json.Compact(&buf, body) == nil && buf.Len() <= 2048 {
			health = buf.String()
		}
	}
	r.mu.Lock()
	delete(r.fails, id)
	r.mu.Unlock()
	r.setCachedStatus(id, "healthy")
	return r.Store.SetHealth(ctx, id, "healthy", time.Since(started).Milliseconds(), health)
}

func (r *Router) probeFailed(ctx context.Context, node Node, cause error, hard bool) {
	msg := strings.TrimSpace(cause.Error())
	r.mu.Lock()
	r.fails[node.ID]++
	n := r.fails[node.ID]
	r.mu.Unlock()
	_ = r.Store.RecordFailure(ctx, node.ID, msg)
	if hard || n >= softFailures || node.Status != "healthy" {
		r.setCachedStatus(node.ID, "unhealthy")
		_ = r.Store.SetHealth(ctx, node.ID, "unhealthy", 0, msg)
	}
}

func hasCapability(list, wanted string) bool {
	wanted = strings.TrimSpace(strings.ToLower(wanted))
	for _, item := range strings.Split(list, ",") {
		if strings.TrimSpace(strings.ToLower(item)) == wanted {
			return true
		}
	}
	return false
}
