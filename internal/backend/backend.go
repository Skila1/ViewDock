package backend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/nodeauth"
	"github.com/viewdock/viewdock/internal/secrets"
)

var ErrNoCipher = errors.New("node credentials need a master key")

type Node struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Scheme       string `json:"scheme"`
	Role         string `json:"role"`
	Region       string `json:"region"`
	Capabilities string `json:"capabilities"`
	Priority     int    `json:"priority"`
	Weight       int    `json:"weight"`
	Capacity     int    `json:"capacity"`
	Enabled      bool   `json:"enabled"`
	Status       string `json:"status"`
	LatencyMS    int64  `json:"latency_ms"`
	Health       string `json:"health"`
	UpdatedAt    string `json:"updated_at"`
	CreatedAt    string `json:"created_at"`
	Draining     bool   `json:"draining"`
	// HasCredential reports whether a node secret is configured; the secret
	// itself is write-only.
	HasCredential bool   `json:"has_credential"`
	Failures      int    `json:"failures"`
	LastFailureAt string `json:"last_failure_at"`
	LastError     string `json:"last_error"`
}

// BaseURL is the address the control plane uses to reach the node.
func (n Node) BaseURL() string {
	return fmt.Sprintf("%s://%s", n.Scheme, net.JoinHostPort(n.Host, strconv.Itoa(n.Port)))
}

type Store struct {
	DB      *sql.DB
	Dialect db.Dialect
	Cipher  *secrets.Cipher
}

const nodeColumns = `id, name, host, port, scheme, role, region, capabilities, priority, weight, capacity,
	enabled, status, latency_ms, health, draining, created_at, updated_at, secret <> '', failures, last_failure_at, last_error`

type scanner interface{ Scan(...any) error }

func scanNode(row scanner) (Node, error) {
	var n Node
	var enabled, draining int
	err := row.Scan(&n.ID, &n.Name, &n.Host, &n.Port, &n.Scheme, &n.Role, &n.Region, &n.Capabilities, &n.Priority, &n.Weight, &n.Capacity,
		&enabled, &n.Status, &n.LatencyMS, &n.Health, &draining, &n.CreatedAt, &n.UpdatedAt, &n.HasCredential, &n.Failures, &n.LastFailureAt, &n.LastError)
	n.Enabled = enabled == 1
	n.Draining = draining == 1
	return n, err
}

func secretName(id string) string { return "node:" + id }

