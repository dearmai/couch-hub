package setupuri

import "strings"

// Go's net/url escaping does not match either of the two JavaScript escapers
// livesync relies on, so both are reproduced here.
//
//   - url.QueryEscape encodes space as '+' and escapes '~', which
//     encodeURIComponent does not.
//   - url.PathEscape leaves '/', '$', '&', '+', ',', ':', ';', '=', '?', '@'
//     alone, which encodeURIComponent does not.
//
// Getting either wrong produces a URI Obsidian silently fails to import, so
// these are table-tested against the real JS in setupuri_test.go.

const upperhex = "0123456789ABCDEF"

// encodeURIComponent matches the ECMAScript function of the same name.
// Unescaped set: A-Z a-z 0-9 and - _ . ! ~ * ' ( )
func encodeURIComponent(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isURIComponentUnreserved(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upperhex[c>>4])
		b.WriteByte(upperhex[c&0xF])
	}
	return b.String()
}

func isURIComponentUnreserved(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '-', '_', '.', '!', '~', '*', '\'', '(', ')':
		return true
	}
	return false
}

// encodeFormURLComponent matches the WHATWG application/x-www-form-urlencoded
// serializer, which is what URLSearchParams.set() uses.
//
// Unescaped set: A-Z a-z 0-9 and * - . _ ; space becomes '+'.
//
// This matters because CouchDB database names may legally contain '+', '/',
// '(', ')' and '$', all of which this escaper encodes but encodeURIComponent
// would (for some of them) leave alone.
func encodeFormURLComponent(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '*', c == '-', c == '.', c == '_':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&0xF])
		}
	}
	return b.String()
}
