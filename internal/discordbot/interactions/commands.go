package interactions

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/discordbot"
)

// CommandName is the single top-level slash command; actions are subcommands.
const CommandName = "party"

// Commands is the command set registered with Discord.
func Commands() []discordbot.Command {
	voice := []int{discordbot.ChannelGuildVoice, discordbot.ChannelGuildStage}
	return []discordbot.Command{{
		Type:        1,
		Name:        CommandName,
		Description: "ViewDock watch parties",
		Contexts:    []int{discordbot.ContextGuild},
		Options: []discordbot.CommandOption{
			{Type: discordbot.OptionSubCommand, Name: "create", Description: "Start a watch party for a title and link it to this channel",
				Options: []discordbot.CommandOption{{Type: discordbot.OptionString, Name: "title", Description: "Title to watch", Required: true, Autocomplete: true, MaxLength: 100}}},
			{Type: discordbot.OptionSubCommand, Name: "invite", Description: "Post the invite link for the party linked to this channel"},
			{Type: discordbot.OptionSubCommand, Name: "status", Description: "Show the state of the party linked to this channel"},
			{Type: discordbot.OptionSubCommand, Name: "pause", Description: "Pause the party (host only)"},
			{Type: discordbot.OptionSubCommand, Name: "resume", Description: "Resume the party (host only)"},
			{Type: discordbot.OptionSubCommand, Name: "link-voice", Description: "Link a voice channel to a party (host only)",
				Options: []discordbot.CommandOption{
					{Type: discordbot.OptionChannel, Name: "channel", Description: "Voice channel", Required: true, ChannelTypes: voice},
					{Type: discordbot.OptionString, Name: "invite", Description: "Invite link or code (defaults to the party linked here)", MaxLength: 300},
				}},
			{Type: discordbot.OptionSubCommand, Name: "unlink", Description: "Remove a channel's party link",
				Options: []discordbot.CommandOption{{Type: discordbot.OptionChannel, Name: "channel", Description: "Channel to unlink (defaults to this one)"}}},
		},
	}}
}

