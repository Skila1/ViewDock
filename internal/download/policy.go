package download

import (
	"bytes"
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/library"
)

const (
	DefaultMaxItems   = 10
	DefaultExpiryDays = 30
	MaxItemsLimit     = 500
	MaxExpiryDays     = 365
	// SpeedTestBytes bounds the throughput probe so it cannot be used to pull
	// arbitrary volumes from the server.
	SpeedTestBytes = 8 << 20
)

// Policy is the Offline Vault policy for the calling account. Clients enforce
// MaxItems and expiry locally; the per-library flags mirror the download grants
// the download endpoint already enforces.
type Policy struct {
	Enabled        bool            `json:"enabled"`
	MaxItems       int             `json:"max_items"`
	ExpiryDays     int             `json:"expiry_days"`
	MaxItemBytes   int64           `json:"max_item_bytes"`
	DefaultAllowed bool            `json:"default_allowed"`
	Libraries      map[string]bool `json:"libraries"`
	IssuedAt       time.Time       `json:"issued_at"`
}

// PolicyAPI serves the offline policy and a bounded throughput probe. Nil
// funcs fall back to defaults: enabled, DefaultMaxItems, DefaultExpiryDays and
// no per-item size cap.
type PolicyAPI struct {
	Grants     library.LibraryGrants
	Enabled    func() bool
	MaxItems   func() int
	ExpiryDays func() int
	MaxItemGB  func() int
	Now        func() time.Time
}

func (a *PolicyAPI) Routes(r chi.Router) {
	r.Route("/offline", func(r chi.Router) {
		r.Use(auth.RequireUser)
		r.Get("/policy", a.handlePolicy)
		r.Get("/speedtest", a.handleSpeedTest)
	})
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// For builds the policy for p. It never errors: when grants cannot be read the
// library map is empty and DefaultAllowed decides, which is false for
// non-admin users.
func (a *PolicyAPI) For(ctx context.Context, p *auth.Principal) Policy {
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	pol := Policy{
		Enabled:    a.Enabled == nil || a.Enabled(),
		MaxItems:   DefaultMaxItems,
		ExpiryDays: DefaultExpiryDays,
		Libraries:  map[string]bool{},
		IssuedAt:   now().UTC(),
	}
	if a.MaxItems != nil {
		pol.MaxItems = clamp(a.MaxItems(), 1, MaxItemsLimit)
	}
	if a.ExpiryDays != nil {
		pol.ExpiryDays = clamp(a.ExpiryDays(), 0, MaxExpiryDays)
	}
	if a.MaxItemGB != nil {
		pol.MaxItemBytes = int64(clamp(a.MaxItemGB(), 0, 1024)) << 30
	}
	if p == nil {
		return pol
	}
	pol.DefaultAllowed = p.IsAdmin
	if a.Grants == nil {
		return pol
	}
	ids, err := a.Grants.GrantedLibraryIDs(ctx, p.UserID)
	if err != nil {
		return pol
	}
	for _, id := range ids {
		pol.Libraries[id] = Can(ctx, p, a.Grants, id)
	}
	return pol
}

func (a *PolicyAPI) handlePolicy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	httpapi.WriteJSON(w, http.StatusOK, a.For(r.Context(), auth.FromRequest(r)))
}

var (
	speedOnce    sync.Once
	speedPayload []byte
)

// payload is incompressible so intermediaries cannot inflate the measurement.
func payload() []byte {
	speedOnce.Do(func() {
		speedPayload = make([]byte, SpeedTestBytes)
		x := uint64(0x9E3779B97F4A7C15)
		for i := 0; i < len(speedPayload); i += 8 {
			x ^= x << 13
			x ^= x >> 7
			x ^= x << 17
			for j := 0; j < 8 && i+j < len(speedPayload); j++ {
				speedPayload[i+j] = byte(x >> (8 * j))
			}
		}
	})
	return speedPayload
}

func (a *PolicyAPI) handleSpeedTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store, no-transform")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(payload()))
}
