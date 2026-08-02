package setupuri

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// configURIBase is the prefix Obsidian's protocol handler matches on.
// From livesync-commonlib common/models/shared.const.js.
const configURIBase = "obsidian://setuplivesync?settings="

// Build returns the Setup URI for a vault, encrypted under uriPassphrase.
//
// uriPassphrase protects the URI in transit only; it is unrelated to
// VaultConfig.E2EEPassphrase, which protects the vault contents on the server.
// An empty uriPassphrase is accepted by the plugin (the user just confirms the
// empty prompt) but leaves the CouchDB credentials readable to anyone who sees
// the URI or QR code.
func Build(cfg VaultConfig, uriPassphrase string) (string, error) {
	settings, err := BuildSettings(cfg)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("setupuri: marshal settings: %w", err)
	}
	blob, err := Encrypt(string(payload), uriPassphrase)
	if err != nil {
		return "", err
	}
	// The library appends a trailing space here and the official generator trims
	// it back off before display, so the canonical URI carries no trailing space.
	return configURIBase + encodeURIComponent(blob), nil
}

// Parse reverses Build, for importing a vault that was configured elsewhere.
func Parse(uri, uriPassphrase string) (map[string]any, error) {
	trimmed := strings.TrimSpace(uri)
	if !strings.HasPrefix(trimmed, configURIBase) {
		return nil, fmt.Errorf("setupuri: not a setup URI (expected prefix %q)", configURIBase)
	}
	blob, err := decodeURIComponent(trimmed[len(configURIBase):])
	if err != nil {
		return nil, err
	}
	plaintext, err := Decrypt(blob, uriPassphrase)
	if err != nil {
		return nil, err
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(plaintext), &settings); err != nil {
		return nil, fmt.Errorf("setupuri: decrypted payload is not JSON: %w", err)
	}
	return settings, nil
}

// ErrQRTooLarge reports a Setup URI that exceeds QR version 40 capacity.
//
// Realistically this only happens with an extremely long hostname combined with
// long credentials; a typical URI is around 1.4 KB against a ~2.9 KB ceiling.
// Callers should fall back to offering the URI as copyable text.
var ErrQRTooLarge = fmt.Errorf("setupuri: setup URI does not fit in a QR code")

// QRSVG renders a Setup URI as a standalone SVG document.
//
// The QR encodes the *encrypted* URI, so scanning it is equivalent to clicking
// the link: Obsidian's protocol handler prompts for the passphrase either way.
// This is deliberately not livesync's own `?settingsQR=` parameter, which packs
// the settings into an unencrypted positional array - that would put the CouchDB
// password and the E2EE passphrase in the QR image in the clear, and would tie
// us to an index mapping that shifts whenever the plugin gains a setting.
func QRSVG(uri string, moduleSize int) (string, error) {
	if moduleSize <= 0 {
		moduleSize = 4
	}
	// Medium recovery still leaves headroom at realistic URI lengths and
	// tolerates a scuffed screen or a bad camera angle better than Low.
	qr, err := qrcode.New(uri, qrcode.Medium)
	if err != nil {
		return "", fmt.Errorf("%w: %d bytes", ErrQRTooLarge, len(uri))
	}
	return bitmapToSVG(qr.Bitmap(), moduleSize), nil
}

// bitmapToSVG emits one <rect> per dark module. Runs of adjacent dark modules
// are merged into a single rect, which roughly halves the output size.
func bitmapToSVG(bitmap [][]bool, moduleSize int) string {
	n := len(bitmap)
	side := n * moduleSize

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="Setup URI QR code">`,
		side, side, side, side)
	b.WriteString(`<rect width="100%" height="100%" fill="#ffffff"/>`)
	b.WriteString(`<g fill="#000000">`)
	for y := 0; y < n; y++ {
		row := bitmap[y]
		for x := 0; x < len(row); x++ {
			if !row[x] {
				continue
			}
			run := 1
			for x+run < len(row) && row[x+run] {
				run++
			}
			fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d"/>`,
				x*moduleSize, y*moduleSize, run*moduleSize, moduleSize)
			x += run - 1
		}
	}
	b.WriteString(`</g></svg>`)
	return b.String()
}

// decodeURIComponent reverses encodeURIComponent. Written by hand rather than
// via url.QueryUnescape because that treats '+' as a space, which would corrupt
// base64 payloads.
func decodeURIComponent(s string) (string, error) {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		if i+2 >= len(s) {
			return "", fmt.Errorf("setupuri: truncated percent-escape at offset %d", i)
		}
		hi, err1 := unhex(s[i+1])
		lo, err2 := unhex(s[i+2])
		if err1 != nil || err2 != nil {
			return "", fmt.Errorf("setupuri: invalid percent-escape %q at offset %d", s[i:i+3], i)
		}
		b.WriteByte(hi<<4 | lo)
		i += 2
	}
	return b.String(), nil
}

func unhex(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	}
	return 0, fmt.Errorf("not a hex digit: %q", c)
}

// GenerateSecret mirrors generateSecret() in the official Deno generator:
// 24 random bytes, base64, made URL-safe, unpadded.
func GenerateSecret() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("setupuri: random secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// GeneratePIN returns a numeric PIN of the given length, for protecting a Setup
// URI that is handed over as a QR code on a trusted screen.
//
// A short PIN is weak against an attacker who has captured the URI and can
// grind it offline - PBKDF2 at 310k iterations is the only thing slowing them
// down. It is meant for the homelab case where the QR is displayed briefly on
// the operator's own screen; anything reaching an untrusted channel should use
// GenerateSecret instead.
func GeneratePIN(digits int) (string, error) {
	if digits <= 0 {
		digits = 6
	}
	var b strings.Builder
	b.Grow(digits)
	for i := 0; i < digits; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", fmt.Errorf("setupuri: random pin: %w", err)
		}
		b.WriteByte(byte('0' + n.Int64()))
	}
	return b.String(), nil
}
