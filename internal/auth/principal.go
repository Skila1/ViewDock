package auth

import (
	"context"
	"net/http"
)

const (
	KindUser       = "user"
	KindGuestShare = "guest_share"
)

type Principal struct {
	Kind           string
	UserID         string
	SessionID      string
	GuestSessionID string
	ShareID        string
	MediaKind      string
	MediaID        string
	IsAdmin        bool
	DisplayName    string
	Username       string
	CanDownload    bool
	PINLocked      bool
	APIKey         bool
	Permissions    []string
	Roles          []string
}

func (p *Principal) HasPerm(name string) bool {
	if p == nil || !p.IsUser() {
		return false
	}
	if p.IsAdmin {
		return true
	}
	for _, x := range p.Permissions {
		if x == name || x == PermAdmin {
			return true
		}
	}
	return false
}

type ctxKey int

const principalKey ctxKey = 1

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey).(*Principal)
	return p
}

func FromRequest(r *http.Request) *Principal {
	return FromContext(r.Context())
}

func (p *Principal) IsUser() bool {
	return p != nil && p.Kind == KindUser && p.UserID != ""
}

func (p *Principal) IsGuest() bool {
	return p != nil && p.Kind == KindGuestShare
}

func (p *Principal) ID() string {
	if p == nil {
		return ""
	}
	if p.IsGuest() {
		return p.GuestSessionID
	}
	return p.UserID
}
