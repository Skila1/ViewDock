package share

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/library"
)

type Service struct {
	DB      *sql.DB
	Catalog library.MediaCatalog
	mu      sync.Mutex
	seen    map[string]time.Time // guestSessionID -> last heartbeat (memory)
}

func New(db *sql.DB, cat library.MediaCatalog) *Service {
	return &Service{DB: db, Catalog: cat, seen: map[string]time.Time{}}
}

type Share struct {
	ID             string
	ItemKind       string
	ItemID         string
	HasPassword    bool
	ExpiresAt      string
	MaxConcurrent  int
	AllowedQuality string
	AllowDownload  bool
}

func (s *Service) Create(ctx context.Context, actor *auth.Principal, itemKind, itemID, password string, maxConc int, quality string, download bool, ttl time.Duration) (raw string, sh Share, err error) {
	if actor == nil || !actor.IsAdmin {
		return "", Share{}, errors.New("admin only")
	}
	if s.Catalog != nil && !s.Catalog.Exists(ctx, itemKind, itemID) {
		return "", Share{}, errors.New("item not found")
	}
	raw, err = auth.RandomToken(32)
	if err != nil {
		return "", Share{}, err
	}
	ph := ""
	if password != "" {
		ph, err = auth.HashPassword(password)
		if err != nil {
			return "", Share{}, err
		}
	}
	now := time.Now().UTC()
	var exp any
	if ttl > 0 {
		exp = now.Add(ttl).Format(time.RFC3339)
	}
	id := uuid.NewString()
	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO shares(id, token_hash, item_kind, item_id, created_by, password_hash, expires_at, max_concurrent, allowed_quality, allow_download, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, auth.HashToken(raw), itemKind, itemID, actor.UserID, ph, exp, maxConc, quality, boolInt(download), now.Format(time.RFC3339))
	sh = Share{ID: id, ItemKind: itemKind, ItemID: itemID, HasPassword: password != "", MaxConcurrent: maxConc, AllowedQuality: quality, AllowDownload: download}
	return raw, sh, err
}

func (s *Service) LookupByToken(ctx context.Context, raw string) (id string, sh Share, passHash string, revoked bool, err error) {
	var exp, rev sql.NullString
	var dl int
	err = s.DB.QueryRowContext(ctx, `
		SELECT id, item_kind, item_id, password_hash, expires_at, revoked_at, max_concurrent, allowed_quality, allow_download
		FROM shares WHERE token_hash = ?
	`, auth.HashToken(raw)).Scan(&id, &sh.ItemKind, &sh.ItemID, &passHash, &exp, &rev, &sh.MaxConcurrent, &sh.AllowedQuality, &dl)
	if err != nil {
		return "", Share{}, "", false, err
	}
	sh.ID = id
	sh.HasPassword = passHash != ""
	sh.AllowDownload = dl == 1
	if exp.Valid {
		sh.ExpiresAt = exp.String
		if t, e := time.Parse(time.RFC3339, exp.String); e == nil && time.Now().UTC().After(t) {
			return "", Share{}, "", true, sql.ErrNoRows
		}
	}
	if rev.Valid && rev.String != "" {
		return "", Share{}, "", true, sql.ErrNoRows
	}
	return id, sh, passHash, false, nil
}

func (s *Service) Revoke(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE shares SET revoked_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (s *Service) MintGuest(ctx context.Context, shareID, ip string) (raw string, exp time.Time, err error) {
	raw, err = auth.RandomToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	now := time.Now().UTC()
	exp = now.Add(24 * time.Hour)
	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO guest_sessions(id, share_id, token_hash, created_at, last_seen_at, expires_at, ip)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, uuid.NewString(), shareID, auth.HashToken(raw), now.Format(time.RFC3339), now.Format(time.RFC3339), exp.Format(time.RFC3339), ip)
	return raw, exp, err
}

func (s *Service) activeCount(ctx context.Context, shareID string) int {
	cut := time.Now().UTC().Add(-90 * time.Second).Format(time.RFC3339)
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM guest_sessions WHERE share_id = ? AND last_seen_at >= ?`, shareID, cut).Scan(&n)
	return n
}

func (s *Service) touchGuest(ctx context.Context, guestID string) {
	now := time.Now().UTC()
	s.mu.Lock()
	prev := s.seen[guestID]
	s.seen[guestID] = now
	s.mu.Unlock()
	if now.Sub(prev) < 30*time.Second {
		return
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE guest_sessions SET last_seen_at = ? WHERE id = ?`, now.Format(time.RFC3339), guestID)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Service) AllowStream(ctx context.Context, guestSessionID, itemKind, itemID string) error {
	return s.check(ctx, guestSessionID, itemKind, itemID, true)
}

func (s *Service) CanStreamMedia(ctx context.Context, guestSessionID, itemKind, itemID string) error {
	return s.check(ctx, guestSessionID, itemKind, itemID, false)
}

func (s *Service) check(ctx context.Context, guestSessionID, itemKind, itemID string, mintSlot bool) error {
	var shareID, kind, mid string
	var maxc int
	var rev, exp sql.NullString
	err := s.DB.QueryRowContext(ctx, `
		SELECT gs.share_id, sh.item_kind, sh.item_id, sh.max_concurrent, sh.revoked_at, sh.expires_at
		FROM guest_sessions gs JOIN shares sh ON sh.id = gs.share_id
		WHERE gs.id = ?
	`, guestSessionID).Scan(&shareID, &kind, &mid, &maxc, &rev, &exp)
	if err != nil {
		return ErrGone
	}
	if rev.Valid && rev.String != "" {
		return ErrGone
	}
	if exp.Valid && exp.String != "" {
		if t, e := time.Parse(time.RFC3339, exp.String); e == nil && time.Now().UTC().After(t) {
			return ErrGone
		}
	}
	if kind != itemKind || mid != itemID {
		return ErrDenied
	}
	if mintSlot && maxc > 0 && s.activeCount(ctx, shareID) > maxc {
		return ErrBusy
	}
	s.touchGuest(ctx, guestSessionID)
	return nil
}

func (s *Service) Heartbeat(ctx context.Context, guestSessionID string) error {
	s.touchGuest(ctx, guestSessionID)
	return nil
}

func (s *Service) Release(ctx context.Context, guestSessionID string) {
	s.mu.Lock()
	delete(s.seen, guestSessionID)
	s.mu.Unlock()
}

func (s *Service) ShareTokenForGuest(ctx context.Context, guestSessionID string) string {
	return ""
}