// SetSecret stores a node secret encrypted at rest.
func (s *Store) SetSecret(ctx context.Context, id, secret string) error {
	if s.Cipher == nil {
		return ErrNoCipher
	}
	enc, err := s.Cipher.Encrypt(secretName(id), secret)
	if err != nil {
		return err
	}
	res, err := s.execContext(ctx, `UPDATE backend_nodes SET secret = ?, updated_at = ? WHERE id = ?`, enc, time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// Secret returns the decrypted node secret, or "" when none is set.
func (s *Store) Secret(ctx context.Context, id string) (string, error) {
	var raw string
	if err := s.queryRowContext(ctx, `SELECT secret FROM backend_nodes WHERE id = ?`, id).Scan(&raw); err != nil {
		return "", err
	}
	if raw == "" {
		return "", nil
	}
	if s.Cipher == nil {
		return "", ErrNoCipher
	}
	return s.Cipher.Decrypt(secretName(id), raw)
}

// RecordFailure appends to a node's failure history.
func (s *Store) RecordFailure(ctx context.Context, id, msg string) error {
	if len(msg) > 300 {
		msg = msg[:300]
	}
	_, err := s.execContext(ctx, `UPDATE backend_nodes SET failures = failures + 1, last_failure_at = ?, last_error = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), msg, id)
	return err
}

func New(db *sql.DB) *Store {
	return &Store{DB: db, Dialect: dbpkgDialect(db)}
}

func NewWithDialect(sqlDB *sql.DB, dialect db.Dialect) *Store {
	return &Store{DB: sqlDB, Dialect: dialect}
}

func dbpkgDialect(_ *sql.DB) db.Dialect { return db.DialectSQLite }

func (s *Store) query(query string) string {
	if s == nil || s.Dialect != db.DialectPostgres {
		return query
	}
	return db.RewritePlaceholders(query)
}

func (s *Store) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.DB.ExecContext(ctx, s.query(query), args...)
}

func (s *Store) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.DB.QueryContext(ctx, s.query(query), args...)
}

func (s *Store) queryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return s.DB.QueryRowContext(ctx, s.query(query), args...)
}

func (s *Store) ensureSchema(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return errors.New("backend store is nil")
	}
	_, err := s.execContext(ctx, `
		CREATE TABLE IF NOT EXISTS backend_nodes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			host TEXT NOT NULL DEFAULT '',
			port INTEGER NOT NULL DEFAULT 0,
			scheme TEXT NOT NULL DEFAULT 'https',
			role TEXT NOT NULL DEFAULT 'media-worker',
			region TEXT NOT NULL DEFAULT '',
			capabilities TEXT NOT NULL DEFAULT '',
			priority INTEGER NOT NULL DEFAULT 0,
			weight INTEGER NOT NULL DEFAULT 0,
			capacity INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			status TEXT NOT NULL DEFAULT 'unknown',
			latency_ms INTEGER NOT NULL DEFAULT 0,
			health TEXT NOT NULL DEFAULT '',
			draining INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)
	`)
	return err
}

func (s *Store) Upsert(ctx context.Context, n Node) (Node, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return Node{}, err
	}
	if strings.TrimSpace(n.ID) == "" {
		n.ID = uuid.NewString()
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if n.CreatedAt == "" {
		n.CreatedAt = now
	}
	n.UpdatedAt = now
	if n.Status == "" {
		n.Status = "unknown"
	}
	if n.Scheme == "" {
		n.Scheme = "https"
	}
	if n.Role == "" {
		n.Role = "media-worker"
	}
	_, err := s.execContext(ctx, `
		INSERT INTO backend_nodes(
			id, name, host, port, scheme, role, region, capabilities, priority, weight, capacity,
			enabled, status, latency_ms, health, draining, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			host = excluded.host,
			port = excluded.port,
			scheme = excluded.scheme,
			role = excluded.role,
			region = excluded.region,
			capabilities = excluded.capabilities,
			priority = excluded.priority,
			weight = excluded.weight,
			capacity = excluded.capacity,
			enabled = excluded.enabled,
			status = excluded.status,
			latency_ms = excluded.latency_ms,
			health = excluded.health,
			draining = excluded.draining,
			updated_at = excluded.updated_at
	`,
		n.ID, n.Name, n.Host, n.Port, n.Scheme, n.Role, n.Region, n.Capabilities, n.Priority, n.Weight, n.Capacity,
		boolInt(n.Enabled), n.Status, n.LatencyMS, n.Health, boolInt(n.Draining), n.CreatedAt, n.UpdatedAt,
	)
	if err != nil {
		return Node{}, err
	}
	return s.Get(ctx, n.ID)
}

func (s *Store) Get(ctx context.Context, id string) (Node, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return Node{}, err
	}
	n, err := scanNode(s.queryRowContext(ctx, `SELECT `+nodeColumns+` FROM backend_nodes WHERE id = ?`, id))
	if err != nil {
		return Node{}, err
	}
	return n, nil
}

func (s *Store) List(ctx context.Context) ([]Node, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.queryContext(ctx, `SELECT `+nodeColumns+` FROM backend_nodes ORDER BY priority DESC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) SetHealth(ctx context.Context, id, status string, latencyMS int64, health string) error {
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}
	_, err := s.execContext(ctx, `
		UPDATE backend_nodes SET status = ?, latency_ms = ?, health = ?, updated_at = ? WHERE id = ?
	`, status, latencyMS, health, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (s *Store) Delete(ctx context.Context, id string) error {
	if err := s.ensureSchema(ctx); err != nil {
		return err
	}
	_, err := s.execContext(ctx, `DELETE FROM backend_nodes WHERE id = ?`, id)
	return err
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

type API struct {
	Store  *Store
	Router *Router
	Audit  *audit.Log
	Cfg    config.Config
}

func NewAPI(store *Store) *API { return &API{Store: store, Router: NewRouter(store)} }

// Roles a node may advertise. Playback is placed on media and transcode
// workers; storage workers only hold media.
const (
	RoleMediaWorker     = "media-worker"
	RoleTranscodeWorker = "transcode-worker"
	RoleStorageWorker   = "storage-worker"
)

func validNodeRole(role string) bool {
	return role == RoleMediaWorker || role == RoleTranscodeWorker || role == RoleStorageWorker
}

func (a *API) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/admin/nodes", a.handleList)
		r.Post("/admin/nodes", a.handleUpsert)
		r.Delete("/admin/nodes/{id}", a.handleDelete)
		r.Patch("/admin/nodes/{id}/drain", a.handleDrain)
		r.Post("/admin/nodes/{id}/probe", a.handleProbe)
		r.Post("/admin/nodes/{id}/credential", a.handleCredential)
		r.Get("/admin/nodes/route", a.handleRoute)
	})
}

func (a *API) audit(r *http.Request, action, target, detail string) {
	if a.Audit == nil {
		return
	}
	actor := ""
	if p := auth.FromRequest(r); p != nil {
		actor = p.UserID
	}
	a.Audit.Event(r.Context(), actor, action, target, httpapi.ClientIPString(r, a.Cfg), detail)
}

// handleCredential issues a new node secret, replacing any previous one. The
// secret is returned once; the worker is configured with it as VD_NODE_SECRET.
func (a *API) handleCredential(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := a.Store.Get(r.Context(), id); err != nil {
		httpapi.WriteErr(w, http.StatusNotFound, "nodes", "node not found")
		return
	}
	secret, err := nodeauth.NewSecret()
	if err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "nodes", "could not generate a credential")
		return
	}
	if err := a.Store.SetSecret(r.Context(), id, secret); err != nil {
		if errors.Is(err, ErrNoCipher) {
			httpapi.WriteErr(w, http.StatusServiceUnavailable, "no_master_key", err.Error())
			return
		}
		httpapi.WriteErr(w, http.StatusInternalServerError, "nodes", "could not store the credential")
		return
	}
	a.Router.Invalidate()
	a.audit(r, "node.credential", id, "rotated")
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"id": id, "secret": secret, "env": "VD_NODE_SECRET"})
}

