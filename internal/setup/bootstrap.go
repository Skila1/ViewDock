package setup

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/settings"
)

const (
	settingBootstrapHash     = "setup.bootstrap_hash"
	settingBootstrapConsumed = "setup.bootstrap_consumed"
	settingAdminCreated      = "setup.admin_created"
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
	path := TokenFile(cfg)
	if existing, _ := kv.Get(ctx, settingBootstrapHash); existing != "" {
		if log != nil {
			log.Info("setup bootstrap pending", "token_file", path)
		}
		return nil
	}
	raw := os.Getenv("VD_SETUP_TOKEN")
	wroteFile := false
	if raw == "" {
		tok, err := auth.RandomToken(24)
		if err != nil {
			return err
		}
		raw = tok
		if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(raw+"\n"), 0o600); err != nil {
			return err
		}
		wroteFile = true
	}
	if err := kv.Set(ctx, settingBootstrapHash, auth.HashToken(raw)); err != nil {
		return err
	}
	if log != nil {
		if wroteFile {
			log.Info("setup bootstrap token written", "token_file", path)
		} else {
			log.Info("setup bootstrap token accepted from environment")
		}
	}
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
	if want == "" || raw == "" {
		return false
	}
	return auth.HashToken(raw) == want
}
