package setup

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/settings"
)

const (
	settingBootstrapHash     = "setup.bootstrap_hash"
	settingBootstrapConsumed = "setup.bootstrap_consumed"
	settingAdminCreated      = "setup.admin_created"

	// Easy to type: no 0/O, 1/I. Length is within the 4–12 range the UI expects.
	setupTokenLen      = 8
	setupTokenAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

func TokenFile(cfg config.Config) string {
	return filepath.Join(cfg.ConfigDir, "setup.token")
}

func AdminCreated(ctx context.Context, kv *settings.Store) bool {
	return kv != nil && kv.Bool(ctx, settingAdminCreated)
}

func BootstrapPending(ctx context.Context, kv *settings.Store) bool {
	if kv == nil {
		return false
	}
	if kv.Bool(ctx, settingBootstrapConsumed) || kv.Bool(ctx, settingAdminCreated) {
		return false
	}
	h, _ := kv.Get(ctx, settingBootstrapHash)
	return h != ""
}

func normalizeSetupToken(raw string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToUpper(r)
	}, raw)
}

func isEasySetupToken(raw string) bool {
	s := normalizeSetupToken(raw)
	if len(s) < 4 || len(s) > 12 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(setupTokenAlphabet, r) {
			return false
		}
	}
	return true
}

func generateSetupToken() (string, error) {
	out := make([]byte, setupTokenLen)
	alphabet := []byte(setupTokenAlphabet)
	for i := range out {
		var b [1]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		out[i] = alphabet[int(b[0])%len(alphabet)]
	}
	return string(out), nil
}

func ReadTokenFile(cfg config.Config) string {
	b, err := os.ReadFile(TokenFile(cfg))
	if err != nil {
		return ""
	}
	return normalizeSetupToken(string(b))
}

func writeTokenFile(cfg config.Config, raw string) error {
	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(TokenFile(cfg), []byte(raw+"\n"), 0o600)
}

// AnnounceToken prints the pending first-admin token to stdout, stderr, and logs.
func AnnounceToken(cfg config.Config, log *slog.Logger) {
	raw := ReadTokenFile(cfg)
	if raw == "" {
		return
	}
	line := fmt.Sprintf("ViewDock setup token: %s  (also in %s)", raw, TokenFile(cfg))
	_, _ = fmt.Fprintln(os.Stdout, line)
	_, _ = fmt.Fprintln(os.Stderr, line)
	if log != nil {
		log.Info("setup bootstrap token ready", "token", raw, "token_file", TokenFile(cfg))
	}
}

func EnsureBootstrap(ctx context.Context, kv *settings.Store, cfg config.Config, log *slog.Logger, userCount int) error {
	if kv == nil {
		return nil
	}
	if userCount > 0 || kv.Bool(ctx, settingAdminCreated) || kv.Bool(ctx, "setup.complete") {
		if log != nil {
			log.Info("setup bootstrap closed")
		}
		return nil
	}
	if kv.Bool(ctx, settingBootstrapConsumed) {
		return nil
	}

	existingHash, _ := kv.Get(ctx, settingBootstrapHash)
	fileTok := ReadTokenFile(cfg)
	keep := existingHash != "" && isEasySetupToken(fileTok) && auth.HashToken(fileTok) == existingHash
	if keep {
		AnnounceToken(cfg, log)
		return nil
	}

	raw, err := generateSetupToken()
	if err != nil {
		return err
	}
	if err := writeTokenFile(cfg, raw); err != nil {
		return err
	}
	if err := kv.Set(ctx, settingBootstrapHash, auth.HashToken(raw)); err != nil {
		return err
	}
	AnnounceToken(cfg, log)
	return nil
}

func consumeBootstrap(ctx context.Context, kv *settings.Store, cfg config.Config) {
	if kv == nil {
		return
	}
	_ = kv.Set(ctx, settingBootstrapConsumed, "1")
	_ = kv.Set(ctx, settingAdminCreated, "1")
	_ = os.Remove(TokenFile(cfg))
}

func checkBootstrapToken(ctx context.Context, kv *settings.Store, raw string) bool {
	if kv == nil || kv.Bool(ctx, settingBootstrapConsumed) {
		return false
	}
	want, _ := kv.Get(ctx, settingBootstrapHash)
	got := normalizeSetupToken(raw)
	if want == "" || got == "" {
		return false
	}
	return auth.HashToken(got) == want
}