func (a *API) handleProbe(w http.ResponseWriter, r *http.Request) {
	if err := a.Router.Probe(r.Context(), chi.URLParam(r, "id")); err != nil {
		httpapi.WriteErr(w, http.StatusBadGateway, "nodes", err.Error())
		return
	}
	node, err := a.Store.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpapi.WriteErr(w, http.StatusNotFound, "nodes", "node not found")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, node)
}

func (a *API) handleRoute(w http.ResponseWriter, r *http.Request) {
	node, err := a.Router.Select(r.Context(), RouteRequest{
		Role: r.URL.Query().Get("role"), Capability: r.URL.Query().Get("capability"), Region: r.URL.Query().Get("region"),
	})
	if err != nil {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "nodes", err.Error())
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, node)
}

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	if a == nil || a.Store == nil {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "nodes", "node store unavailable")
		return
	}
	list, err := a.Store.List(r.Context())
	if err != nil {
		slog.Error("list nodes", "err", err)
		httpapi.WriteErr(w, http.StatusInternalServerError, "nodes", "nodes could not be loaded")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (a *API) handleUpsert(w http.ResponseWriter, r *http.Request) {
	var body Node
	if err := httpapi.ReadJSON(r, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		httpapi.WriteErr(w, http.StatusBadRequest, "nodes", "name is required")
		return
	}
	body.Host = strings.TrimSpace(body.Host)
	if body.Scheme == "" {
		body.Scheme = "https"
	}
	if err := ValidateEndpoint(body.Scheme, body.Host, body.Port); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "invalid_endpoint", err.Error())
		return
	}
	if body.Role == "" {
		body.Role = RoleMediaWorker
	}
	if !validNodeRole(body.Role) {
		httpapi.WriteErr(w, http.StatusBadRequest, "nodes", "role must be media-worker, transcode-worker or storage-worker")
		return
	}
	if body.Weight < 0 || body.Capacity < 0 {
		httpapi.WriteErr(w, http.StatusBadRequest, "nodes", "weight and capacity cannot be negative")
		return
	}
	// Health is owned by the monitor; clients cannot mark a node healthy.
	body.Status, body.LatencyMS, body.Health = "unknown", 0, ""
	action := "node.create"
	if body.ID != "" {
		cur, err := a.Store.Get(r.Context(), body.ID)
		if err != nil {
			httpapi.WriteErr(w, http.StatusNotFound, "nodes", "node not found")
			return
		}
		action = "node.update"
		body.CreatedAt = cur.CreatedAt
		if cur.BaseURL() == body.BaseURL() {
			body.Status, body.LatencyMS, body.Health = cur.Status, cur.LatencyMS, cur.Health
		}
	}
	out, err := a.Store.Upsert(r.Context(), body)
	if err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "nodes", "the node could not be saved")
		return
	}
	a.Router.Invalidate()
	a.audit(r, action, out.ID, out.BaseURL()+" role="+out.Role)
	httpapi.WriteJSON(w, http.StatusOK, out)
}

