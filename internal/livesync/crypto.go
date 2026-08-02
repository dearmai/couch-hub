// Package livesync reads obsidian-livesync's own storage format: the note
// entries, the chunks they are split into, and the several generations of
// encryption those chunks may carry.
//
// It exists so CouchHub can show what is actually in a vault. A note is not one
// document - it is an entry listing chunk ids, and with end-to-end encryption
// both its path and every chunk are ciphertext, so nothing is readable without
// reproducing the plugin's crypto.
//
// Formats, by the prefix the plugin writes:
//
//	%$  HKDF with an ephemeral PBKDF2 salt carried inline (current)
//	%=  HKDF with a PBKDF2 salt stored in the vault's sync-parameters document
//	%~  V3: PBKDF2 over the passphrase with a salt derived from it
//	%   V2: PBKDF2 over the passphrase digest, with an inline salt
//	[   V1: unsupported - reported rather than guessed at
package livesync

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Prefixes the plugin uses to tag each format.
const (
	PrefixHKDFEphemeral = "%$"
	PrefixHKDF          = "%="
	PrefixV3            = "%~"
	PrefixV2            = "%"
	PrefixV1            = "["

	// PrefixEncryptedMeta marks a path field that is not a path at all but an
	// encrypted bundle of the entry's metadata. From ENCRYPTED_META_PREFIX in
	// livesync-commonlib/pouchdb/encryption.
	PrefixEncryptedMeta = `/\:`
)

const (
	ivLen         = 12
	hkdfSaltLen   = 32
	pbkdf2SaltLen = 32
	aesKeyLen     = 32

	// hkdfIterations is PBKDF2_ITERATIONS in octagonal-wheels/encryption/hkdf.js.
	hkdfIterations = 310000
	// legacyIterations is the fixed count V2 and V3 use.
	legacyIterations = 100000
)

// v3Salt is the fixed salt V3 mixes into the passphrase before deriving its own.
var v3Salt = []byte("fancySyncForYou!")

var (
	// ErrUnsupportedFormat marks ciphertext in a format this package will not
	// guess at, rather than returning plausible nonsense.
	ErrUnsupportedFormat = errors.New("livesync: unsupported encryption format")
	// ErrWrongPassphrase marks a failed authentication tag, which in practice
	// always means the passphrase is wrong.
	ErrWrongPassphrase = errors.New("livesync: wrong passphrase")
	// ErrSaltRequired marks a %= blob decrypted without the vault's stored salt.
	ErrSaltRequired = errors.New("livesync: this vault's sync-parameters document is required to decrypt it")
)

// KeyCache memoises PBKDF2 master keys.
//
// Every decryption starts with PBKDF2 at 310,000 iterations, which takes a
// noticeable fraction of a second. The salt is per-vault for %= and per-session
// for %$, so the same key is derived over and over - once per chunk, once per
// path. Without this, listing a few hundred notes takes minutes; with it, the
// cost is paid once.
//
// Safe for concurrent use. Bounded so a vault that really does use a fresh salt
// per blob cannot grow it without limit.
type KeyCache struct {
	mu   sync.Mutex
	keys map[string][]byte
}

const keyCacheLimit = 16

func NewKeyCache() *KeyCache { return &KeyCache{keys: make(map[string][]byte, keyCacheLimit)} }

func (c *KeyCache) derive(passphrase string, salt []byte) ([]byte, error) {
	if c == nil {
		return pbkdf2.Key(sha256.New, passphrase, salt, hkdfIterations, aesKeyLen)
	}

	cacheKey := passphrase + "\x00" + hex.EncodeToString(salt)

	c.mu.Lock()
	if key, ok := c.keys[cacheKey]; ok {
		c.mu.Unlock()
		return key, nil
	}
	c.mu.Unlock()

	key, err := pbkdf2.Key(sha256.New, passphrase, salt, hkdfIterations, aesKeyLen)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if len(c.keys) >= keyCacheLimit {
		// Plain eviction: entries are interchangeable and a vault only ever has
		// a couple of live salts, so which one goes does not matter.
		for k := range c.keys {
			delete(c.keys, k)
			break
		}
	}
	c.keys[cacheKey] = key
	c.mu.Unlock()

	return key, nil
}

