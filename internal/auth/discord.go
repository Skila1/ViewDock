package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

type DiscordProfile struct {
	ID     string `json:"id"`
	Username string `json:"username"`
	Global string `json:"global_name"`
	Avatar string `json:"avatar"`
}

type DiscordOAuthConfig struct {
	LoginEnabled         bool   `json:"login_enabled"`
	ClientID             string `json:"client_id"`
	Secret               string `json:"-"`
	ClientSecretSet      bool   `json:"client_secret_set"`
	RegistrationEnabled  bool   `json:"registration_enabled"`
	AdminDiscordIDs      string `json:"admin_discord_ids"`
	RedirectURI          string `json:"redirect_uri"`
}

func (c DiscordOAuthConfig) Ready() bool {
	return c.LoginEnabled && c.ClientID != "" && c.Secret != ""
}

func (s *Service) LoadDiscord(ctx context.Context) DiscordOAuthConfig {
	var login, reg int
	var clientID, secret, admins string
	_ = s.DB.QueryRowContext(ctx, `
		SELECT login_enabled, client_id, client_secret, registration_enabled, admin_discord_ids
		FROM discord_settings WHERE id = 1
	`).Scan(&login, &clientID, &secret, &reg, &admins)
	return DiscordOAuthConfig{
		LoginEnabled:        login == 1,
		ClientID:            strings.TrimSpace(clientID),
		Secret:              secret,
		ClientSecretSet:     strings.TrimSpace(secret) != "",
		RegistrationEnabled: reg == 1,
		AdminDiscordIDs:     admins,
	}
}

func (s *Service) SaveDiscord(ctx context.Context, cfg DiscordOAuthConfig, updateSecret bool) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if updateSecret {
		_, err := s.DB.ExecContext(ctx, `
			INSERT INTO discord_settings(id, login_enabled, client_id, client_secret, registration_enabled, admin_discord_ids, updated_at)
			VALUES (1, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				login_enabled=excluded.login_enabled,
				client_id=excluded.client_id,
				client_secret=excluded.client_secret,
				registration_enabled=excluded.registration_enabled,
				admin_discord_ids=excluded.admin_discord_ids,
				updated_at=excluded.updated_at
		`, boolInt(cfg.LoginEnabled), strings.TrimSpace(cfg.ClientID), cfg.Secret,
			boolInt(cfg.RegistrationEnabled), strings.TrimSpace(cfg.AdminDiscordIDs), now)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO discord_settings(id, login_enabled, client_id, client_secret, registration_enabled, admin_discord_ids, updated_at)
		VALUES (1, ?, ?, '', ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			login_enabled=excluded.login_enabled,
			client_id=excluded.client_id,
			registration_enabled=excluded.registration_enabled,
			admin_discord_ids=excluded.admin_discord_ids,
			updated_at=excluded.updated_at
	`, boolInt(cfg.LoginEnabled), strings.TrimSpace(cfg.ClientID),
		boolInt(cfg.RegistrationEnabled), strings.TrimSpace(cfg.AdminDiscordIDs), now)
	return err
}

func (s *Service) SyncDiscordEnv() {
	id := strings.TrimSpace(os.Getenv("VD_DISCORD_CLIENT_ID"))
	sec := os.Getenv("VD_DISCORD_CLIENT_SECRET")
	if id == "" && sec == "" {
		return
	}
	ctx := context.Background()
	cur := s.LoadDiscord(ctx)
	if id != "" {
		cur.ClientID = id
	}
	updateSecret := sec != ""
	if updateSecret {
		cur.Secret = sec
	}
	if os.Getenv("VD_DISCORD_LOGIN") == "1" || strings.EqualFold(os.Getenv("VD_DISCORD_LOGIN"), "true") {
		cur.LoginEnabled = true
	}
	_ = s.SaveDiscord(ctx, cur, updateSecret)
}

func pkce() (verifier, challenge string) {
	b := make([]byte, 32)
	tok, _ := RandomToken(32)
	if tok != "" {
		verifier = tok
	} else {
		verifier = base64.RawURLEncoding.EncodeToString(b)
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return
}

func (s *Service) StoreLoginState(ctx context.Context, state, verifier, linkUserID string) error {
	exp := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO login_states(state, provider, code_verifier, link_user_id, expires_at)
		VALUES (?, 'discord', ?, ?, ?)
	`, state, verifier, linkUserID, exp)
	return err
}

