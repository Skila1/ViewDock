package nodeauth

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSignVerify(t *testing.T) {
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	v := NewVerifier(secret)
	body := []byte(`{"a":1}`)
	req := httptest.NewRequest("PUT", "http://worker/api/v1/playback/sessions/x/progress?stoken=t", nil)
	if err := Sign(req, "n1", []byte(secret), body); err != nil {
		t.Fatal(err)
	}
	if err := v.Verify(req, body); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := v.Verify(req, body); !errors.Is(err, ErrReplay) {
		t.Fatalf("replay accepted: %v", err)
	}

	tampered := httptest.NewRequest("PUT", "http://worker/api/v1/playback/sessions/y/progress?stoken=t", nil)
	_ = Sign(tampered, "n1", []byte(secret), body)
	tampered.URL.Path = "/api/v1/playback/sessions/z/progress"
	if err := v.Verify(tampered, body); !errors.Is(err, ErrSignature) {
		t.Fatalf("tampered path accepted: %v", err)
	}
	other := httptest.NewRequest("PUT", "http://worker/p", nil)
	_ = Sign(other, "n1", []byte(secret), body)
	if err := v.Verify(other, []byte(`{"a":2}`)); !errors.Is(err, ErrSignature) {
		t.Fatalf("tampered body accepted: %v", err)
	}
	wrong := httptest.NewRequest("GET", "http://worker/p", nil)
	_ = Sign(wrong, "n1", []byte("not-the-secret"), nil)
	if err := v.Verify(wrong, nil); !errors.Is(err, ErrSignature) {
		t.Fatalf("wrong secret accepted: %v", err)
	}
	if err := v.Verify(httptest.NewRequest("GET", "http://worker/p", nil), nil); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("unsigned accepted: %v", err)
	}

	stale := httptest.NewRequest("GET", "http://worker/p", nil)
	_ = Sign(stale, "n1", []byte(secret), nil)
	v.now = func() time.Time { return time.Now().Add(2 * MaxSkew) }
	if err := v.Verify(stale, nil); !errors.Is(err, ErrExpired) {
		t.Fatalf("stale signature accepted: %v", err)
	}
}
