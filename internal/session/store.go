package session

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

const AbsoluteTTL = 14 * 24 * time.Hour

type Store struct {
	DB *sql.DB
}

func New(db *sql.DB) *Store { return &Store{DB: db} }

type Row struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
	LastSeen  time.Time
}

func (s *Store) Create(ctx context.Context, userID, ip, ua string) (raw string, expires time.Time, err error) {
	raw, err = randomToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	now := time.Now().UTC()
	exp := now.Add(AbsoluteTTL)
	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO sessions(id, user_id, token_hash, expires_at, last_seen_at, created_at, ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, uuid.NewString(), userID, hashToken(raw), exp.Format(time.RFC3339), now.Format(time.RFC3339), now.Format(time.RFC3339), ip, ua)
	return raw, exp, err
}

func (s *Store) Lookup(ctx context.Context, raw string) (Row, error) {
	var r Row
	var exp, seen string
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, user_id, expires_at, last_seen_at FROM sessions WHERE token_hash = ?
	`, hashToken(raw)).Scan(&r.ID, &r.UserID, &exp, &seen)
	if err != nil {
		return Row{}, err
	}
	r.ExpiresAt, _ = time.Parse(time.RFC3339, exp)
	r.LastSeen, _ = time.Parse(time.RFC3339, seen)
	if time.Now().UTC().After(r.ExpiresAt) {
		return Row{}, sql.ErrNoRows
	}
	return r, nil
}

func (s *Store) Touch(ctx context.Context, id string) {
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = s.DB.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id = ?`, now, id)
}

func (s *Store) Delete(ctx context.Context, raw string) {
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(raw))
}

func (s *Store) DeleteAllForUser(ctx context.Context, userID string) {
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
}

type Info struct {
	ID         string `json:"id"`
	IP         string `json:"ip"`
	UserAgent  string `json:"user_agent"`
	CreatedAt  string `json:"created_at"`
	LastSeenAt string `json:"last_seen_at"`
	ExpiresAt  string `json:"expires_at"`
	Current    bool   `json:"current"`
}

func (s *Store) ListForUser(ctx context.Context, userID, currentID string) ([]Info, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, ip, user_agent, created_at, last_seen_at, expires_at
		FROM sessions WHERE user_id = ? ORDER BY last_seen_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Info{}
	for rows.Next() {
		var row Info
		if err := rows.Scan(&row.ID, &row.IP, &row.UserAgent, &row.CreatedAt, &row.LastSeenAt, &row.ExpiresAt); err != nil {
			return nil, err
		}
		row.Current = row.ID == currentID
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) DeleteID(ctx context.Context, userID, sessionID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id = ? AND user_id = ?`, sessionID, userID)
	return err
}
