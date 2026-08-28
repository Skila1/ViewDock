package bandwidth

import (
	"net"
	"net/http/httptest"
	"testing"

	"github.com/viewdock/viewdock/internal/config"
)

func TestUnknownIPIsRemote(t *testing.T) {
	cfg := config.Load()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "not-an-ip"
	if IsLAN(req, cfg) {
		t.Fatal("unknown IP must be remote")
	}
	if cfg.IsLAN(nil) {
		t.Fatal("nil IP is remote")
	}
}

func TestPublicIPRemote(t *testing.T) {
	cfg := config.Load()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "8.8.8.8:9"
	if IsLAN(req, cfg) {
		t.Fatal("public IP is remote")
	}
	if !cfg.IsLAN(net.ParseIP("192.168.1.2")) {
		t.Fatal("LAN")
	}
}

func TestSlots429(t *testing.T) {
	l := New(1)
	if err := l.TryAcquire(); err != nil {
		t.Fatal(err)
	}
	if err := l.TryAcquire(); err != ErrLoad {
		t.Fatalf("want LOAD_429 got %v", err)
	}
	l.Release()
	if err := l.TryAcquire(); err != nil {
		t.Fatal(err)
	}
}
