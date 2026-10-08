package config

import (
	"net"
	"os"
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

func TestAllowedOrigins(t *testing.T) {
	t.Setenv("VD_ALLOWED_ORIGINS", "https://app.example/, http://localhost:5173")
	cfg := Load()
	if len(cfg.AllowedOrigins) != 2 || cfg.AllowedOrigins[0] != "https://app.example" || cfg.AllowedOrigins[1] != "http://localhost:5173" {
		t.Fatalf("allowed origins: %#v", cfg.AllowedOrigins)
	}
	_ = os.Unsetenv("VD_ALLOWED_ORIGINS")
}

func TestDatabaseProviderConfiguration(t *testing.T) {
	t.Setenv("VD_DATABASE_DRIVER", "postgres")
	t.Setenv("VD_DATABASE_URL", "postgres://viewdock:secret@db/viewdock")
	cfg := Load()
	if cfg.DatabaseDriver != "postgres" || cfg.DatabaseURL == "" {
		t.Fatalf("database config: %#v", cfg)
	}
}

func TestStorageProviderConfiguration(t *testing.T) {
	t.Setenv("VD_STORAGE_DRIVER", "s3")
	t.Setenv("VD_STORAGE_ENDPOINT", "minio:9000")
	t.Setenv("VD_STORAGE_BUCKET", "viewdock")
	t.Setenv("VD_STORAGE_USE_SSL", "true")
	cfg := Load()
	if cfg.StorageDriver != "s3" || cfg.StorageEndpoint != "minio:9000" || cfg.StorageBucket != "viewdock" || !cfg.StorageUseSSL {
		t.Fatalf("storage config: %#v", cfg)
	}
}
