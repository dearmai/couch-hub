package setupuri

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Expected values captured from a real JS engine:
//
//	encodeURIComponent(s)
//	new URLSearchParams([["db", s]]).toString().slice(3)
//
// scripts/verify-setup-uri.mjs re-proves these end to end against the library,
// but keeping the table here means `go test` alone catches an escaping mistake.

func TestEncodeURIComponent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"abc123", "abc123"},
		{"-_.!~*'()", "-_.!~*'()"}, // the full unreserved punctuation set
		{"a b/c?d=e&f#g", "a%20b%2Fc%3Fd%3De%26f%23g"},
		{"%", "%25"},
		{"한글", "%ED%95%9C%EA%B8%80"},
		{"🔐", "%F0%9F%94%90"},
		{"+/=", "%2B%2F%3D"},
		{"p@ss:w#rd?&=+% ok", "p%40ss%3Aw%23rd%3F%26%3D%2B%25%20ok"},
	}
	for _, c := range cases {
		if got := encodeURIComponent(c.in); got != c.want {
			t.Errorf("encodeURIComponent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEncodeFormURLComponent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"abc123", "abc123"},
		// Diverges from encodeURIComponent here: only * - . _ stay literal.
		{"-_.!~*'()", "-_.%21%7E*%27%28%29"},
		{"a b/c?d=e&f#g", "a+b%2Fc%3Fd%3De%26f%23g"},
		{"%", "%25"},
		{"한글", "%ED%95%9C%EA%B8%80"},
		{"🔐", "%F0%9F%94%90"},
		{"+/=", "%2B%2F%3D"},
		{"p@ss:w#rd?&=+% ok", "p%40ss%3Aw%23rd%3F%26%3D%2B%25+ok"},
	}
	for _, c := range cases {
		if got := encodeFormURLComponent(c.in); got != c.want {
			t.Errorf("encodeFormURLComponent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEncodeDecodeURIComponentRoundTrip(t *testing.T) {
	for _, s := range []string{"", "plain", "%$abc+/=", "한글 🔐", "a%20b"} {
		got, err := decodeURIComponent(encodeURIComponent(s))
		if err != nil {
			t.Fatalf("decodeURIComponent(encodeURIComponent(%q)): %v", s, err)
		}
		if got != s {
			t.Errorf("round trip of %q gave %q", s, got)
		}
	}
}

func TestDecodeURIComponentRejectsBadEscapes(t *testing.T) {
	for _, s := range []string{"%", "%2", "%zz", "abc%g0"} {
		if _, err := decodeURIComponent(s); err == nil {
			t.Errorf("decodeURIComponent(%q) succeeded, want error", s)
		}
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	// Non-ASCII and an empty passphrase both have to work: the plugin accepts an
	// empty prompt, and vault passphrases are frequently non-Latin.
	cases := []struct{ plaintext, passphrase string }{
		{`{"a":1}`, "482917"},
		{`{"a":1}`, ""},
		{"한국어 패스프레이즈 ✔️ 𠮷", "암호🔐"},
		{strings.Repeat("x", 64*1024), "long-payload"},
	}
	for _, c := range cases {
		blob, err := Encrypt(c.plaintext, c.passphrase)
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		if !strings.HasPrefix(blob, hkdfSaltedPrefix) {
			t.Fatalf("blob %.8q does not start with %q", blob, hkdfSaltedPrefix)
		}
		got, err := Decrypt(blob, c.passphrase)
		if err != nil {
			t.Fatalf("Decrypt: %v", err)
		}
		if got != c.plaintext {
			t.Errorf("round trip changed the payload (%d -> %d bytes)", len(c.plaintext), len(got))
		}
	}
}

func TestEncryptUsesFreshSaltsAndIV(t *testing.T) {
	a, err := Encrypt("same", "pw")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Encrypt("same", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two encryptions of the same plaintext produced identical blobs; salt or IV is being reused")
	}
}

func TestDecryptWrongPassphrase(t *testing.T) {
	blob, err := Encrypt(`{"secret":true}`, "right")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(blob, "wrong"); !errors.Is(err, ErrWrongPassphrase) {
		t.Errorf("got %v, want ErrWrongPassphrase", err)
	}
}

func TestDecryptLegacyFormatsAreReported(t *testing.T) {
	for _, blob := range []string{"%~abc", "%abc", "[abc]"} {
		if _, err := Decrypt(blob, "pw"); !errors.Is(err, ErrLegacyFormat) {
			t.Errorf("Decrypt(%q) = %v, want ErrLegacyFormat", blob, err)
		}
	}
}

func TestSerializeCouchDB(t *testing.T) {
	cases := []struct {
		name                          string
		uri, user, pass, db           string
		wantConnURI, wantNormalisedDB string
	}{
		{
			name: "host and port",
			uri:  "http://192.168.1.50:5984", user: "u", pass: "p", db: "notes",
			wantConnURI:      "sls+http://u:p@192.168.1.50:5984/?db=notes",
			wantNormalisedDB: "http://192.168.1.50:5984",
		},
		{
			name: "trailing slash dropped from couchDB_URI",
			uri:  "https://sync.example.com/", user: "u", pass: "p", db: "v",
			wantConnURI:      "sls+https://u:p@sync.example.com/?db=v",
			wantNormalisedDB: "https://sync.example.com",
		},
		{
			name: "https default port stripped",
			uri:  "https://sync.example.com:443", user: "u", pass: "p", db: "v",
			wantConnURI:      "sls+https://u:p@sync.example.com/?db=v",
			wantNormalisedDB: "https://sync.example.com",
		},
		{
			name: "host lowercased",
			uri:  "https://SYNC.Example.COM:5984", user: "u", pass: "p", db: "v",
			wantConnURI:      "sls+https://u:p@sync.example.com:5984/?db=v",
			wantNormalisedDB: "https://sync.example.com:5984",
		},
		{
			name: "subpath preserved",
			uri:  "https://example.com/couchdb", user: "u", pass: "p", db: "v",
			wantConnURI:      "sls+https://u:p@example.com/couchdb?db=v",
			wantNormalisedDB: "https://example.com/couchdb",
		},
		{
			name: "credentials percent-encoded, db form-encoded",
			uri:  "http://couch.local:5984", user: "a b", pass: "p@ss", db: "v+w",
			wantConnURI:      "sls+http://a%20b:p%40ss@couch.local:5984/?db=v%2Bw",
			wantNormalisedDB: "http://couch.local:5984",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := serializeCouchDB(c.uri, c.user, c.pass, c.db)
			if err != nil {
				t.Fatalf("serializeCouchDB: %v", err)
			}
			if got.URI != c.wantConnURI {
				t.Errorf("connection string\n got %q\nwant %q", got.URI, c.wantConnURI)
			}
			if got.NormalisedCouchDBURI != c.wantNormalisedDB {
				t.Errorf("normalised couchDB_URI\n got %q\nwant %q", got.NormalisedCouchDBURI, c.wantNormalisedDB)
			}
		})
	}
}

func TestSerializeCouchDBRejectsRelativeURI(t *testing.T) {
	for _, uri := range []string{"", "sync.example.com:5984", "/couchdb"} {
		if _, err := serializeCouchDB(uri, "u", "p", "v"); err == nil {
			t.Errorf("serializeCouchDB(%q) succeeded, want error", uri)
		}
	}
}

func validConfig() VaultConfig {
	return VaultConfig{
		CouchDBURI:     "https://sync.example.com",
		User:           "vault_notes",
		Password:       "server-password",
		DBName:         "notes",
		E2EEPassphrase: "vault-passphrase",
	}
}

func TestBuildSettingsFillsEveryPlaceholder(t *testing.T) {
	settings, err := BuildSettings(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "@@COUCHHUB_") {
		t.Fatalf("placeholder survived into the settings object: %s", raw)
	}

	// remoteType must stay absent - it is "" for CouchDB, which the official
	// generator prunes as a default. Emitting it would diverge from the reference.
	if _, ok := settings["remoteType"]; ok {
		t.Error("remoteType should be pruned, not emitted")
	}

	id, _ := settings["activeConfigurationId"].(string)
	configs, _ := settings["remoteConfigurations"].(map[string]any)
	if id == "" || configs[id] == nil {
		t.Errorf("activeConfigurationId %q does not point at a remote configuration", id)
	}
}

func TestBuildSettingsRejectsPassphraseEqualToPassword(t *testing.T) {
	// The library's own docs call this out: reusing the server password as the
	// E2EE passphrase means the server operator holds the decryption key.
	cfg := validConfig()
	cfg.E2EEPassphrase = cfg.Password
	if _, err := BuildSettings(cfg); err == nil {
		t.Error("expected an error when the passphrase equals the CouchDB password")
	}
}

func TestBuildSettingsRequiresEveryField(t *testing.T) {
	for _, mutate := range []func(*VaultConfig){
		func(c *VaultConfig) { c.CouchDBURI = "" },
		func(c *VaultConfig) { c.User = "" },
		func(c *VaultConfig) { c.Password = "" },
		func(c *VaultConfig) { c.DBName = "" },
		func(c *VaultConfig) { c.E2EEPassphrase = "" },
	} {
		cfg := validConfig()
		mutate(&cfg)
		if _, err := BuildSettings(cfg); err == nil {
			t.Errorf("expected an error for config %+v", cfg)
		}
	}
}

func TestBuildAndParseRoundTrip(t *testing.T) {
	cfg := validConfig()
	uri, err := Build(cfg, "482917")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uri, configURIBase) {
		t.Fatalf("URI %.40q lacks the %q prefix", uri, configURIBase)
	}
	// The library appends a trailing space; the official generator trims it. Ours
	// must already be trimmed or the QR encodes a stray byte.
	if strings.TrimSpace(uri) != uri {
		t.Error("Setup URI has surrounding whitespace")
	}

	got, err := Parse(uri, "482917")
	if err != nil {
		t.Fatal(err)
	}
	if got["couchDB_PASSWORD"] != cfg.Password || got["passphrase"] != cfg.E2EEPassphrase {
		t.Error("credentials did not survive the round trip")
	}
	if got["couchDB_URI"] != "https://sync.example.com" {
		t.Errorf("couchDB_URI = %v", got["couchDB_URI"])
	}
}

func TestParseRejectsForeignURI(t *testing.T) {
	if _, err := Parse("https://example.com/not-a-setup-uri", "pw"); err == nil {
		t.Error("expected an error for a non-setuplivesync URI")
	}
}

func TestQRSVG(t *testing.T) {
	uri, err := Build(validConfig(), "482917")
	if err != nil {
		t.Fatal(err)
	}
	svg, err := QRSVG(uri, 4)
	if err != nil {
		t.Fatalf("QRSVG: %v", err)
	}
	if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") {
		t.Error("output is not a standalone SVG document")
	}
	if !strings.Contains(svg, "<rect") {
		t.Error("SVG contains no modules")
	}
}

func TestQRSVGTooLargeIsReported(t *testing.T) {
	// Well past QR version 40 capacity; callers fall back to copyable text.
	if _, err := QRSVG(strings.Repeat("a", 5000), 4); !errors.Is(err, ErrQRTooLarge) {
		t.Errorf("got %v, want ErrQRTooLarge", err)
	}
}

func TestGeneratedSecretsAreDistinctAndURLSafe(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		s, err := GenerateSecret()
		if err != nil {
			t.Fatal(err)
		}
		if seen[s] {
			t.Fatalf("GenerateSecret repeated %q", s)
		}
		seen[s] = true
		if strings.ContainsAny(s, "+/=") {
			t.Errorf("GenerateSecret returned non-URL-safe output %q", s)
		}
	}
}

func TestGeneratePIN(t *testing.T) {
	pin, err := GeneratePIN(6)
	if err != nil {
		t.Fatal(err)
	}
	if len(pin) != 6 {
		t.Fatalf("GeneratePIN(6) = %q", pin)
	}
	if strings.Trim(pin, "0123456789") != "" {
		t.Errorf("GeneratePIN produced a non-digit: %q", pin)
	}
}
