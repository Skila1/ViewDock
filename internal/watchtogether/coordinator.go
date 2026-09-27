package watchtogether

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/nodeauth"
)

// Coordinator is the watch party surface used outside the room routes:
// playback authorization for party-only accounts, Discord commands, Labs
// sources and diagnostics. *Hub implements it in process and *Remote against
// a separately deployed coordinator.
type Coordinator interface {
	PartyAccess(principalID, itemKind, itemID string) bool
	Create(ctx context.Context, p *auth.Principal, itemKind, itemID string) (*Room, error)
	ReclaimOwner(ctx context.Context, p *auth.Principal, roomID string) bool
	Control(roomID, principalID, action string) error
	Invite(code string) *Room
	State(roomID string) map[string]any
	Rooms() []RoomSummary
	SyncSnapshot() map[string]any
}

var _ Coordinator = (*Hub)(nil)

// ErrUnavailable means a remote coordinator could not be reached.
var ErrUnavailable = errors.New("the watch party coordinator is unavailable")

const (
	// CoordinatorSigner is the signer ID control planes use.
	CoordinatorSigner    = "control"
	coordinatorMaxBody   = 64 << 10
	coordinatorTimeout   = 4 * time.Second
	coordinatorAPIPrefix = "/api/v1/coordinator"
)

type coordRequest struct {
	Principal   *auth.Principal `json:"principal,omitempty"`
	PrincipalID string          `json:"principal_id,omitempty"`
	ItemKind    string          `json:"item_kind,omitempty"`
	ItemID      string          `json:"item_id,omitempty"`
	Action      string          `json:"action,omitempty"`
}

type roomRef struct {
	ID         string `json:"id"`
	InviteCode string `json:"invite_code"`
	ItemKind   string `json:"item_kind"`
	ItemID     string `json:"item_id"`
	HostID     string `json:"host"`
	OwnerID    string `json:"owner"`
}

type roomSummaryJSON struct {
	ID       string `json:"id"`
	ItemKind string `json:"item_kind"`
	ItemID   string `json:"item_id"`
	Members  int    `json:"members"`
	Playing  bool   `json:"playing"`
}

func (h *Hub) refLocked(room *Room) roomRef {
	return roomRef{ID: room.ID, InviteCode: room.InviteCode, ItemKind: room.ItemKind, ItemID: room.ItemID, HostID: room.HostID, OwnerID: room.OwnerID}
}

// CoordinatorGuard admits only requests signed with the shared coordinator
// secret.
func CoordinatorGuard(v *nodeauth.Verifier, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := v.VerifyRequest(r, coordinatorMaxBody); err != nil {
				if errors.Is(err, nodeauth.ErrTooLarge) {
					httpapi.WriteErr(w, http.StatusRequestEntityTooLarge, "too_large", "request body too large")
					return
				}
				if log != nil {
					log.Warn("rejected unsigned or invalid coordinator request", "category", "watchtogether", "path", r.URL.Path, "reason", err.Error())
				}
				httpapi.WriteErr(w, http.StatusUnauthorized, "node_auth", "coordinator authentication required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// InternalRoutes serves the signed coordinator API under /api/v1/coordinator.
// Principals in requests were authenticated by the calling control plane.
func (h *Hub) InternalRoutes(r chi.Router) {
	r.Post("/party-access", h.handleInternalPartyAccess)
	r.Post("/rooms", h.handleInternalCreate)
	r.Get("/rooms", h.handleInternalRooms)
	r.Get("/rooms/{id}/state", h.handleInternalState)
	r.Post("/rooms/{id}/reclaim", h.handleInternalReclaim)
	r.Post("/rooms/{id}/control", h.handleInternalControl)
	r.Get("/invites/{code}", h.handleInternalInvite)
	r.Get("/snapshot", h.handleInternalSnapshot)
}

func decodeCoord(w http.ResponseWriter, r *http.Request) (coordRequest, bool) {
	var in coordRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid request")
		return in, false
	}
	return in, true
}

func (h *Hub) handleInternalPartyAccess(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeCoord(w, r)
	if !ok {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]bool{"allowed": h.PartyAccess(in.PrincipalID, in.ItemKind, in.ItemID)})
}

func (h *Hub) handleInternalCreate(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeCoord(w, r)
	if !ok {
		return
	}
	if in.Principal == nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "principal required")
		return
	}
	room, err := h.Create(r.Context(), in.Principal, in.ItemKind, in.ItemID)
	if err != nil {
		httpapi.WriteErr(w, http.StatusForbidden, "forbidden", "not allowed")
		return
	}
	h.mu.Lock()
	ref := h.refLocked(room)
	h.mu.Unlock()
	httpapi.WriteJSON(w, http.StatusOK, ref)
}

