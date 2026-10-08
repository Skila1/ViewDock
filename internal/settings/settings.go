package settings

import (
	"context"
	"database/sql"

	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/secrets"
)

// Store is the application accessor for server_settings (0001).
type Store struct {
	db     db.Queryer
	cipher *secrets.Cipher
}

func New(database db.Queryer) *Store { return &Store{db: database} }

// UseCipher enables transparent decryption of encrypted values and SetSecret.
func (s *Store) UseCipher(c *secrets.Cipher) { s.cipher = c }

func (s *Store) Cipher() *secrets.Cipher { return s.cipher }

func (s *Store) Get(ctx context.Context, key string) (string, error) {
	v, err := s.GetRaw(ctx, key)
	if err != nil || !secrets.IsEncrypted(v) {
		return v, err
	}
	if s.cipher == nil {
		return "", secrets.ErrDecrypt
	}
	return s.cipher.Decrypt(key, v)
}

// GetRaw returns the stored value without decrypting it.
func (s *Store) GetRaw(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM server_settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Store) Set(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO server_settings(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}

// SetSecret stores value encrypted when a cipher is configured.
func (s *Store) SetSecret(ctx context.Context, key, value string) error {
	if s.cipher == nil || value == "" {
		return s.Set(ctx, key, value)
	}
	enc, err := s.cipher.Encrypt(key, value)
	if err != nil {
		return err
	}
	return s.Set(ctx, key, enc)
}

func (s *Store) Bool(ctx context.Context, key string) bool {
	v, _ := s.Get(ctx, key)
	return v == "1" || v == "true"
}