func (s *Service) TakeLoginState(ctx context.Context, state string) (verifier, linkUserID string, err error) {
	var exp string
	err = s.DB.QueryRowContext(ctx, `
		SELECT code_verifier, link_user_id, expires_at FROM login_states WHERE state = ?
	`, state).Scan(&verifier, &linkUserID, &exp)
	if err != nil {
		return "", "", err
	}
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM login_states WHERE state = ?`, state)
	if t, e := time.Parse(time.RFC3339, exp); e == nil && time.Now().UTC().After(t) {
		return "", "", errors.New("expired")
	}
	return verifier, linkUserID, nil
}

func discordAuthURL(clientID, redirect, state, challenge string) string {
	q := url.Values{
		"client_id":             {clientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirect},
		"scope":                 {"identify"},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"prompt":                {"consent"},
	}
	return "https://discord.com/oauth2/authorize?" + q.Encode()
}

func exchangeDiscordCode(ctx context.Context, clientID, secret, redirect, code, verifier string) (DiscordProfile, error) {
	var prof DiscordProfile
	form := url.Values{
		"client_id": {clientID}, "client_secret": {secret}, "grant_type": {"authorization_code"},
		"code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier},
	}
	cli := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://discord.com/api/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return prof, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := cli.Do(req)
	if err != nil {
		return prof, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return prof, fmt.Errorf("discord token: %s", strings.TrimSpace(string(b)))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(b, &tok); err != nil || tok.AccessToken == "" {
		return prof, errors.New("discord token missing")
	}
	ureq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://discord.com/api/users/@me", nil)
	if err != nil {
		return prof, err
	}
	ureq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	uresp, err := cli.Do(ureq)
	if err != nil {
		return prof, err
	}
	defer uresp.Body.Close()
	ub, _ := io.ReadAll(io.LimitReader(uresp.Body, 1<<20))
	if uresp.StatusCode >= 400 {
		return prof, fmt.Errorf("discord profile: %s", strings.TrimSpace(string(ub)))
	}
	if err := json.Unmarshal(ub, &prof); err != nil || prof.ID == "" {
		return prof, errors.New("discord profile missing")
	}
	return prof, nil
}

func discordDisplay(p DiscordProfile) string {
	if g := strings.TrimSpace(p.Global); g != "" {
		return g
	}
	if u := strings.TrimSpace(p.Username); u != "" {
		return u
	}
	return p.ID
}

func splitIDs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func isAdminDiscordID(id string, raw string) bool {
	for _, a := range splitIDs(raw) {
		if a == id {
			return true
		}
	}
	return false
}

func (s *Service) DiscordUserID(ctx context.Context, userID string) string {
	var id string
	_ = s.DB.QueryRowContext(ctx, `
		SELECT provider_user_id FROM user_identities WHERE user_id = ? AND provider = 'discord'
	`, userID).Scan(&id)
	return id
}

func (s *Service) ListIdentities(ctx context.Context, userID string) ([]map[string]any, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT provider, provider_user_id, provider_username, avatar_hash, linked_at
		FROM user_identities WHERE user_id = ?
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var prov, pid, pun, av, at string
		if err := rows.Scan(&prov, &pid, &pun, &av, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"provider": prov, "provider_user_id": pid, "provider_username": pun,
			"avatar_hash": av, "linked_at": at,
		})
	}
	return out, rows.Err()
}

func (s *Service) UnlinkDiscord(ctx context.Context, userID string) error {
	var hp int
	_ = s.DB.QueryRowContext(ctx, `SELECT has_password FROM users WHERE id = ?`, userID).Scan(&hp)
	if hp != 1 {
		return errors.New("set a password before unlinking Discord")
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM user_identities WHERE user_id = ? AND provider = 'discord'`, userID)
	return err
}

func (s *Service) linkDiscord(ctx context.Context, userID string, p DiscordProfile) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO user_identities(user_id, provider, provider_user_id, provider_username, avatar_hash, linked_at)
		VALUES (?, 'discord', ?, ?, ?, ?)
		ON CONFLICT(user_id, provider) DO UPDATE SET
			provider_user_id=excluded.provider_user_id,
			provider_username=excluded.provider_username,
			avatar_hash=excluded.avatar_hash
	`, userID, p.ID, p.Username, p.Avatar, now)
	return err
}

func (s *Service) userByDiscord(ctx context.Context, discordID string) (User, error) {
	var userID string
	err := s.DB.QueryRowContext(ctx, `
		SELECT user_id FROM user_identities WHERE provider = 'discord' AND provider_user_id = ?
	`, discordID).Scan(&userID)
	if err != nil {
		return User{}, err
	}
	return s.GetUser(ctx, userID)
}

func (s *Service) UpsertDiscordUser(ctx context.Context, p DiscordProfile) (User, error) {
	if u, err := s.userByDiscord(ctx, p.ID); err == nil {
		return u, nil
	}
	cfg := s.LoadDiscord(ctx)
	adminListed := isAdminDiscordID(p.ID, cfg.AdminDiscordIDs)
	if !cfg.RegistrationEnabled && !adminListed {
		return User{}, errors.New("discord registration is disabled")
	}

	display := discordDisplay(p)
	username := strings.TrimSpace(p.Username)
	if username == "" {
		username = "discord_" + p.ID
	}
	hash, err := HashPassword(uuid.NewString() + uuid.NewString())
	if err != nil {
		return User{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	u := User{ID: uuid.NewString(), Username: username, DisplayName: display, HasPassword: false}
	ai := 0
	if adminListed {
		ai = 1
		u.IsAdmin = true
	}
	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO users(id, username, password_hash, display_name, email, is_admin, disabled, pin_hash, created_at, updated_at, has_password)
		VALUES (?, ?, ?, ?, '', ?, 0, '', ?, ?, 0)
	`, u.ID, u.Username, hash, u.DisplayName, ai, now, now)
	if err != nil {
		u.Username = "discord_" + p.ID
		_, err = s.DB.ExecContext(ctx, `
			INSERT INTO users(id, username, password_hash, display_name, email, is_admin, disabled, pin_hash, created_at, updated_at, has_password)
			VALUES (?, ?, ?, ?, '', ?, 0, '', ?, ?, 0)
		`, u.ID, u.Username, hash, u.DisplayName, ai, now, now)
		if err != nil {
			return User{}, err
		}
	}
	if adminListed {
		_ = s.AssignRole(ctx, u.ID, RoleAdministrator)
	} else {
		_ = s.AssignRole(ctx, u.ID, RoleUser)
	}
	if err := s.linkDiscord(ctx, u.ID, p); err != nil {
		return User{}, err
	}
	return s.GetUser(ctx, u.ID)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
