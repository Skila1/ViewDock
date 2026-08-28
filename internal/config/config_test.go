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

func TestTrustedLocalAndCloudflare(t *testing.T) {
	cfg := Load()
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "172.18.0.1", "192.168.1.1", "162.158.1.1", "104.16.1.1", "2606:4700::1"} {
		if !cfg.TrustedContains(net.ParseIP(ip)) {
			t.Fatalf("%s should be trusted (local or Cloudflare)", ip)
		}
	}
	if cfg.TrustedContains(net.ParseIP("8.8.8.8")) {
		t.Fatal("public resolver must not be trusted")
	}
}
