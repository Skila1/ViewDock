package main

import (
	"os"
	"strconv"

	"github.com/viewdock/viewdock/internal/bandwidth"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/download"
	"github.com/viewdock/viewdock/internal/runtimecfg"
)

const (
	cfgPublicURL         = "app.public_url"
	cfgTMDBKey           = "tmdb.api_key"
	cfgDiscordBot        = "discord.bot_token"
	cfgTranscodeSlots    = "playback.transcode_slots"
	cfgLogRetention      = "logs.retention_days"
	cfgWatchTogether     = "features.watch_together"
	cfgDownloads         = "features.downloads"
	cfgHardDrift         = "sync.hard_drift_ms"
	cfgGuestHours        = "guests.max_hours"
	cfgMeshPlayback      = "mesh.route_playback"
	cfgDiscordPublicKey  = "discord.public_key"
	cfgDiscordSeparate   = "discord.bot.separate"
	cfgDiscordSepToken   = "discord.bot.separate_token"
	cfgDiscordSepKey     = "discord.bot.separate_public_key"
	cfgBlockUnrated      = "content.block_unrated"
	cfgCertCountry       = "metadata.certification_country"
	cfgOfflineMaxItems   = "offline.max_items"
	cfgOfflineExpiryDays = "offline.expiry_days"
	cfgOfflineMaxItemGB  = "offline.max_item_gb"
	cfgFlightRetention   = "diagnostics.flight_retention_hours"
	cfgTelemetryBudget   = "diagnostics.telemetry_events_per_minute"
)

// discordBotKeys returns the runtime keys of the bot token and public key in
// use. The shared keys belong to the sign-in application; the separate keys
// are kept, unused, while the separate configuration is off.
func discordBotKeys(rc *runtimecfg.Service) (token, publicKey string) {
	if rc.Bool(cfgDiscordSeparate) {
		return cfgDiscordSepToken, cfgDiscordSepKey
	}
	return cfgDiscordBot, cfgDiscordPublicKey
}

// discordBotToken returns the bot token in use. getenv supplies the
// VD_DISCORD_BOT_TOKEN fallback for the shared token when the runtime
// configuration could not provide it.
func discordBotToken(rc *runtimecfg.Service, getenv func(string) string) string {
	key, _ := discordBotKeys(rc)
	if v := rc.String(key); v != "" || key != cfgDiscordBot {
		return v
	}
	return getenv("VD_DISCORD_BOT_TOKEN")
}

