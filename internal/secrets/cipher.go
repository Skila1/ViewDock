// Package secrets encrypts configuration secrets at rest with AES-256-GCM.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Prefix marks an encrypted value; the version allows future key rotation.
const Prefix = "enc:v1:"

const keyFile = "master.key"

var ErrDecrypt = errors.New("secret could not be decrypted with the configured master key")

type Cipher struct {
	aead cipher.AEAD
	// ID is a short fingerprint of the master key, safe to display.
	ID string
}

// Load returns a cipher using VD_MASTER_KEY (base64, 32 bytes) when set, or a
// key file in configDir that is created with owner-only permissions on first use.
func Load(configDir, envKey string) (*Cipher, error) {
	var key []byte
	if strings.TrimSpace(envKey) != "" {
		k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(envKey))
		if err != nil || len(k) != 32 {
			return nil, errors.New("VD_MASTER_KEY must be 32 bytes encoded as base64")
		}
		key = k
	} else {
		path := filepath.Join(configDir, keyFile)
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			k, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
			if derr != nil || len(k) != 32 {
				return nil, fmt.Errorf("%s is not a valid master key", path)
			}
			key = k
		case errors.Is(err, os.ErrNotExist):
			key = make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return nil, err
			}
			if err := os.MkdirAll(configDir, 0o700); err != nil {
				return nil, err
			}
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				if errors.Is(err, os.ErrExist) {
					return Load(configDir, "")
				}
				return nil, err
			}
			_, werr := f.WriteString(base64.StdEncoding.EncodeToString(key) + "\n")
			cerr := f.Close()
			if werr != nil || cerr != nil {
				return nil, errors.Join(werr, cerr)
			}
		default:
			return nil, err
		}
	}
	return New(key)
}

func New(key []byte) (*Cipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(key)
	return &Cipher{aead: aead, ID: base64.RawURLEncoding.EncodeToString(sum[:6])}, nil
}

func IsEncrypted(v string) bool { return strings.HasPrefix(v, Prefix) }

// Encrypt binds the ciphertext to name so it cannot be replayed under another key.
func (c *Cipher) Encrypt(name, plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := c.aead.Seal(nonce, nonce, []byte(plaintext), []byte(name))
	return Prefix + base64.RawStdEncoding.EncodeToString(out), nil
}

func (c *Cipher) Decrypt(name, value string) (string, error) {
	if !IsEncrypted(value) {
		return value, nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(value, Prefix))
	if err != nil || len(raw) < c.aead.NonceSize() {
		return "", ErrDecrypt
	}
	n := c.aead.NonceSize()
	plain, err := c.aead.Open(nil, raw[:n], raw[n:], []byte(name))
	if err != nil {
		return "", ErrDecrypt
	}
	return string(plain), nil
}
