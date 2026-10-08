package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// ErrDiscordNotLinked means no enabled ViewDock account is linked to a Discord user.
var ErrDiscordNotLinked = errors.New("discord account is not linked to a ViewDock account")

// PrincipalForDiscord returns a principal for the enabled account linked to
// discordUserID through Discord OAuth, for requests authenticated by Discord
// (bot interactions) rather than a ViewDock session. Disabled and expired
// temporary accounts are treated as unlinked.
func (s *Service) PrincipalForDiscord(ctx context.Context, discordUserID string) (*Principal, error) {
	discordUserID = strings.TrimSpace(discordUserID)
	if discordUserID == "" {
		return nil, ErrDiscordNotLinked
	}
	u, err := s.userByDiscord(ctx, discordUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDiscordNotLinked
	}
	if err != nil {
		return nil, err
	}
	if u.Disabled || u.TemporaryExpired() {
		return nil, ErrDiscordNotLinked
	}
	return &Principal{
		Kind: KindUser, UserID: u.ID, IsAdmin: u.IsAdmin,
		DisplayName: u.DisplayName, Username: u.Username,
		Permissions: u.Permissions, Roles: u.Roles,
		Temporary: u.Temporary, PartyOnly: u.PartyOnly,
	}, nil
}
