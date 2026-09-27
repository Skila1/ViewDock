package discordbot

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"
	"time"
)

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func sign(priv ed25519.PrivateKey, ts string, body []byte) string {
	return hex.EncodeToString(ed25519.Sign(priv, append([]byte(ts), body...)))
}

func TestVerifierSignatures(t *testing.T) {
	pub, priv := testKey(t)
	now := time.Unix(1_800_000_000, 0)
	v := NewVerifier(func() string { return hex.EncodeToString(pub) })
	v.Now = func() time.Time { return now }
	body := []byte(`{"type":1}`)
	ts := strconv.FormatInt(now.Unix(), 10)

	if err := v.Verify(sign(priv, ts, body), ts, body); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := v.Verify(sign(priv, ts, body), ts, body); !errors.Is(err, ErrReplay) {
		t.Fatalf("replay = %v, want ErrReplay", err)
	}

	ts2 := strconv.FormatInt(now.Unix()+1, 10)
	if err := v.Verify(sign(priv, ts2, body), ts2, []byte(`{"type":2}`)); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("tampered body = %v", err)
	}
	_, other := testKey(t)
	if err := v.Verify(sign(other, ts2, body), ts2, body); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("foreign key = %v", err)
	}
	if err := v.Verify("zz", ts2, body); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("malformed signature = %v", err)
	}
	if err := v.Verify("", ts2, body); !errors.Is(err, ErrMissingSignature) {
		t.Fatalf("missing signature = %v", err)
	}
	if err := v.Verify(sign(priv, "abc", body), "abc", body); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("non-numeric timestamp = %v", err)
	}

	stale := strconv.FormatInt(now.Add(-DefaultWindow-time.Second).Unix(), 10)
	if err := v.Verify(sign(priv, stale, body), stale, body); !errors.Is(err, ErrStaleTimestamp) {
		t.Fatalf("stale timestamp = %v", err)
	}
	future := strconv.FormatInt(now.Add(DefaultWindow+time.Second).Unix(), 10)
	if err := v.Verify(sign(priv, future, body), future, body); !errors.Is(err, ErrStaleTimestamp) {
		t.Fatalf("future timestamp = %v", err)
	}
	edge := strconv.FormatInt(now.Add(-DefaultWindow+time.Second).Unix(), 10)
	if err := v.Verify(sign(priv, edge, body), edge, body); err != nil {
		t.Fatalf("timestamp inside window rejected: %v", err)
	}
}

func TestVerifierReplayCacheExpires(t *testing.T) {
	pub, priv := testKey(t)
	now := time.Unix(1_800_000_000, 0)
	v := NewVerifier(func() string { return hex.EncodeToString(pub) })
	v.Window = time.Minute
	v.Now = func() time.Time { return now }
	body := []byte(`{}`)
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := sign(priv, ts, body)
	if err := v.Verify(sig, ts, body); err != nil {
		t.Fatal(err)
	}
	// Once the entry expires the timestamp is stale anyway, so a replay
	// still fails, now on the window check.
	now = now.Add(3 * time.Minute)
	if err := v.Verify(sig, ts, body); !errors.Is(err, ErrStaleTimestamp) {
		t.Fatalf("expired replay = %v", err)
	}
	if len(v.seen) != 1 {
		t.Fatalf("replay cache size = %d", len(v.seen))
	}
	ts2 := strconv.FormatInt(now.Unix(), 10)
	if err := v.Verify(sign(priv, ts2, body), ts2, body); err != nil {
		t.Fatal(err)
	}
	if len(v.seen) != 1 {
		t.Fatalf("expired entries not pruned: %d", len(v.seen))
	}
}

func TestVerifierKeyConfiguration(t *testing.T) {
	key := ""
	v := NewVerifier(func() string { return key })
	if v.Configured() {
		t.Fatal("empty key reported configured")
	}
	if err := v.Verify("00", "1", nil); !errors.Is(err, ErrNoPublicKey) {
		t.Fatalf("no key = %v", err)
	}
	key = "not-hex"
	if err := v.Verify("00", "1", nil); !errors.Is(err, ErrBadPublicKey) {
		t.Fatalf("bad key = %v", err)
	}
	pub, priv := testKey(t)
	key = hex.EncodeToString(pub)
	if !v.Configured() || !ValidPublicKey(key) {
		t.Fatal("valid key not accepted")
	}
	// Key rotation takes effect without a restart.
	pub2, priv2 := testKey(t)
	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	key = hex.EncodeToString(pub2)
	if err := v.Verify(sign(priv, ts, []byte("a")), ts, []byte("a")); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("old key still accepted: %v", err)
	}
	if err := v.Verify(sign(priv2, ts, []byte("a")), ts, []byte("a")); err != nil {
		t.Fatalf("rotated key rejected: %v", err)
	}
}