func (h *Hub) handleInternalRooms(w http.ResponseWriter, _ *http.Request) {
	rooms := h.Rooms()
	out := make([]roomSummaryJSON, 0, len(rooms))
	for _, s := range rooms {
		out = append(out, roomSummaryJSON(s))
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Hub) handleInternalState(w http.ResponseWriter, r *http.Request) {
	st := h.State(chi.URLParam(r, "id"))
	if st == nil {
		httpapi.WriteErr(w, http.StatusNotFound, "not_found", "room not found")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, st)
}

func (h *Hub) handleInternalReclaim(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeCoord(w, r)
	if !ok {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]bool{"ok": h.ReclaimOwner(r.Context(), in.Principal, chi.URLParam(r, "id"))})
}

func (h *Hub) handleInternalControl(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeCoord(w, r)
	if !ok {
		return
	}
	if err := h.Control(chi.URLParam(r, "id"), in.PrincipalID, in.Action); err != nil {
		httpapi.WriteErr(w, http.StatusForbidden, "forbidden", "not allowed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) handleInternalInvite(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	room := h.rooms[h.invites[chi.URLParam(r, "code")]]
	var ref roomRef
	if room != nil {
		ref = h.refLocked(room)
	}
	h.mu.Unlock()
	if room == nil {
		httpapi.WriteErr(w, http.StatusNotFound, "not_found", "room not found")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, ref)
}

func (h *Hub) handleInternalSnapshot(w http.ResponseWriter, _ *http.Request) {
	httpapi.WriteJSON(w, http.StatusOK, h.SyncSnapshot())
}

// Remote is a Coordinator backed by a separately deployed coordinator. Calls
// fail closed: when the coordinator is unreachable, party access is denied
// and rooms are reported as missing.
type Remote struct {
	base   *url.URL
	secret []byte
	client *http.Client
	log    *slog.Logger
}

var _ Coordinator = (*Remote)(nil)

// NewRemote returns a client for the coordinator at base, signing requests
// with secret.
func NewRemote(base *url.URL, secret string, log *slog.Logger) *Remote {
	return &Remote{
		base: base, secret: []byte(secret), log: log,
		client: &http.Client{Timeout: coordinatorTimeout, Transport: &http.Transport{
			Proxy: nil, MaxIdleConnsPerHost: 16, IdleConnTimeout: 90 * time.Second,
			TLSHandshakeTimeout: coordinatorTimeout, ResponseHeaderTimeout: coordinatorTimeout,
		}},
	}
}

// call performs a signed request and decodes a 2xx JSON response into out.
// It returns the status, or ErrUnavailable when the coordinator could not
// be reached or failed.
func (c *Remote) call(ctx context.Context, method, path string, in, out any) (int, error) {
	var body []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = b
	}
	ctx, cancel := context.WithTimeout(ctx, coordinatorTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+coordinatorAPIPrefix+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if id := middleware.GetReqID(ctx); id != "" {
		req.Header.Set(middleware.RequestIDHeader, id)
	}
	if err := nodeauth.Sign(req, CoordinatorSigner, c.secret, body); err != nil {
		return 0, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.warn(path, err)
		return 0, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusUnauthorized {
		err := fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
		c.warn(path, err)
		return resp.StatusCode, err
	}
	if resp.StatusCode/100 == 2 && out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("coordinator response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

func (c *Remote) warn(path string, err error) {
	if c.log != nil {
		c.log.Warn("watch party coordinator call failed", "category", "watchtogether", "path", path, "err", err.Error())
	}
}

func (c *Remote) PartyAccess(principalID, itemKind, itemID string) bool {
	var out struct {
		Allowed bool `json:"allowed"`
	}
	st, err := c.call(context.Background(), http.MethodPost, "/party-access",
		coordRequest{PrincipalID: principalID, ItemKind: itemKind, ItemID: itemID}, &out)
	return err == nil && st == http.StatusOK && out.Allowed
}

func (c *Remote) Create(ctx context.Context, p *auth.Principal, itemKind, itemID string) (*Room, error) {
	var ref roomRef
	st, err := c.call(ctx, http.MethodPost, "/rooms", coordRequest{Principal: p, ItemKind: itemKind, ItemID: itemID}, &ref)
	if err != nil {
		return nil, err
	}
	if st != http.StatusOK {
		return nil, errDenied
	}
	return ref.room(), nil
}

func (ref roomRef) room() *Room {
	return &Room{ID: ref.ID, InviteCode: ref.InviteCode, ItemKind: ref.ItemKind, ItemID: ref.ItemID, HostID: ref.HostID, OwnerID: ref.OwnerID}
}

func (c *Remote) ReclaimOwner(ctx context.Context, p *auth.Principal, roomID string) bool {
	var out struct {
		OK bool `json:"ok"`
	}
	st, err := c.call(ctx, http.MethodPost, "/rooms/"+url.PathEscape(roomID)+"/reclaim", coordRequest{Principal: p}, &out)
	return err == nil && st == http.StatusOK && out.OK
}

func (c *Remote) Control(roomID, principalID, action string) error {
	st, err := c.call(context.Background(), http.MethodPost, "/rooms/"+url.PathEscape(roomID)+"/control",
		coordRequest{PrincipalID: principalID, Action: action}, nil)
	if err != nil {
		return err
	}
	if st != http.StatusNoContent {
		return errDenied
	}
	return nil
}

func (c *Remote) Invite(code string) *Room {
	var ref roomRef
	st, err := c.call(context.Background(), http.MethodGet, "/invites/"+url.PathEscape(code), nil, &ref)
	if err != nil || st != http.StatusOK {
		return nil
	}
	return ref.room()
}

// State returns the room state with the integer fields typed as the
// in-process hub reports them.
func (c *Remote) State(roomID string) map[string]any {
	var st map[string]any
	code, err := c.call(context.Background(), http.MethodGet, "/rooms/"+url.PathEscape(roomID)+"/state", nil, &st)
	if err != nil || code != http.StatusOK || st == nil {
		return nil
	}
	for _, k := range []string{"position_ms", "server_ms", "seq"} {
		if v, ok := st[k].(float64); ok {
			st[k] = int64(v)
		}
	}
	return st
}

func (c *Remote) Rooms() []RoomSummary {
	var out struct {
		Items []roomSummaryJSON `json:"items"`
	}
	st, err := c.call(context.Background(), http.MethodGet, "/rooms", nil, &out)
	if err != nil || st != http.StatusOK {
		return []RoomSummary{}
	}
	rooms := make([]RoomSummary, 0, len(out.Items))
	for _, s := range out.Items {
		rooms = append(rooms, RoomSummary(s))
	}
	return rooms
}

// SyncSnapshot returns nil when the coordinator is unreachable.
func (c *Remote) SyncSnapshot() map[string]any {
	var snap map[string]any
	st, err := c.call(context.Background(), http.MethodGet, "/snapshot", nil, &snap)
	if err != nil || st != http.StatusOK || snap == nil {
		return nil
	}
	for _, k := range []string{"rooms", "members", "connected"} {
		if v, ok := snap[k].(float64); ok {
			snap[k] = int(v)
		}
	}
	for _, k := range []string{"max_drift_ms", "target_ms", "hard_ms", "server_ms"} {
		if v, ok := snap[k].(float64); ok {
			snap[k] = int64(v)
		}
	}
	return snap
}