// DecryptOptions carries what the per-vault formats need beyond the passphrase.
type DecryptOptions struct {
	// Keys memoises the PBKDF2 step. Optional; nil derives every time.
	Keys *KeyCache

	// PBKDF2Salt is the salt from the vault's sync-parameters document, needed
	// only by the %= format.
	PBKDF2Salt []byte
	// DynamicIterations mirrors the plugin's useDynamicIterationCount setting,
	// which changes V1/V2's iteration count based on passphrase length.
	DynamicIterations bool
}

// DecryptString decrypts one of the plugin's encrypted strings.
func DecryptString(blob, passphrase string, opts DecryptOptions) (string, error) {
	switch {
	case strings.HasPrefix(blob, PrefixHKDFEphemeral):
		return decryptHKDFEphemeral(blob, passphrase, opts.Keys)
	case strings.HasPrefix(blob, PrefixHKDF):
		if len(opts.PBKDF2Salt) == 0 {
			return "", ErrSaltRequired
		}
		return decryptHKDF(blob, passphrase, opts.PBKDF2Salt, opts.Keys)
	case strings.HasPrefix(blob, PrefixV3):
		return decryptV3(blob, passphrase)
	case strings.HasPrefix(blob, PrefixV2):
		return decryptV2(blob, passphrase, opts.DynamicIterations)
	case strings.HasPrefix(blob, PrefixV1):
		return "", fmt.Errorf("%w: V1 (legacy array form)", ErrUnsupportedFormat)
	default:
		return "", fmt.Errorf("%w: no recognised prefix", ErrUnsupportedFormat)
	}
}

// IsEncrypted reports whether a value carries one of the plugin's prefixes.
// A vault with encryption turned off stores its chunks verbatim.
func IsEncrypted(s string) bool {
	return strings.HasPrefix(s, "%") || strings.HasPrefix(s, PrefixV1)
}

// decryptHKDFEphemeral reads the current format:
//
//	"%$" + base64( pbkdf2Salt[32] | iv[12] | hkdfSalt[32] | AES-GCM(ct||tag) )
func decryptHKDFEphemeral(blob, passphrase string, keys *KeyCache) (string, error) {
	raw, err := decodeBase64(blob[len(PrefixHKDFEphemeral):])
	if err != nil {
		return "", err
	}
	if len(raw) < pbkdf2SaltLen+ivLen+hkdfSaltLen {
		return "", fmt.Errorf("livesync: %s blob is too short", PrefixHKDFEphemeral)
	}
	return openHKDF(passphrase, keys,
		raw[:pbkdf2SaltLen],
		raw[pbkdf2SaltLen:pbkdf2SaltLen+ivLen],
		raw[pbkdf2SaltLen+ivLen:pbkdf2SaltLen+ivLen+hkdfSaltLen],
		raw[pbkdf2SaltLen+ivLen+hkdfSaltLen:])
}

// decryptHKDF reads the format whose PBKDF2 salt lives in sync-parameters:
//
//	"%=" + base64( iv[12] | hkdfSalt[32] | AES-GCM(ct||tag) )
func decryptHKDF(blob, passphrase string, pbkdf2Salt []byte, keys *KeyCache) (string, error) {
	raw, err := decodeBase64(blob[len(PrefixHKDF):])
	if err != nil {
		return "", err
	}
	if len(raw) < ivLen+hkdfSaltLen {
		return "", fmt.Errorf("livesync: %s blob is too short", PrefixHKDF)
	}
	return openHKDF(passphrase, keys, pbkdf2Salt, raw[:ivLen], raw[ivLen:ivLen+hkdfSaltLen], raw[ivLen+hkdfSaltLen:])
}

func openHKDF(passphrase string, keys *KeyCache, pbkdf2Salt, iv, hkdfSalt, ct []byte) (string, error) {
	master, err := keys.derive(passphrase, pbkdf2Salt)
	if err != nil {
		return "", fmt.Errorf("livesync: pbkdf2: %w", err)
	}
	key, err := hkdf.Key(sha256.New, master, hkdfSalt, "", aesKeyLen)
	if err != nil {
		return "", fmt.Errorf("livesync: hkdf: %w", err)
	}
	return openGCM(key, iv, ct)
}

