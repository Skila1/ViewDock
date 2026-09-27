package secrets

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRoundTripAndBinding(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := c.Encrypt("tmdb.api_key", "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if !IsEncrypted(enc) || enc == "s3cret" {
		t.Fatalf("not encrypted: %q", enc)
	}
	if got, err := c.Decrypt("tmdb.api_key", enc); err != nil || got != "s3cret" {
		t.Fatalf("decrypt %q %v", got, err)
	}
	if _, err := c.Decrypt("discord.bot_token", enc); err == nil {
		t.Fatal("ciphertext accepted under a different key name")
	}

	again, err := Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != c.ID {
		t.Fatal("key file not reused")
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(filepath.Join(dir, keyFile))
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("key file mode %v", st.Mode().Perm())
		}
	}
	other, _ := New(make([]byte, 32))
	if _, err := other.Decrypt("tmdb.api_key", enc); err == nil {
		t.Fatal("decrypted with the wrong master key")
	}
	if _, err := Load(t.TempDir(), "not-base64"); err == nil {
		t.Fatal("invalid VD_MASTER_KEY accepted")
	}
}
