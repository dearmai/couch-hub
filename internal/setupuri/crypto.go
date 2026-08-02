// Package setupuri reproduces obsidian-livesync's Setup URI format so CouchHub
// can hand a freshly created vault straight to the Obsidian client.
//
// The wire format is defined by octagonal-wheels/encryption/hkdf.js and
// @vrtmrz/livesync-commonlib/API/processSetting.js. Every constant here mirrors
// those files; scripts/verify-setup-uri.mjs proves the reproduction still matches
// the published library, and `make verify-uri` runs it.
package setupuri

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Prefixes used by livesync to tag which encryption format a blob uses.
// We only ever *write* hkdfSaltedPrefix; the others exist so an existing Setup
// URI produced by an older plugin can still be imported.
const (
	hkdfSaltedPrefix = "%$" // HKDF_SALTED_ENCRYPTED_PREFIX - current format
	encryptV3Prefix  = "%~" // ENCRYPT_V3_PREFIX
	encryptV2Prefix  = "%"  // ENCRYPT_V2_PREFIX
	encryptV1Prefix  = "["  // ENCRYPT_V1_PREFIX_PROBABLY
)

// Parameters of the HKDF-salted format. See octagonal-wheels/encryption/hkdf.js.
const (
	pbkdf2Iterations = 310000 // PBKDF2_ITERATIONS
	pbkdf2SaltLen    = 32     // PBKDF2_SALT_LENGTH
	hkdfSaltLen      = 32     // HKDF_SALT_LENGTH
	ivLen            = 12     // IV_LENGTH
	aesKeyLen        = 32     // AES-256
)

// Encrypt produces a livesync "%$" blob.
//
// Layout, matching encryptWithEphemeralSaltBinary():
//
//	"%$" + base64( pbkdf2Salt[32] || iv[12] || hkdfSalt[32] || AES-GCM(ct||tag) )
//
// The key schedule is PBKDF2-HMAC-SHA256(passphrase, pbkdf2Salt, 310000) to a
// 256-bit master key, then HKDF-SHA256(master, hkdfSalt, info="") to the AES key.
//
// Unlike the reference implementation we generate a fresh pbkdf2Salt per call
// rather than caching one per session. The format carries the salt inline, so
// this is compatible - it only costs one extra PBKDF2 derivation per Setup URI,
// which is irrelevant at our call rate and avoids sharing a salt across vaults.
func Encrypt(plaintext, passphrase string) (string, error) {
	pbkdf2Salt := make([]byte, pbkdf2SaltLen)
	hkdfSalt := make([]byte, hkdfSaltLen)
	iv := make([]byte, ivLen)
	for _, b := range [][]byte{pbkdf2Salt, hkdfSalt, iv} {
		if _, err := rand.Read(b); err != nil {
			return "", fmt.Errorf("setupuri: random: %w", err)
		}
	}

	gcm, err := deriveGCM(passphrase, pbkdf2Salt, hkdfSalt)
	if err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, iv, []byte(plaintext), nil)

	buf := make([]byte, 0, pbkdf2SaltLen+ivLen+hkdfSaltLen+len(ct))
	buf = append(buf, pbkdf2Salt...)
	buf = append(buf, iv...)
	buf = append(buf, hkdfSalt...)
	buf = append(buf, ct...)

	return hkdfSaltedPrefix + base64.StdEncoding.EncodeToString(buf), nil
}

// Decrypt reverses Encrypt. Only the current "%$" format is supported; the
// legacy formats are detected so the caller can report something better than
// "corrupt data" when a user pastes an old Setup URI.
func Decrypt(blob, passphrase string) (string, error) {
	if !strings.HasPrefix(blob, hkdfSaltedPrefix) {
		switch {
		case strings.HasPrefix(blob, encryptV3Prefix),
			strings.HasPrefix(blob, encryptV1Prefix),
			strings.HasPrefix(blob, encryptV2Prefix):
			return "", ErrLegacyFormat
		}
		return "", errors.New("setupuri: unrecognised encryption format")
	}

	raw, err := base64.StdEncoding.DecodeString(blob[len(hkdfSaltedPrefix):])
	if err != nil {
		return "", fmt.Errorf("setupuri: base64: %w", err)
	}
	if len(raw) < pbkdf2SaltLen+ivLen+hkdfSaltLen {
		return "", errors.New("setupuri: blob too short")
	}

	pbkdf2Salt := raw[:pbkdf2SaltLen]
	iv := raw[pbkdf2SaltLen : pbkdf2SaltLen+ivLen]
	hkdfSalt := raw[pbkdf2SaltLen+ivLen : pbkdf2SaltLen+ivLen+hkdfSaltLen]
	ct := raw[pbkdf2SaltLen+ivLen+hkdfSaltLen:]

	gcm, err := deriveGCM(passphrase, pbkdf2Salt, hkdfSalt)
	if err != nil {
		return "", err
	}
	pt, err := gcm.Open(nil, iv, ct, nil)
	if err != nil {
		return "", ErrWrongPassphrase
	}
	return string(pt), nil
}

// ErrLegacyFormat reports a Setup URI encrypted with a pre-HKDF livesync format.
var ErrLegacyFormat = errors.New("setupuri: legacy encryption format (pre-HKDF), not supported")

// ErrWrongPassphrase reports a failed GCM tag check, which in practice always
// means the supplied passphrase was wrong.
var ErrWrongPassphrase = errors.New("setupuri: wrong passphrase")

func deriveGCM(passphrase string, pbkdf2Salt, hkdfSalt []byte) (cipher.AEAD, error) {
	master, err := pbkdf2.Key(sha256.New, passphrase, pbkdf2Salt, pbkdf2Iterations, aesKeyLen)
	if err != nil {
		return nil, fmt.Errorf("setupuri: pbkdf2: %w", err)
	}
	// info is deliberately empty - deriveKey() in hkdf.js passes `new Uint8Array()`.
	key, err := hkdf.Key(sha256.New, master, hkdfSalt, "", aesKeyLen)
	if err != nil {
		return nil, fmt.Errorf("setupuri: hkdf: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("setupuri: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("setupuri: gcm: %w", err)
	}
	return gcm, nil
}
