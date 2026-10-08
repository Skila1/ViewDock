// Package nodeauth signs and verifies requests between the control plane and
// media workers with a per-node shared secret.
package nodeauth

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	HeaderNode  = "X-VD-Node-ID"
	HeaderTime  = "X-VD-Node-Time"
	HeaderNonce = "X-VD-Node-Nonce"
	HeaderSig   = "X-VD-Node-Signature"

	MaxSkew    = 60 * time.Second
	maxNonces  = 50000
	secretSize = 32
)

var (
	ErrUnsigned  = errors.New("request is not signed by the control plane")
	ErrSignature = errors.New("node signature is invalid")
	ErrExpired   = errors.New("node signature is outside the allowed clock skew")
	ErrReplay    = errors.New("node signature was already used")
	ErrTooLarge  = errors.New("signed request body is too large")
)

// NewSecret returns a random secret suitable for VD_NODE_SECRET.
func NewSecret() (string, error) {
	b := make([]byte, secretSize)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func mac(secret []byte, method, uri, ts, nonce string, body []byte) string {
	sum := sha256.Sum256(body)
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(method + "\n" + uri + "\n" + ts + "\n" + nonce + "\n" + hex.EncodeToString(sum[:])))
	return hex.EncodeToString(h.Sum(nil))
}

// Sign adds signature headers to req. body must be the exact request body.
func Sign(req *http.Request, nodeID string, secret []byte, body []byte) error {
	n := make([]byte, 12)
	if _, err := rand.Read(n); err != nil {
		return err
	}
	nonce := base64.RawURLEncoding.EncodeToString(n)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set(HeaderNode, nodeID)
	req.Header.Set(HeaderTime, ts)
	req.Header.Set(HeaderNonce, nonce)
	req.Header.Set(HeaderSig, mac(secret, req.Method, req.URL.RequestURI(), ts, nonce, body))
	return nil
}

// Verifier checks signatures on a worker and rejects replays within the
// allowed clock skew.
type Verifier struct {
	secret []byte
	now    func() time.Time

	mu     sync.Mutex
	nonces map[string]time.Time
}

func NewVerifier(secret string) *Verifier {
	return &Verifier{secret: []byte(secret), now: time.Now, nonces: map[string]time.Time{}}
}

// VerifyRequest reads up to maxBody bytes of the body (GET and HEAD are
// signed over an empty body), restores it for the next handler and verifies
// the signature over it.
func (v *Verifier) VerifyRequest(r *http.Request, maxBody int64) error {
	var body []byte
	if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
		b, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
		if err != nil || int64(len(b)) > maxBody {
			return ErrTooLarge
		}
		body = b
		r.Body = io.NopCloser(bytes.NewReader(b))
	}
	return v.Verify(r, body)
}

// Signed reports whether r carries signature headers at all.
func Signed(r *http.Request) bool { return r.Header.Get(HeaderSig) != "" }

func (v *Verifier) Verify(r *http.Request, body []byte) error {
	sig, ts, nonce := r.Header.Get(HeaderSig), r.Header.Get(HeaderTime), r.Header.Get(HeaderNonce)
	if sig == "" || ts == "" || nonce == "" {
		return ErrUnsigned
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return ErrSignature
	}
	now := v.now()
	if d := now.Sub(time.Unix(sec, 0)); d > MaxSkew || d < -MaxSkew {
		return ErrExpired
	}
	want := mac(v.secret, r.Method, r.URL.RequestURI(), ts, nonce, body)
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return ErrSignature
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, seen := v.nonces[nonce]; seen {
		return ErrReplay
	}
	if len(v.nonces) >= maxNonces {
		for k, exp := range v.nonces {
			if now.After(exp) {
				delete(v.nonces, k)
			}
		}
		if len(v.nonces) >= maxNonces {
			return ErrReplay
		}
	}
	v.nonces[nonce] = now.Add(2 * MaxSkew)
	return nil
}
