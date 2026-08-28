package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/session"
	"github.com/viewdock/viewdock/internal/settings"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrDisabled           = errors.New("account disabled")
	ErrSetupComplete      = errors.New("setup already complete")
)

const (
	SessionCookie = "vd_session"
	GuestCookie   = "vd_guest"
	PINIdle       = 15 * time.Minute
)

type User struct {
	ID          string
	Username    string
	DisplayName string
	Email       string
	IsAdmin     bool
	Disabled    bool
	PINHash     string
	HasPassword bool
	Permissions []string
	Roles       []string
}

type Service struct {
	DB       *sql.DB
	Sessions *session.Store
	Settings *settings.Store
	Audit    *audit.Log
	Cfg      config.Config
	Grants   *GrantStore
}

func New(db *sql.DB, cfg config.Config, kv *settings.Store, aud *audit.Log) *Service {
	return &Service{
		DB: db, Sessions: session.New(db), Settings: kv, Audit: aud, Cfg: cfg,
		Grants: NewGrantStore(db),
	}
}

func (s *Service) SetupComplete(ctx context.Context) bool {
	if s.Settings == nil {
		return false
	}
	return s.Settings.Bool(ctx, "setup.complete")
}

func (s *Service) UserCount(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Service) GetUser(ctx context.Context, id string) (User, error) {
	u, err := s.scanUser(s.DB.QueryRowContext(ctx, `
		SELECT id, username, display_name, email, is_admin, disabled, pin_hash, has_password FROM users WHERE id = ?
	`, id))
	if err != nil {
		return u, err
	}
	s.hydrateUser(ctx, &u)
	return u, nil
}

func (s *Service) ByUsername(ctx context.Context, username string) (User, string, error) {
	var u User
	var hash string
	var admin, dis, hp int
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, username, display_name, email, is_admin, disabled, pin_hash, has_password, password_hash
		FROM users WHERE username = ?
	`, username).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &admin, &dis, &u.PINHash, &hp, &hash)
	u.IsAdmin = admin == 1
	u.Disabled = dis == 1
	u.HasPassword = hp == 1
	if err == nil {
		s.hydrateUser(ctx, &u)
	}
	return u, hash, err
}

func (s *Service) scanUser(row *sql.Row) (User, error) {
	var u User
	var admin, dis, hp int
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &admin, &dis, &u.PINHash, &hp)
	u.IsAdmin = admin == 1
	u.Disabled = dis == 1
	u.HasPassword = hp == 1
	return u, err
}

func (s *Service) hydrateUser(ctx context.Context, u *User) {
	u.Permissions = s.PermissionsFor(ctx, u.ID)
	u.Roles = s.RoleNamesFor(ctx, u.ID)
	if !u.IsAdmin {
		for _, p := range u.Permissions {
			if p == PermAdmin {
				u.IsAdmin = true
				break
			}
		}
	}
}

func (s *Service) CreateAdmin(ctx context.Context, username, password, display string) (User, error) {
	n, err := s.UserCount(ctx)
	if err != nil {
		return User{}, err
	}
	if n > 0 {
		return User{}, ErrSetupComplete
	}
	username = strings.TrimSpace(username)
	if username == "" || len(password) < 8 {
		return User{}, errors.New("username required and password must be at least 8 characters")
	}
	if display == "" {
		display = username
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	u := User{ID: uuid.NewString(), Username: strings.TrimSpace(username), DisplayName: display, IsAdmin: true}
	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO users(id, username, password_hash, display_name, email, is_admin, disabled, pin_hash, created_at, updated_at, has_password)
		VALUES (?, ?, ?, ?, '', 1, 0, '', ?, ?, 1)
	`, u.ID, u.Username, hash, u.DisplayName, now, now)
	if err != nil {
		return User{}, err
	}
	_ = s.AssignRole(ctx, u.ID, RoleAdministrator)
	s.hydrateUser(ctx, &u)
	return u, nil
}

