package jellyfin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/secrets"
)

// SyncInterval is how often enabled sources are resynced.
const SyncInterval = 6 * time.Hour

var (
	errNoCipher = errors.New("a server master key is required to store media source credentials")
	errNotExist = errors.New("media source not found")
)

// Source is the admin view of a connected server. Credentials are never
// included.
type Source struct {
	ID         string   `json:"id"`
	LibraryID  string   `json:"library_id"`
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	Username   string   `json:"username"`
	Views      []string `json:"libraries"`
	Enabled    bool     `json:"enabled"`
	Status     string   `json:"status"`
	LastError  string   `json:"last_error"`
	LastSyncAt string   `json:"last_sync_at"`
	ItemCount  int      `json:"item_count"`
	Syncing    bool     `json:"syncing"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`

	remoteUser string
}

// Service owns media sources, their sync and stream grants.
type Service struct {
	DB       *sql.DB
	Cipher   func() *secrets.Cipher
	CacheDir string
	Log      *slog.Logger
	Audit    *audit.Log
	Cfg      config.Config
	HTTP     *http.Client

	mu      sync.Mutex
	syncing map[string]bool
	grants  map[string]*grant
}

func New(db *sql.DB, cipher func() *secrets.Cipher, cacheDir string, log *slog.Logger) *Service {
	return &Service{
		DB: db, Cipher: cipher, CacheDir: cacheDir, Log: log,
		HTTP: &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ResponseHeaderTimeout: 60 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConnsPerHost:   16,
		}},
		syncing: map[string]bool{}, grants: map[string]*grant{},
	}
}

const sourceCols = `id, library_id, name, url, username, remote_user, views, enabled, status, last_error, last_sync_at, item_count, created_at, updated_at`

func (s *Service) scan(row interface{ Scan(...any) error }) (Source, error) {
	var src Source
	var views string
	var enabled int
	err := row.Scan(&src.ID, &src.LibraryID, &src.Name, &src.URL, &src.Username, &src.remoteUser, &views, &enabled,
		&src.Status, &src.LastError, &src.LastSyncAt, &src.ItemCount, &src.CreatedAt, &src.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, errNotExist
	}
	if err != nil {
		return Source{}, err
	}
	src.Enabled = enabled == 1
	if json.Unmarshal([]byte(views), &src.Views) != nil || src.Views == nil {
		src.Views = []string{}
	}
	s.mu.Lock()
	src.Syncing = s.syncing[src.ID]
	s.mu.Unlock()
	return src, nil
}

func (s *Service) get(ctx context.Context, id string) (Source, error) {
	return s.scan(s.DB.QueryRowContext(ctx, `SELECT `+sourceCols+` FROM media_sources WHERE id = ?`, id))
}

func (s *Service) list(ctx context.Context) ([]Source, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+sourceCols+` FROM media_sources ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		src, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

func (s *Service) cipher() (*secrets.Cipher, error) {
	if s.Cipher == nil {
		return nil, errNoCipher
	}
	c := s.Cipher()
	if c == nil {
		return nil, errNoCipher
	}
	return c, nil
}

func secretName(kind, id string) string { return "media_source." + kind + ":" + id }

func (s *Service) seal(kind, id, value string) (string, error) {
	c, err := s.cipher()
	if err != nil {
		return "", err
	}
	return c.Encrypt(secretName(kind, id), value)
}

func (s *Service) open(ctx context.Context, kind, id string) (string, error) {
	c, err := s.cipher()
	if err != nil {
		return "", err
	}
	var enc string
	if err := s.DB.QueryRowContext(ctx, `SELECT `+kind+`_enc FROM media_sources WHERE id = ?`, id).Scan(&enc); err != nil {
		return "", err
	}
	if enc == "" {
		return "", nil
	}
	return c.Decrypt(secretName(kind, id), enc)
}

func (s *Service) newClient(base, sourceID string) *client {
	return &client{base: base, device: "viewdock-" + sourceID, http: s.HTTP}
}

// connect returns an authenticated client, signing in again with the stored
// password when there is no token or the token was revoked.
func (s *Service) connect(ctx context.Context, src Source, forceLogin bool) (*client, error) {
	c := s.newClient(src.URL, src.ID)
	if !forceLogin {
		tok, err := s.open(ctx, "token", src.ID)
		if err != nil {
			return nil, err
		}
		if tok != "" && src.remoteUser != "" {
			c.token = tok
			return c, nil
		}
	}
	pw, err := s.open(ctx, "password", src.ID)
	if err != nil {
		return nil, err
	}
	tok, userID, err := c.authenticate(ctx, src.Username, pw)
	if err != nil {
		return nil, err
	}
	enc, err := s.seal("token", src.ID, tok)
	if err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE media_sources SET token_enc = ?, remote_user = ? WHERE id = ?`, enc, userID, src.ID); err != nil {
		return nil, err
	}
	return c, nil
}

// withClient runs fn with an authenticated client and retries once with a
// fresh login when the stored token is rejected.
func (s *Service) withClient(ctx context.Context, src Source, fn func(c *client, userID string) error) error {
	c, err := s.connect(ctx, src, false)
	if err != nil {
		return err
	}
	userID := src.remoteUser
	if userID == "" {
		fresh, _ := s.get(ctx, src.ID)
		userID = fresh.remoteUser
	}
	err = fn(c, userID)
	if !errors.Is(err, errUnauthorized) {
		return err
	}
	if c, err = s.connect(ctx, src, true); err != nil {
		return err
	}
	fresh, err := s.get(ctx, src.ID)
	if err != nil {
		return err
	}
	return fn(c, fresh.remoteUser)
}

func (s *Service) setStatus(ctx context.Context, id, status, lastErr string, count int, synced bool) {
	now := time.Now().UTC().Format(time.RFC3339)
	q := `UPDATE media_sources SET status = ?, last_error = ?, updated_at = ?`
	args := []any{status, lastErr, now}
	if synced {
		q += `, last_sync_at = ?, item_count = ?`
		args = append(args, now, count)
	}
	q += ` WHERE id = ?`
	args = append(args, id)
	if _, err := s.DB.ExecContext(ctx, q, args...); err != nil && s.Log != nil {
		s.Log.Warn("media source status", "category", "media_sources", "id", id, "err", err.Error())
	}
}

// Start syncs enabled sources shortly after boot and then periodically.
func (s *Service) Start(ctx context.Context) {
	go func() {
		timer := time.NewTimer(time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			s.syncDue(ctx)
			s.sweepGrants()
			timer.Reset(15 * time.Minute)
		}
	}()
}

func (s *Service) syncDue(ctx context.Context) {
	sources, err := s.list(ctx)
	if err != nil {
		return
	}
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		last, _ := time.Parse(time.RFC3339, src.LastSyncAt)
		if src.Status == "error" || time.Since(last) >= SyncInterval {
			s.Sync(ctx, src.ID)
		}
	}
}