var (
	itemKinds  = map[string]bool{"movie": true, "series": true, "episode": true}
	itemIDRe   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,80}$`)
	inviteCode = regexp.MustCompile(`^[A-Za-z0-9_-]{4,64}$`)
)

func subcommand(in *interaction) (string, map[string]option) {
	if in.Data == nil || in.Data.Name != CommandName || len(in.Data.Options) == 0 || in.Data.Options[0].Type != discordbot.OptionSubCommand {
		return "", nil
	}
	sub := in.Data.Options[0]
	opts := make(map[string]option, len(sub.Options))
	for _, o := range sub.Options {
		opts[o.Name] = o
	}
	return sub.Name, opts
}

func (s *Service) linkHint(ctx context.Context) string {
	if base := s.publicBase(ctx); base != "" {
		return "Link your Discord account to ViewDock first: " + base + "/settings/connected"
	}
	return "Link your Discord account to ViewDock first under Settings, Connected accounts."
}

// principal resolves the linked account, returning a reply when there is none.
func (s *Service) principal(ctx context.Context, userID string) (*auth.Principal, *response) {
	if s.Accounts == nil {
		r := private("Discord account linking is not available on this server.")
		return nil, &r
	}
	p, err := s.Accounts.PrincipalForDiscord(ctx, userID)
	if errors.Is(err, ErrNotLinked) || (err == nil && (p == nil || !p.IsUser())) {
		r := private(s.linkHint(ctx))
		return nil, &r
	}
	if err != nil {
		r := private(s.errText(ctx, "account", err))
		return nil, &r
	}
	return p, nil
}

func (s *Service) command(ctx context.Context, in *interaction, userID string) response {
	name, opts := subcommand(in)
	if name == "" {
		return private("Unknown command.")
	}
	if !s.partiesEnabled() {
		return private("Watch parties are turned off on this ViewDock server.")
	}
	p, reply := s.principal(ctx, userID)
	if reply != nil {
		return *reply
	}
	switch name {
	case "create":
		return s.create(ctx, in, p, userID, opts["title"].str())
	case "invite":
		return s.invite(ctx, in)
	case "status":
		return s.status(ctx, in, p)
	case ActionPause, ActionResume:
		link, st, err := s.linkedRoom(ctx, in.ChannelID)
		if err != nil {
			return private(s.errText(ctx, name, err))
		}
		return s.control(ctx, p, link.RoomID, link.Title, st, name)
	case "link-voice":
		return s.linkVoice(ctx, in, p, userID, opts["channel"].str(), opts["invite"].str())
	case "unlink":
		return s.unlink(ctx, in, p, opts["channel"].str())
	}
	return private("Unknown command.")
}

// pickTitle resolves an autocomplete value ("kind:id") or free text.
func (s *Service) pickTitle(ctx context.Context, p *auth.Principal, raw string) (Title, error) {
	raw = strings.TrimSpace(raw)
	if kind, id, ok := strings.Cut(raw, ":"); ok && itemKinds[kind] && itemIDRe.MatchString(id) {
		name, err := s.Catalog.TitleOf(ctx, p, kind, id)
		if err != nil {
			return Title{}, err
		}
		return Title{Kind: kind, ID: id, Name: name}, nil
	}
	if len(raw) < 2 {
		return Title{}, errNoMatch
	}
	hits, err := s.Catalog.SearchTitles(ctx, p, raw, 5)
	if err != nil {
		return Title{}, err
	}
	for _, h := range hits {
		if strings.EqualFold(h.Name, raw) {
			return h, nil
		}
	}
	if len(hits) == 0 {
		return Title{}, errNoMatch
	}
	return hits[0], nil
}

var errNoMatch = errors.New("no matching title")

func (s *Service) create(ctx context.Context, in *interaction, p *auth.Principal, userID, raw string) response {
	if s.Catalog == nil || s.Parties == nil {
		return private("Watch parties are not available on this server.")
	}
	if p.PartyOnly {
		return private("Your ViewDock account can join parties but cannot start them.")
	}
	t, err := s.pickTitle(ctx, p, raw)
	if errors.Is(err, errNoMatch) {
		return private("No title you can watch matched that search.")
	}
	if err != nil {
		return private(s.errText(ctx, "create", err))
	}
	roomID, code, err := s.Parties.Create(ctx, p, t.Kind, t.ID)
	if err != nil {
		return private(s.errText(ctx, "create", err))
	}
	link := Link{ChannelID: in.ChannelID, GuildID: in.GuildID, Kind: LinkText, RoomID: roomID, InviteCode: code, Title: t.Name, LinkedBy: p.UserID, DiscordUserID: userID}
	if err := s.Links.Put(ctx, link); err != nil {
		s.Log.Warn("discord channel link failed", "category", "discord", "err", err)
	}
	s.audit(ctx, p.UserID, "discord.party_create", roomID, "channel="+in.ChannelID)
	u := s.inviteURL(ctx, code)
	if u == "" {
		return private(fmt.Sprintf("Party created (invite code %s), but this server has no public URL set, so no link can be posted. An administrator can set it under Admin, Settings.", code))
	}
	text := fmt.Sprintf("**%s** watch party hosted by %s\n%s\nJoin in ViewDock and talk here in Discord.", discordbot.EscapeMarkdown(t.Name), discordbot.EscapeMarkdown(displayName(p)), u)
	return message(text, false, joinRow(u)...)
}

func joinRow(u string) []actionRow {
	if !strings.HasPrefix(u, "https://") {
		return nil
	}
	return []actionRow{{Type: 1, Components: []button{{Type: 2, Style: 5, Label: "Join party", URL: u}}}}
}

func displayName(p *auth.Principal) string {
	if p.DisplayName != "" {
		return p.DisplayName
	}
	if p.Username != "" {
		return p.Username
	}
	return "a ViewDock user"
}

// linkedRoom loads the channel link and live room state, dropping links to
// rooms that no longer exist.
func (s *Service) linkedRoom(ctx context.Context, channelID string) (Link, map[string]any, error) {
	if s.Parties == nil {
		return Link{}, nil, ErrRoomNotFound
	}
	link, err := s.Links.Get(ctx, channelID)
	if err != nil {
		return Link{}, nil, err
	}
	st := s.Parties.State(link.RoomID)
	if st == nil {
		_ = s.Links.DeleteRoom(ctx, link.RoomID)
		return Link{}, nil, ErrRoomNotFound
	}
	return link, st, nil
}

func (s *Service) invite(ctx context.Context, in *interaction) response {
	link, _, err := s.linkedRoom(ctx, in.ChannelID)
	if errors.Is(err, ErrNoLink) {
		return private("No watch party is linked to this channel. Start one with /party create.")
	}
	if err != nil {
		return private(s.errText(ctx, "invite", err))
	}
	u := s.inviteURL(ctx, link.InviteCode)
	if u == "" {
		return private("This server has no public URL set, so no invite link can be posted.")
	}
	return message(fmt.Sprintf("Join the **%s** watch party:\n%s", discordbot.EscapeMarkdown(orTitle(link.Title)), u), false, joinRow(u)...)
}

func orTitle(t string) string {
	if strings.TrimSpace(t) == "" {
		return "ViewDock"
	}
	return t
}

func (s *Service) status(ctx context.Context, in *interaction, p *auth.Principal) response {
	link, st, err := s.linkedRoom(ctx, in.ChannelID)
	if errors.Is(err, ErrNoLink) {
		return private("No watch party is linked to this channel. Start one with /party create.")
	}
	if err != nil {
		return private(s.errText(ctx, "status", err))
	}
	host := stateString(st, "host")
	members := stateMembers(st)
	hostName := "unknown"
	var names []string
	for _, m := range members {
		if m.id == host {
			hostName = m.name
		}
		if len(names) < 10 {
			names = append(names, discordbot.EscapeMarkdown(m.name))
		}
	}
	state := "Paused"
	if stateBool(st, "playing") {
		state = "Playing"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%s**\n%s at %s, hosted by %s\n", discordbot.EscapeMarkdown(orTitle(link.Title)), state, formatPosition(stateInt(st, "position_ms")), discordbot.EscapeMarkdown(hostName))
	fmt.Fprintf(&b, "%d watching", len(members))
	if len(names) > 0 {
		b.WriteString(": " + strings.Join(names, ", "))
		if len(members) > len(names) {
			fmt.Fprintf(&b, " and %d more", len(members)-len(names))
		}
	}
	if u := s.inviteURL(ctx, link.InviteCode); u != "" {
		b.WriteString("\n" + u)
	}
	var rows []actionRow
	if hosts(st, p) {
		rows = []actionRow{{Type: 1, Components: []button{
			{Type: 2, Style: 2, Label: "Pause", CustomID: "party:pause:" + link.RoomID},
			{Type: 2, Style: 1, Label: "Resume", CustomID: "party:resume:" + link.RoomID},
		}}}
	}
	return message(b.String(), true, rows...)
}

func (s *Service) control(ctx context.Context, p *auth.Principal, roomID, title string, st map[string]any, action string) response {
	// The adapter enforces host-only control too; this check keeps the
	// contract even if an adapter forgets.
	if !hosts(st, p) {
		return private("Only the party host can do that.")
	}
	if err := s.Parties.Control(ctx, p, roomID, action); err != nil {
		return private(s.errText(ctx, action, err))
	}
	s.audit(ctx, p.UserID, "discord.party_"+action, roomID, "")
	verb := "Paused"
	if action == ActionResume {
		verb = "Resumed"
	}
	pos := formatPosition(stateInt(st, "position_ms"))
	if after := s.Parties.State(roomID); after != nil {
		pos = formatPosition(stateInt(after, "position_ms"))
	}
	return message(fmt.Sprintf("%s **%s** at %s (by %s).", verb, discordbot.EscapeMarkdown(orTitle(title)), pos, discordbot.EscapeMarkdown(displayName(p))), false)
}

func (s *Service) component(ctx context.Context, in *interaction, userID string) response {
	parts := strings.Split(in.Data.CustomID, ":")
	if len(parts) != 3 || parts[0] != "party" || (parts[1] != ActionPause && parts[1] != ActionResume) || parts[2] == "" {
		return private("That button is no longer supported.")
	}
	if !s.partiesEnabled() || s.Parties == nil {
		return private("Watch parties are turned off on this ViewDock server.")
	}
	p, reply := s.principal(ctx, userID)
	if reply != nil {
		return *reply
	}
	st := s.Parties.State(parts[2])
	if st == nil {
		return private("That watch party has ended.")
	}
	title := ""
	if link, err := s.Links.Get(ctx, in.ChannelID); err == nil && link.RoomID == parts[2] {
		title = link.Title
	}
	return s.control(ctx, p, parts[2], title, st, parts[1])
}

// parseInvite accepts a bare invite code or a ViewDock invite URL.
func parseInvite(raw string) string {
	raw = strings.TrimSpace(raw)
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		if i := strings.LastIndex(u.Path, "/together/"); i >= 0 {
			raw = strings.Trim(u.Path[i+len("/together/"):], "/")
		} else {
			return ""
		}
	}
	if !inviteCode.MatchString(raw) {
		return ""
	}
	return raw
}

func (s *Service) linkVoice(ctx context.Context, in *interaction, p *auth.Principal, userID, channelID, invite string) response {
	if !discordbot.ValidSnowflake(channelID) {
		return private("Pick a voice channel.")
	}
	if s.Parties == nil {
		return private("Watch parties are not available on this server.")
	}
	var roomID, code, title string
	if strings.TrimSpace(invite) != "" {
		code = parseInvite(invite)
		id, ok := "", false
		if code != "" {
			id, ok = s.Parties.Resolve(code)
		}
		if !ok {
			return private("That invite link or code does not match an active party.")
		}
		roomID = id
	} else {
		link, _, err := s.linkedRoom(ctx, in.ChannelID)
		if errors.Is(err, ErrNoLink) {
			return private("No party is linked to this channel. Pass an invite link, or run this where the party was created.")
		}
		if err != nil {
			return private(s.errText(ctx, "link-voice", err))
		}
		roomID, code, title = link.RoomID, link.InviteCode, link.Title
	}
	st := s.Parties.State(roomID)
	if st == nil {
		return private("That watch party has ended.")
	}
	if !hosts(st, p) {
		return private("Only the party host can link a voice channel.")
	}
	if title == "" && s.Catalog != nil {
		if t, err := s.Catalog.TitleOf(ctx, p, stateString(st, "item_kind"), stateString(st, "item_id")); err == nil {
			title = t
		}
	}
	if err := s.Links.Put(ctx, Link{ChannelID: channelID, GuildID: in.GuildID, Kind: LinkVoice, RoomID: roomID, InviteCode: code, Title: title, LinkedBy: p.UserID, DiscordUserID: userID}); err != nil {
		return private(s.errText(ctx, "link-voice", err))
	}
	s.audit(ctx, p.UserID, "discord.voice_link", roomID, "channel="+channelID)
	return message(fmt.Sprintf("<#%s> is now linked to the **%s** watch party. Talk there and watch in ViewDock; /party commands in that channel control this party.", channelID, discordbot.EscapeMarkdown(orTitle(title))), false)
}

func (s *Service) unlink(ctx context.Context, in *interaction, p *auth.Principal, channelID string) response {
	if channelID == "" {
		channelID = in.ChannelID
	}
	if !discordbot.ValidSnowflake(channelID) {
		return private("Pick a channel.")
	}
	link, err := s.Links.Get(ctx, channelID)
	if errors.Is(err, ErrNoLink) {
		return private("That channel has no party link.")
	}
	if err != nil {
		return private(s.errText(ctx, "unlink", err))
	}
	isHost := false
	if s.Parties != nil {
		if st := s.Parties.State(link.RoomID); st != nil {
			isHost = hosts(st, p)
		}
	}
	if p.UserID != link.LinkedBy && !isHost && !p.IsAdmin {
		return private("Only the party host, the person who linked the channel, or a ViewDock administrator can unlink it.")
	}
	if _, err := s.Links.Delete(ctx, channelID); err != nil {
		return private(s.errText(ctx, "unlink", err))
	}
	s.audit(ctx, p.UserID, "discord.channel_unlink", link.RoomID, "channel="+channelID)
	return private(fmt.Sprintf("<#%s> is no longer linked to a watch party.", channelID))
}

func (s *Service) autocomplete(ctx context.Context, in *interaction, userID string) response {
	name, opts := subcommand(in)
	o, ok := opts["title"]
	if name != "create" || !ok || !o.Focused || s.Catalog == nil || s.Accounts == nil || !s.partiesEnabled() {
		return choices(nil)
	}
	q := strings.TrimSpace(o.str())
	if len(q) < 2 {
		return choices(nil)
	}
	p, err := s.Accounts.PrincipalForDiscord(ctx, userID)
	if err != nil || p == nil || !p.IsUser() || p.PartyOnly {
		return choices(nil)
	}
	hits, err := s.Catalog.SearchTitles(ctx, p, q, 25)
	if err != nil {
		if ctx.Err() == nil {
			s.Log.Warn("discord autocomplete search failed", "category", "discord", "err", err)
		}
		return choices(nil)
	}
	out := make([]choice, 0, len(hits))
	for _, h := range hits {
		value := h.Kind + ":" + h.ID
		if !itemKinds[h.Kind] || !itemIDRe.MatchString(h.ID) || len(value) > 100 {
			continue
		}
		label := h.Name
		if h.Year != "" {
			label += " (" + h.Year + ")"
		}
		if h.Kind != "movie" {
			label += " [" + h.Kind + "]"
		}
		out = append(out, choice{Name: clip(label, 100), Value: value})
		if len(out) == 25 {
			break
		}
	}
	return choices(out)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}

func formatPosition(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	sec := ms / 1000
	h, m, s := sec/3600, (sec/60)%60, sec%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func stateString(st map[string]any, key string) string {
	v, _ := st[key].(string)
	return v
}

// hosts reports whether p is the current host or the owner (creator) of the
// room; the owner takes host back when acting on the room.
func hosts(st map[string]any, p *auth.Principal) bool {
	id := p.ID()
	if id == "" {
		return false
	}
	return stateString(st, "host") == id || stateString(st, "owner") == id
}

func stateBool(st map[string]any, key string) bool {
	v, _ := st[key].(bool)
	return v
}

func stateInt(st map[string]any, key string) int64 {
	switch v := st[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

type member struct{ id, name string }

func stateMembers(st map[string]any) []member {
	var raw []map[string]any
	switch v := st["members"].(type) {
	case []map[string]any:
		raw = v
	case []any:
		for _, x := range v {
			if m, ok := x.(map[string]any); ok {
				raw = append(raw, m)
			}
		}
	}
	out := make([]member, 0, len(raw))
	for _, m := range raw {
		id, _ := m["id"].(string)
		name, _ := m["display_name"].(string)
		if name == "" {
			name = "Guest"
		}
		out = append(out, member{id: id, name: name})
	}
	return out
}
