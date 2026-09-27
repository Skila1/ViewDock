package share

import (
	"context"
	"errors"
)

var (
	ErrDenied = errors.New("share denied")
	ErrGone   = errors.New("share gone")
	ErrBusy   = errors.New("share concurrent limit")
)

// Gate is implemented by Auth and consumed by playback and watchtogether.
type Gate interface {
	AllowStream(ctx context.Context, guestSessionID, itemKind, itemID string) error
	CanStreamMedia(ctx context.Context, guestSessionID, itemKind, itemID string) error
	Heartbeat(ctx context.Context, guestSessionID string) error
	Release(ctx context.Context, guestSessionID string)
	ShareTokenForGuest(ctx context.Context, guestSessionID string) string
}

type noopGate struct{}

func NoopGate() Gate { return noopGate{} }

func (noopGate) AllowStream(context.Context, string, string, string) error { return ErrDenied }
func (noopGate) CanStreamMedia(context.Context, string, string, string) error {
	return ErrDenied
}
func (noopGate) Heartbeat(context.Context, string) error { return nil }
func (noopGate) Release(context.Context, string)         {}
func (noopGate) ShareTokenForGuest(context.Context, string) string {
	return ""
}
