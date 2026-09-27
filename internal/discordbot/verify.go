package discordbot

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Signature verification failures. All of them must be answered with 401 so
// Discord's endpoint validation accepts the URL.
var (
	ErrNoPublicKey      = errors.New("discord public key is not configured")
	ErrBadPublicKey     = errors.New("discord public key is not a valid Ed25519 key")
	ErrMissingSignature = errors.New("missing signature headers")
	ErrBadSignature     = errors.New("invalid request signature")
	ErrStaleTimestamp   = errors.New("request timestamp outside the accepted window")
	ErrReplay           = errors.New("request already processed")
)

// DefaultWindow bounds how far a signed timestamp may be from the local clock.
const DefaultWindow = 5 * time.Minute

const maxReplayEntries = 100_000

// Verifier checks Discord's Ed25519 interaction signatures, rejects stale
// timestamps and refuses to process the same signed request twice.
type Verifier struct {
	// PublicKey returns the hex-encoded application public key.
	PublicKey func() string
	Window    time.Duration
	Now       func() time.Time

	mu        sync.Mutex
	seen      map[[32]byte]time.Time
	lastPrune time.Time
	keyHex    string
	key       ed25519.PublicKey
}

func NewVerifier(publicKey func() string) *Verifier {
	return &Verifier{PublicKey: publicKey, Window: DefaultWindow, Now: time.Now}
}

// Configured reports whether a usable public key is set.
func (v *Verifier) Configured() bool {
	_, err := v.publicKey()
	return err == nil
}

// ValidPublicKey reports whether s is a hex-encoded Ed25519 public key.
func ValidPublicKey(s string) bool {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	return err == nil && len(b) == ed25519.PublicKeySize
}

func (v *Verifier) publicKey() (ed25519.PublicKey, error) {
	raw := ""
	if v.PublicKey != nil {
		raw = strings.ToLower(strings.TrimSpace(v.PublicKey()))
	}
	if raw == "" {
		return nil, ErrNoPublicKey
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if raw == v.keyHex && v.key != nil {
		return v.key, nil
	}
	b, err := hex.DecodeString(raw)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, ErrBadPublicKey
	}
	v.keyHex, v.key = raw, ed25519.PublicKey(b)
	return v.key, nil
}

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func (v *Verifier) window() time.Duration {
	if v.Window > 0 {
		return v.Window
	}
	return DefaultWindow
}

// Verify checks the X-Signature-Ed25519 and X-Signature-Timestamp header
// values against body. A request that verifies is remembered for twice the
// timestamp window so an identical replay is rejected.
func (v *Verifier) Verify(signatureHex, timestamp string, body []byte) error {
	key, err := v.publicKey()
	if err != nil {
		return err
	}
	signatureHex, timestamp = strings.TrimSpace(signatureHex), strings.TrimSpace(timestamp)
	if signatureHex == "" || timestamp == "" {
		return ErrMissingSignature
	}
	sig, err := hex.DecodeString(signatureHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return ErrBadSignature
	}
	secs, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || secs <= 0 {
		return ErrBadSignature
	}
	msg := make([]byte, 0, len(timestamp)+len(body))
	msg = append(append(msg, timestamp...), body...)
	if !ed25519.Verify(key, msg, sig) {
		return ErrBadSignature
	}
	now := v.now()
	skew := now.Sub(time.Unix(secs, 0))
	if skew < 0 {
		skew = -skew
	}
	if skew > v.window() {
		return ErrStaleTimestamp
	}
	return v.remember(sha256.Sum256(sig), now)
}

func (v *Verifier) remember(id [32]byte, now time.Time) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.seen == nil {
		v.seen = map[[32]byte]time.Time{}
	}
	if len(v.seen) >= maxReplayEntries || now.Sub(v.lastPrune) > time.Minute {
		for k, exp := range v.seen {
			if now.After(exp) {
				delete(v.seen, k)
			}
		}
		v.lastPrune = now
	}
	if exp, ok := v.seen[id]; ok && !now.After(exp) {
		return ErrReplay
	}
	if len(v.seen) >= maxReplayEntries {
		// Only verified requests reach this point, so a full cache means
		// sustained legitimate load; refusing is safer than forgetting.
		return ErrReplay
	}
	v.seen[id] = now.Add(2 * v.window())
	return nil
}
