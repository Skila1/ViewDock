package config

import (
	"net"
	"testing"
)

func TestIsLAN(t *testing.T) {
	cfg := Load()
	if !cfg.IsLAN(net.ParseIP("192.168.1.10")) {
		t.Fatal("expected LAN")
	}
	if cfg.IsLAN(net.ParseIP("8.8.8.8")) {
		t.Fatal("public IP is remote")
	}
	if cfg.IsLAN(nil) {
		t.Fatal("nil is remote")
	}
}

func TestTrustedDefaultLoopback(t *testing.T) {
	cfg := Load()
	if !cfg.TrustedContains(net.ParseIP("127.0.0.1")) {
		t.Fatal("loopback should be trusted")
	}
	if cfg.TrustedContains(net.ParseIP("10.0.0.1")) {
		t.Fatal("LAN must not be trusted by default")
	}
}