func (s *Service) CreateUser(ctx context.Context, username, password, display string, admin bool) (User, error) {
	username = strings.TrimSpace(username)
	if username == "" || len(password) < 8 {
		return User{}, errors.New("username required and password must be at least 8 characters")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	if display == "" {
		display = username
	}
	now := time.Now().UTC().Format(time.RFC3339)
	u := User{ID: uuid.NewString(), Username: strings.TrimSpace(username), DisplayName: display, IsAdmin: admin}
	ai := 0
	if admin {
		ai = 1
	}
	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO users(id, username, password_hash, display_name, email, is_admin, disabled, pin_hash, created_at, updated_at, has_password)
		VALUES (?, ?, ?, ?, '', ?, 0, '', ?, ?, 1)
	`, u.ID, u.Username, hash, u.DisplayName, ai, now, now)
	if err != nil {
		return User{}, err
	}
	if admin {
		_ = s.AssignRole(ctx, u.ID, RoleAdministrator)
	} else {
		_ = s.AssignRole(ctx, u.ID, RoleUser)
	}
	s.hydrateUser(ctx, &u)
	return u, nil
}

func (s *Service) UpdateDisplayName(ctx context.Context, userID, display string) error {
	display = strings.TrimSpace(display)
	if display == "" {
		return errors.New("display name required")
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET display_name = ?, updated_at = ? WHERE id = ?`,
		display, time.Now().UTC().Format(time.RFC3339), userID)
	return err
}

func (s *Service) SetDisabled(ctx context.Context, actorID, userID string, disabled bool) error {
	if actorID == userID && disabled {
		return errors.New("cannot disable your own account")
	}
	if disabled {
		if err := s.guardLastAdmin(ctx, userID, true, nil); err != nil {
			return err
		}
	}
	d := 0
	if disabled {
		d = 1
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET disabled = ?, updated_at = ? WHERE id = ?`,
		d, time.Now().UTC().Format(time.RFC3339), userID)
	if err != nil {
		return err
	}
	if disabled {
		s.Sessions.DeleteAllForUser(ctx, userID)
	}
	return nil
}

func (s *Service) SetPassword(ctx context.Context, userID, next string) error {
	if len(next) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	nh, err := HashPassword(next)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE users SET password_hash = ?, has_password = 1, updated_at = ? WHERE id = ?`,
		nh, time.Now().UTC().Format(time.RFC3339), userID)
	return err
}

func (s *Service) Login(ctx context.Context, username, password, ip, ua string) (raw string, exp time.Time, u User, err error) {
	u, hash, err := s.ByUsername(ctx, username)
	if err != nil {
		return "", time.Time{}, User{}, ErrInvalidCredentials
	}
	if u.Disabled {
		return "", time.Time{}, User{}, ErrDisabled
	}
	if !VerifyPassword(hash, password) {
		return "", time.Time{}, User{}, ErrInvalidCredentials
	}
	raw, exp, err = s.Sessions.Create(ctx, u.ID, ip, ua)
	return raw, exp, u, err
}

func (s *Service) ChangePassword(ctx context.Context, userID, current, next string) error {
	var hash string
	if err := s.DB.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hash); err != nil {
		return err
	}
	var hp int
	_ = s.DB.QueryRowContext(ctx, `SELECT has_password FROM users WHERE id = ?`, userID).Scan(&hp)
	if hp == 1 && !VerifyPassword(hash, current) {
		return ErrInvalidCredentials
	}
	if len(next) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	nh, err := HashPassword(next)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE users SET password_hash = ?, has_password = 1, updated_at = ? WHERE id = ?`, nh, time.Now().UTC().Format(time.RFC3339), userID)
	if err != nil {
		return err
	}
	s.Sessions.DeleteAllForUser(ctx, userID)
	return nil
}

func (s *Service) SetPIN(ctx context.Context, userID, pin string) error {
	if len(pin) < 4 || len(pin) > 8 {
		return errors.New("pin must be 4-8 digits")
	}
	for _, c := range pin {
		if c < '0' || c > '9' {
			return errors.New("pin must be digits")
		}
	}
	h, err := HashPassword(pin)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE users SET pin_hash = ?, updated_at = ? WHERE id = ?`, h, time.Now().UTC().Format(time.RFC3339), userID)
	return err
}

func (s *Service) ClearPIN(ctx context.Context, userID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET pin_hash = '', updated_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), userID)
	return err
}

func (s *Service) VerifyPIN(ctx context.Context, userID, pin string) bool {
	var h string
	if err := s.DB.QueryRowContext(ctx, `SELECT pin_hash FROM users WHERE id = ?`, userID).Scan(&h); err != nil || h == "" {
		return false
	}
	return VerifyPassword(h, pin)
}
