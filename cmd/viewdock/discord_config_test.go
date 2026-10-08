package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/runtimecfg"
	"github.com/viewdock/viewdock/internal/secrets"
	"github.com/viewdock/viewdock/internal/settings"
)

func newTestRuntimeConfig(t *testing.T) *runtimecfg.Service {
	t.Helper()
	for _, k := range []string{"VD_DISCORD_BOT_TOKEN", "VD_DISCORD_PUBLIC_KEY"} {
		t.Setenv(k, "")
	}
	path := filepath.Join(t.TempDir(), "t.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	kv := settings.New(sqlDB)
	c, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	kv.UseCipher(c)
	rc := runtimecfg.New(sqlDB, kv, audit.New(sqlDB), configDefs(config.Config{}))
	if err := rc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return rc
}

func applyConfig(t *testing.T, rc *runtimecfg.Service, values map[string]string) {
	t.Helper()
	changes := map[string]runtimecfg.Change{}
	for k, v := range values {
		changes[k] = runtimecfg.Change{Value: v}
	}
	if _, err := rc.Apply(context.Background(), "admin", "", changes, rc.Version(), "test"); err != nil {
		t.Fatal(err)
	}
}

func TestDiscordBotCredentialsFollowTheSeparateToggle(t *testing.T) {
	rc := newTestRuntimeConfig(t)
	noEnv := func(string) string { return "" }

	if rc.Bool(cfgDiscordSeparate) {
		t.Fatal("the separate bot configuration must be off by default")
	}
	applyConfig(t, rc, map[string]string{cfgDiscordBot: "shared-token", cfgDiscordPublicKey: "shared-key"})
	if tok, key := discordBotKeys(rc); tok != cfgDiscordBot || key != cfgDiscordPublicKey {
		t.Fatalf("shared mode keys = %s, %s", tok, key)
	}
	if got := discordBotToken(rc, noEnv); got != "shared-token" {
		t.Fatalf("shared mode token = %q", got)
	}

	applyConfig(t, rc, map[string]string{cfgDiscordSeparate: "1", cfgDiscordSepToken: "separate-token", cfgDiscordSepKey: "separate-key"})
	if tok, key := discordBotKeys(rc); tok != cfgDiscordSepToken || key != cfgDiscordSepKey {
		t.Fatalf("separate mode keys = %s, %s", tok, key)
	}
	if got := discordBotToken(rc, noEnv); got != "separate-token" {
		t.Fatalf("separate mode token = %q", got)
	}
	if rc.String(cfgDiscordBot) != "shared-token" {
		t.Fatal("turning the separate configuration on must keep the shared token")
	}

	applyConfig(t, rc, map[string]string{cfgDiscordSeparate: "0"})
	if got := discordBotToken(rc, noEnv); got != "shared-token" {
		t.Fatalf("token after turning separate off = %q", got)
	}
	if rc.String(cfgDiscordSepToken) != "separate-token" || rc.String(cfgDiscordSepKey) != "separate-key" {
		t.Fatal("turning the separate configuration off must keep the separate credentials")
	}
}

func TestDiscordBotTokenEnvironmentFallbackIsSharedOnly(t *testing.T) {
	rc := newTestRuntimeConfig(t)
	env := func(k string) string {
		if k == "VD_DISCORD_BOT_TOKEN" {
			return "env-token"
		}
		return ""
	}
	if got := discordBotToken(rc, env); got != "env-token" {
		t.Fatalf("shared mode without a saved token = %q, want the environment token", got)
	}
	applyConfig(t, rc, map[string]string{cfgDiscordSeparate: "1"})
	if got := discordBotToken(rc, env); got != "" {
		t.Fatalf("separate mode without a separate token = %q, want no token", got)
	}
}