// ValidateEndpoint rejects node addresses that are malformed or point at
// cloud metadata, link-local, multicast or unspecified addresses. Loopback and
// private ranges stay allowed for nodes on the same host or LAN.
func ValidateEndpoint(scheme, host string, port int) error {
	if scheme != "http" && scheme != "https" {
		return errors.New("scheme must be http or https")
	}
	if port < 1 || port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if host == "" || len(host) > 253 || strings.ContainsAny(host, "/\\@?#% \t\r\n[]") {
		return errors.New("host must be a hostname or IP address")
	}
	if ip := net.ParseIP(host); ip != nil {
		return checkNodeIP(ip)
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 {
			return errors.New("host must be a hostname or IP address")
		}
	}
	return nil
}

func checkNodeIP(ip net.IP) error {
	if ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return fmt.Errorf("address %s is not allowed for a node", ip)
	}
	return nil
}

func (a *API) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if strings.TrimSpace(id) == "" {
		httpapi.WriteErr(w, http.StatusBadRequest, "nodes", "id required")
		return
	}
	if err := a.Store.Delete(r.Context(), id); err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "nodes", "the node could not be deleted")
		return
	}
	a.Router.Invalidate()
	a.audit(r, "node.delete", id, "")
	httpapi.WriteOK(w)
}

func (a *API) handleDrain(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Draining bool `json:"draining"`
	}
	if err := httpapi.ReadJSON(r, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	cur, err := a.Store.Get(r.Context(), id)
	if err != nil {
		httpapi.WriteErr(w, http.StatusNotFound, "nodes", "node not found")
		return
	}
	cur.Draining = body.Draining
	if _, err := a.Store.Upsert(r.Context(), cur); err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "nodes", "the node could not be saved")
		return
	}
	a.Router.Invalidate()
	a.audit(r, "node.drain", id, strconv.FormatBool(body.Draining))
	httpapi.WriteJSON(w, http.StatusOK, cur)
}

func (a *API) UpdateStatus(ctx context.Context, id, status string, latencyMS int64, health string) error {
	return a.Store.SetHealth(ctx, id, status, latencyMS, health)
}
