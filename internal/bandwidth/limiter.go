package bandwidth

import (
	"errors"
	"net"
	"net/http"
	"sync"

	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/httpapi"
)

var ErrLoad = errors.New("LOAD_429")

const (
	DefaultSlots         = 2
	DefaultRemoteBitrate = 8_000_000
)

type Limiter struct {
	Slots         int
	RemoteBitrate int64

	mu     sync.Mutex
	active int
}

func New(slots int) *Limiter {
	if slots <= 0 {
		slots = DefaultSlots
	}
	return &Limiter{Slots: slots, RemoteBitrate: DefaultRemoteBitrate}
}

// SetSlots changes capacity at runtime; sessions already admitted keep running.
func (l *Limiter) SetSlots(n int) {
	if n <= 0 {
		n = DefaultSlots
	}
	l.mu.Lock()
	l.Slots = n
	l.mu.Unlock()
}

func (l *Limiter) TryAcquire() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active >= l.Slots {
		return ErrLoad
	}
	l.active++
	return nil
}

func (l *Limiter) Release() {
	l.mu.Lock()
	if l.active > 0 {
		l.active--
	}
	l.mu.Unlock()
}

func (l *Limiter) Capacity() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.Slots
}

func (l *Limiter) Active() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active
}

func IsLAN(r *http.Request, cfg config.Config) bool {
	ip := httpapi.ClientIP(r, cfg)
	return cfg.IsLAN(ip)
}

func IsRemote(r *http.Request, cfg config.Config) bool {
	return !IsLAN(r, cfg)
}

func ClientIP(r *http.Request, cfg config.Config) net.IP {
	return httpapi.ClientIP(r, cfg)
}

func ShareHeight(allowedQuality string) int {
	switch allowedQuality {
	case "1080":
		return 1080
	case "720":
		return 720
	case "480":
		return 480
	default:
		return 0
	}
}