// decryptV3 reads "%~" + ivHex(24) + base64(ct).
//
// The key comes from PBKDF2 over the passphrase bytes, salted with the first 16
// bytes of SHA-256(passphrase || "fancySyncForYou!").
func decryptV3(blob, passphrase string) (string, error) {
	body := blob[len(PrefixV3):]
	if len(body) < 24 {
		return "", fmt.Errorf("livesync: %s blob is too short", PrefixV3)
	}
	iv, err := hex.DecodeString(body[:24])
	if err != nil {
		return "", fmt.Errorf("livesync: %s iv: %w", PrefixV3, err)
	}
	ct, err := decodeBase64(body[24:])
	if err != nil {
		return "", err
	}

	digest := sha256.Sum256(append([]byte(passphrase), v3Salt...))
	key, err := pbkdf2.Key(sha256.New, passphrase, digest[:16], legacyIterations, aesKeyLen)
	if err != nil {
		return "", fmt.Errorf("livesync: pbkdf2: %w", err)
	}
	return openGCM(key, iv, ct)
}

// decryptV2 reads "%" + ivHex(32) + saltHex(32) + base64(ct).
//
// Its key derivation differs from V3: PBKDF2 runs over the SHA-256 digest of
// the passphrase rather than the passphrase itself.
func decryptV2(blob, passphrase string, dynamicIterations bool) (string, error) {
	body := blob[len(PrefixV2):]
	if len(body) < 64 {
		return "", fmt.Errorf("livesync: %s blob is too short", PrefixV2)
	}
	iv, err := hex.DecodeString(body[:32])
	if err != nil {
		return "", fmt.Errorf("livesync: %s iv: %w", PrefixV2, err)
	}
	salt, err := hex.DecodeString(body[32:64])
	if err != nil {
		return "", fmt.Errorf("livesync: %s salt: %w", PrefixV2, err)
	}

	payload := body[64:]
	if strings.HasPrefix(payload, "%") {
		// The plugin's own binary codec, used for large binary chunks. Reading it
		// would be guesswork here, and a wrong guess renders as garbage.
		return "", fmt.Errorf("%w: V2 with the binary codec", ErrUnsupportedFormat)
	}
	ct, err := decodeBase64(payload)
	if err != nil {
		return "", err
	}

	key, err := legacyKey(passphrase, salt, dynamicIterations)
	if err != nil {
		return "", err
	}
	return openGCM(key, iv, ct)
}

// legacyKey reproduces getKeyForDecryption: PBKDF2 over SHA-256(passphrase).
func legacyKey(passphrase string, salt []byte, dynamicIterations bool) ([]byte, error) {
	iterations := legacyIterations
	if dynamicIterations {
		// (15 - len) * 1000 + 121 - len, with the multiplier floored at zero.
		//
		// len is JavaScript's String#length - UTF-16 code units, not bytes. A
		// non-Latin passphrase makes the two disagree, and the wrong iteration
		// count derives a different key with no error to show for it.
		remaining := 15 - utf16Len(passphrase)
		multiplier := remaining
		if multiplier < 0 {
			multiplier = 0
		}
		iterations = multiplier*1000 + 121 - remaining
	}

	digest := sha256.Sum256([]byte(passphrase))
	key, err := pbkdf2.Key(sha256.New, string(digest[:]), salt, iterations, aesKeyLen)
	if err != nil {
		return nil, fmt.Errorf("livesync: pbkdf2: %w", err)
	}
	return key, nil
}

// utf16Len counts UTF-16 code units, matching JavaScript's String#length.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func openGCM(key, iv, ct []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("livesync: aes: %w", err)
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, len(iv))
	if err != nil {
		return "", fmt.Errorf("livesync: gcm: %w", err)
	}
	plain, err := gcm.Open(nil, iv, ct, nil)
	if err != nil {
		return "", ErrWrongPassphrase
	}
	return string(plain), nil
}

// decodeBase64 accepts padded and unpadded input; the plugin's encoders are not
// consistent about it across versions.
func decodeBase64(s string) ([]byte, error) {
	if raw, err := base64.StdEncoding.DecodeString(s); err == nil {
		return raw, nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, fmt.Errorf("livesync: base64: %w", err)
	}
	return raw, nil
}
