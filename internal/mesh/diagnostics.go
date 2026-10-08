package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/viewdock/viewdock/internal/backend"
	"github.com/viewdock/viewdock/internal/diagnostics"
)

const (
	nodeDiagSessionsPath = "/api/v1/node/diagnostics/sessions"
	nodeDiagTimelinePath = "/api/v1/node/diagnostics/flight-recorder/"
	diagTimeout          = 4 * time.Second
	remoteSessionLimit   = 200
	maxDiagNodes         = 32
)

var sessionIDRE = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// diagNodes lists enabled, reachable playback workers, the remembered owner
// of sessionID first. Draining workers are included because they still hold
// sessions.
func (d *Dispatcher) diagNodes(ctx context.Context, sessionID string) []backend.Node {
	nodes, err := d.Router.Nodes(ctx)
	if err != nil {
		return nil
	}
	owner := d.placedOn(sessionID)
	out := make([]backend.Node, 0, len(nodes))
	for _, n := range nodes {
		if !n.Enabled || n.Status != "healthy" || (n.Role != backend.RoleMediaWorker && n.Role != backend.RoleTranscodeWorker) {
			continue
		}
		if n.ID == owner {
			out = append([]backend.Node{n}, out...)
			continue
		}
		out = append(out, n)
	}
	if len(out) > maxDiagNodes {
		out = out[:maxDiagNodes]
	}
	return out
}

func (d *Dispatcher) fetchJSON(ctx context.Context, node backend.Node, path string, dst any) error {
	ctx, cancel := context.WithTimeout(ctx, diagTimeout)
	defer cancel()
	status, body, err := d.signedCall(ctx, node, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("worker answered %d", status)
	}
	return json.Unmarshal(body, dst)
}

// Timeline returns the flight recorder timeline a worker holds for a session
// placed on it. The remembered owner is asked first; after a control plane
// restart the other workers are asked in turn. Each event is labelled with
// the worker that recorded it.
func (d *Dispatcher) Timeline(ctx context.Context, sessionID string) []diagnostics.Event {
	if d == nil || d.Router == nil || !sessionIDRE.MatchString(sessionID) {
		return nil
	}
	for _, node := range d.diagNodes(ctx, sessionID) {
		var events []diagnostics.Event
		if err := d.fetchJSON(ctx, node, nodeDiagTimelinePath+sessionID, &events); err != nil {
			d.warn("worker timeline unavailable", "node", node.ID, "err", err.Error())
			continue
		}
		if len(events) == 0 {
			continue
		}
		for i := range events {
			events[i].Node = node.ID
		}
		return events
	}
	return nil
}

// RemoteSessions gathers retained session summaries from every reachable
// worker concurrently. Unreachable workers are skipped and logged.
func (d *Dispatcher) RemoteSessions(ctx context.Context) []diagnostics.SessionSummary {
	if d == nil || d.Router == nil {
		return nil
	}
	nodes := d.diagNodes(ctx, "")
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out []diagnostics.SessionSummary
	)
	for _, node := range nodes {
		wg.Add(1)
		go func(node backend.Node) {
			defer wg.Done()
			var resp struct {
				Items []diagnostics.SessionSummary `json:"items"`
			}
			if err := d.fetchJSON(ctx, node, nodeDiagSessionsPath, &resp); err != nil {
				d.warn("worker sessions unavailable", "node", node.ID, "err", err.Error())
				return
			}
			for i := range resp.Items {
				resp.Items[i].Node = node.ID
			}
			mu.Lock()
			out = append(out, resp.Items...)
			mu.Unlock()
		}(node)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt.After(out[j].LastAt) })
	return out
}