func configDefs(cfg config.Config) []runtimecfg.Def {
	return []runtimecfg.Def{
		{Key: cfgPublicURL, Label: "Public URL", Category: "General", Kind: runtimecfg.KindURL,
			Help: "Used for Discord sign-in, share links and invites.", Env: func() string { return cfg.PublicURL }},
		{Key: cfgTMDBKey, Label: "TMDB API key", Category: "Metadata", Kind: runtimecfg.KindSecret,
			Help: "Optional metadata and artwork provider.", Env: func() string { return cfg.TMDBAPIKey }},
		{Key: cfgDiscordBot, Label: "Discord bot token", Category: "Discord", Kind: runtimecfg.KindSecret,
			Help: "Bot token of the Discord application used for sign-in. Enables the official bot: party invites, slash commands and command registration.",
			Env:  func() string { return os.Getenv("VD_DISCORD_BOT_TOKEN") }},
		{Key: cfgDiscordPublicKey, Label: "Discord application public key", Category: "Discord", Kind: runtimecfg.KindSecret,
			Help: "Hex Ed25519 public key of the sign-in application (Developer Portal, General Information). Used to verify interaction requests.",
			Env:  func() string { return os.Getenv("VD_DISCORD_PUBLIC_KEY") }},
		{Key: cfgDiscordSeparate, Label: "Use separate Discord bot configuration", Category: "Discord", Kind: runtimecfg.KindBool, Default: "0",
			Help: "Off: the official bot uses the bot token and public key of the sign-in application. On: it uses the separate bot token and public key instead."},
		{Key: cfgDiscordSepToken, Label: "Separate Discord bot token", Category: "Discord", Kind: runtimecfg.KindSecret,
			Help: "Bot token of a separate Discord application. Used only when the separate bot configuration is on."},
		{Key: cfgDiscordSepKey, Label: "Separate Discord bot public key", Category: "Discord", Kind: runtimecfg.KindSecret,
			Help: "Hex Ed25519 public key of the separate bot application. Used only when the separate bot configuration is on."},
		{Key: cfgTranscodeSlots, Label: "Concurrent transcodes", Category: "Playback", Kind: runtimecfg.KindInt,
			Default: strconv.Itoa(bandwidth.DefaultSlots), Min: 1, Max: 64,
			Help: "Simultaneous FFmpeg transcodes on this node. New sessions beyond the limit are refused."},
		{Key: cfgMeshPlayback, Label: "Play on registered media workers", Category: "Playback", Kind: runtimecfg.KindBool, Default: "0",
			Help: "Place new playback sessions on healthy registered workers instead of this server. Always on when VD_ROLE=control."},
		{Key: cfgDownloads, Label: "Downloads and Offline Vault", Category: "Features", Kind: runtimecfg.KindBool, Default: "1"},
		{Key: cfgOfflineMaxItems, Label: "Offline titles per account per device", Category: "Features", Kind: runtimecfg.KindInt,
			Default: strconv.Itoa(download.DefaultMaxItems), Min: 1, Max: download.MaxItemsLimit,
			Help: "Most titles one account may keep in the Offline Vault on a single device."},
		{Key: cfgOfflineExpiryDays, Label: "Offline download expiry (days)", Category: "Features", Kind: runtimecfg.KindInt,
			Default: strconv.Itoa(download.DefaultExpiryDays), Min: 0, Max: download.MaxExpiryDays,
			Help: "Days after download before an offline title is removed. 0 keeps titles until the user removes them. Shortening this also shortens existing downloads the next time the device is online."},
		{Key: cfgOfflineMaxItemGB, Label: "Largest offline title (GB)", Category: "Features", Kind: runtimecfg.KindInt,
			Default: "0", Min: 0, Max: 1024,
			Help: "Titles larger than this cannot be saved offline. 0 means no limit beyond device storage."},
		{Key: cfgBlockUnrated, Label: "Hide unrated titles from restricted viewers", Category: "Accounts", Kind: runtimecfg.KindBool,
			Default: "1", Help: "Applies only to accounts with a content restriction. Turn off to show titles that have no rating."},
		{Key: cfgCertCountry, Label: "Content rating country", Category: "Metadata", Kind: runtimecfg.KindString,
			Default: "US", Help: "Two-letter ISO country code whose TMDB certification becomes the title rating. US is the fallback."},
		{Key: cfgWatchTogether, Label: "Watch parties", Category: "Features", Kind: runtimecfg.KindBool, Default: "1"},
		{Key: cfgHardDrift, Label: "Party hard resync threshold (ms)", Category: "Watch parties", Kind: runtimecfg.KindInt,
			Default: "1000", Min: 250, Max: 10000, Help: "Members further than this from the room timeline seek to it. Smaller drift above 250 ms is corrected by adjusting playback speed."},
		{Key: cfgGuestHours, Label: "Longest guest account lifetime (hours)", Category: "Accounts", Kind: runtimecfg.KindInt,
			Default: "720", Min: 1, Max: 720},
		{Key: cfgLogRetention, Label: "Operational log retention (days)", Category: "Operations", Kind: runtimecfg.KindInt,
			Default: "14", Min: 1, Max: 365},
		{Key: cfgFlightRetention, Label: "Playback timeline retention (hours)", Category: "Operations", Kind: runtimecfg.KindInt,
			Default: "24", Min: 1, Max: 168, Help: "How long playback timelines stay in server memory after the last event."},
		{Key: cfgTelemetryBudget, Label: "Client telemetry per session (events per minute)", Category: "Operations", Kind: runtimecfg.KindInt,
			Default: "60", Min: 10, Max: 600, Help: "Maximum client telemetry events accepted per playback session per minute."},
	}
}
