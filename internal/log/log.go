package log

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/viewdock/viewdock/internal/oplog"
)

func New(level string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(level)}))
}

func Tee(stdout *slog.Logger, store *oplog.Store, level string) *slog.Logger {
	if store == nil {
		return stdout
	}
	return slog.New(&teeHandler{
		out:   slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(level)}),
		store: store,
		level: parseLevel(level),
	})
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type teeHandler struct {
	out   slog.Handler
	store *oplog.Store
	level slog.Level
}

func (h *teeHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

func (h *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	_ = h.out.Handle(ctx, r)
	if h.store != nil {
		h.store.FromRecord(r)
	}
	return nil
}

func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &teeHandler{out: h.out.WithAttrs(attrs), store: h.store, level: h.level}
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	return &teeHandler{out: h.out.WithGroup(name), store: h.store, level: h.level}
}
