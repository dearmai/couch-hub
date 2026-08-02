// Package idgen produces short identifiers and random credentials.
package idgen

import (
	"crypto/rand"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const idAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// readableAlphabet excludes glyphs that are easy to confuse (0/O, 1/l/I),
// because these credentials get read off a screen and typed on a phone during
// device setup.
const readableAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// New returns an id like "vault-mfk2n1x-7q3wze".
//
// The timestamp prefix makes ids sort chronologically in bbolt, so listing a
// bucket yields creation order without a separate index. The random suffix
// covers ids minted within the same millisecond.
func New(prefix string) string {
	return fmt.Sprintf("%s-%s-%s", prefix, strconv.FormatInt(time.Now().UnixMilli(), 36), randomString(6, idAlphabet))
}

// Password returns a random password of the given length.
func Password(length int) string {
	if length <= 0 {
		length = 32
	}
	return randomString(length, readableAlphabet)
}

// randomString draws uniformly from alphabet using rejection sampling.
//
// The obvious `alphabet[b % len(alphabet)]` skews towards the first
// 256 % len(alphabet) characters, which measurably weakens generated
// credentials; discarding out-of-range bytes avoids that.
func randomString(n int, alphabet string) string {
	size := len(alphabet)
	// Largest multiple of size that fits in a byte; anything at or above it
	// would bias the result.
	limit := 256 - (256 % size)

	var b strings.Builder
	b.Grow(n)
	buf := make([]byte, n)
	for b.Len() < n {
		if _, err := rand.Read(buf); err != nil {
			// crypto/rand does not fail on supported platforms, and there is no
			// safe fallback for credential material if it ever does.
			panic("idgen: crypto/rand unavailable: " + err.Error())
		}
		for _, v := range buf {
			if int(v) >= limit {
				continue
			}
			b.WriteByte(alphabet[int(v)%size])
			if b.Len() == n {
				break
			}
		}
	}
	return b.String()
}
