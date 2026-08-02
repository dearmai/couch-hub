// Package secret seals credentials before they are written to the local store.
//
// CouchHub necessarily holds material that grants full access to a vault: the
// CouchDB admin password, each vault's database account, and the end-to-end
// passphrase that decrypts vault contents. Storing those in plaintext would
// make a stolen couchhub.db equivalent to a stolen vault, so they are sealed
// with a key derived from COUCHHUB_SECRET.
//
// This protects a file at rest. It does not protect against an attacker who can
// read the running process, since the key is in memory for the lifetime of the
// server.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

const (
	// pbkdf2Iterations follows the same OWASP guidance livesync uses. It is paid
	// once at startup, not per operation.
	pbkdf2Iterations = 600000
	saltLen          = 32
	keyLen           = 32
)

// ErrDisabled is returned when sealing is attempted without COUCHHUB_SECRET set.
var ErrDisabled = errors.New("secret: COUCHHUB_SECRET is not set, credentials cannot be persisted")

// ErrCorrupt is returned when sealed data fails authentication, which means
// either the file was tampered with or COUCHHUB_SECRET changed.
var ErrCorrupt = errors.New("secret: cannot open sealed data (wrong COUCHHUB_SECRET, or the store was tampered with)")

// Sealer encrypts and decrypts credentials for storage.
//
// The zero value is a disabled Sealer: Seal and Open both fail with ErrDisabled.
// Callers must check Enabled and degrade gracefully rather than storing
// plaintext.
type Sealer struct {
	gcm cipher.AEAD
}

// NewSalt returns a fresh salt to persist alongside the store. The salt is not
// secret; it exists so two CouchHub instances sharing a COUCHHUB_SECRET still
// derive different keys.
func NewSalt() ([]byte, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("secret: generate salt: %w", err)
	}
	return salt, nil
}

// New derives a Sealer from the operator's secret and the store's salt.
// An empty passphrase yields a disabled Sealer rather than an error, so the
// server can still run read-only-ish without credential persistence.
func New(passphrase string, salt []byte) (Sealer, error) {
	if passphrase == "" {
		return Sealer{}, nil
	}
	if len(salt) != saltLen {
		return Sealer{}, fmt.Errorf("secret: salt must be %d bytes, got %d", saltLen, len(salt))
	}
	key, err := pbkdf2.Key(sha256.New, passphrase, salt, pbkdf2Iterations, keyLen)
	if err != nil {
		return Sealer{}, fmt.Errorf("secret: derive key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return Sealer{}, fmt.Errorf("secret: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Sealer{}, fmt.Errorf("secret: gcm: %w", err)
	}
	return Sealer{gcm: gcm}, nil
}

// Enabled reports whether this Sealer can actually seal.
func (s Sealer) Enabled() bool { return s.gcm != nil }

// Seal encrypts plaintext, returning nonce||ciphertext.
func (s Sealer) Seal(plaintext []byte) ([]byte, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secret: nonce: %w", err)
	}
	return s.gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// SealString is Seal for text credentials.
func (s Sealer) SealString(plaintext string) ([]byte, error) { return s.Seal([]byte(plaintext)) }

// Open reverses Seal.
func (s Sealer) Open(sealed []byte) ([]byte, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	if len(sealed) < s.gcm.NonceSize() {
		return nil, ErrCorrupt
	}
	nonce, ct := sealed[:s.gcm.NonceSize()], sealed[s.gcm.NonceSize():]
	plaintext, err := s.gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrCorrupt
	}
	return plaintext, nil
}

// OpenString is Open for text credentials.
func (s Sealer) OpenString(sealed []byte) (string, error) {
	plaintext, err := s.Open(sealed)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
