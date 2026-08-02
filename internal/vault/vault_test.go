package vault

import (
	"testing"

	"github.com/dearmai/couch-hub/internal/setupuri"
)

func TestNormalizeDBName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"notes", "notes"},
		{"My Notes", "my_notes"},
		{"Work-Vault", "work-vault"},
		{"notes.2026", "notes_2026"},
		{"  spaced  ", "spaced"},
		// Must not start with a digit: CouchDB requires a leading lowercase letter.
		{"2026 journal", "vault_2026_journal"},
		// A leading underscore is stripped rather than prefixed: CouchDB reserves
		// that prefix, and "leading" is already a legal name.
		{"_leading", "leading"},
	}
	for _, c := range cases {
		got, err := NormalizeDBName(c.in)
		if err != nil {
			t.Errorf("NormalizeDBName(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeDBName(%q) = %q, want %q", c.in, got, c.want)
		}
		if err := ValidateDBName(got); err != nil {
			t.Errorf("NormalizeDBName(%q) produced an invalid name %q: %v", c.in, got, err)
		}
	}
}

// Non-Latin names are common for Obsidian vaults and have no legal CouchDB
// representation, so they fall back to a generated name. It must be unique:
// a fixed fallback would make the second Korean-named vault fail to create.
func TestNormalizeDBNameFallsBackUniquelyForNonLatinNames(t *testing.T) {
	seen := map[string]bool{}
	for _, in := range []string{"업무 노트", "업무 노트", "🔐", "私のノート", "заметки"} {
		got, err := NormalizeDBName(in)
		if err != nil {
			t.Fatalf("NormalizeDBName(%q): %v", in, err)
		}
		if err := ValidateDBName(got); err != nil {
			t.Errorf("NormalizeDBName(%q) = %q, which is not a legal name: %v", in, got, err)
		}
		if seen[got] {
			t.Errorf("NormalizeDBName(%q) = %q, which collides with an earlier name", in, got)
		}
		seen[got] = true
	}
}

func TestValidateDBName(t *testing.T) {
	valid := []string{"notes", "a", "vault_1", "a$b()c+d-e/f"}
	for _, name := range valid {
		if err := ValidateDBName(name); err != nil {
			t.Errorf("ValidateDBName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{
		"",       // empty
		"Notes",  // uppercase
		"1notes", // leading digit
		"_notes", // leading underscore
		"_users", // system database
		"_replicator",
		"note s", // space
		"note@s", // illegal character
		"노트",     // non-ASCII
	}
	for _, name := range invalid {
		if err := ValidateDBName(name); err == nil {
			t.Errorf("ValidateDBName(%q) = nil, want an error", name)
		}
	}
}

func TestNewCredentialsAreDistinct(t *testing.T) {
	a, err := NewCredentials("notes")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewCredentials("notes")
	if err != nil {
		t.Fatal(err)
	}

	if a.CouchUser != "vault_notes" {
		t.Errorf("CouchUser = %q", a.CouchUser)
	}
	if a.CouchPassword == b.CouchPassword {
		t.Error("two vaults were given the same CouchDB password")
	}
	if a.E2EEPassphrase == b.E2EEPassphrase {
		t.Error("two vaults were given the same E2EE passphrase")
	}
	// Reusing the server password as the E2EE passphrase would hand the vault
	// contents to whoever holds the database credentials.
	if a.E2EEPassphrase == a.CouchPassword {
		t.Error("E2EE passphrase must differ from the CouchDB password")
	}
	if len(a.SetupPIN) != 6 {
		t.Errorf("SetupPIN = %q, want 6 digits", a.SetupPIN)
	}
}

func TestBuildSetupURI(t *testing.T) {
	creds, err := NewCredentials("notes")
	if err != nil {
		t.Fatal(err)
	}
	if err := creds.BuildSetupURI("https://sync.example.com", "notes", false); err != nil {
		t.Fatal(err)
	}

	if creds.SetupURI == "" {
		t.Fatal("SetupURI is empty")
	}
	if creds.QRSVG == "" {
		t.Fatalf("QRSVG is empty (%s)", creds.QRError)
	}

	// The PIN-protected form must not expose anything without the PIN.
	for _, secret := range []string{creds.CouchPassword, creds.E2EEPassphrase, creds.SetupPIN} {
		if containsPlaintext(creds.SetupURI, secret) {
			t.Error("the encrypted Setup URI leaks a secret in plaintext")
		}
	}

	// The plain form deliberately does the opposite: the client reads the
	// passphrase straight out of it, which is what removes the PIN prompt. This
	// asserts the trade-off is real rather than assumed.
	if creds.PlainSetupURI == "" {
		t.Fatal("PlainSetupURI is empty")
	}
	if creds.PlainQRSVG == "" {
		t.Fatalf("PlainQRSVG is empty (%s)", creds.PlainQRError)
	}
	for _, secret := range []string{creds.CouchPassword, creds.E2EEPassphrase} {
		if !containsPlaintext(creds.PlainSetupURI, secret) {
			t.Error("the plain Setup URI should carry the credentials readably; it did not")
		}
	}

	// It is also the smaller of the two, which is why its QR scans more easily.
	if len(creds.PlainSetupURI) >= len(creds.SetupURI) {
		t.Errorf("plain URI (%d) should be shorter than the encrypted one (%d)",
			len(creds.PlainSetupURI), len(creds.SetupURI))
	}
}

// Adopting a database that already stores plaintext must produce settings that
// say so, rather than quietly claiming encryption the data does not have.
func TestBuildSetupURIWithE2EEDisabled(t *testing.T) {
	creds, err := NewCredentials("legacy")
	if err != nil {
		t.Fatal(err)
	}
	creds.E2EEPassphrase = ""

	if err := creds.BuildSetupURI("https://sync.example.com", "legacy", true); err != nil {
		t.Fatal(err)
	}

	settings, err := setupuri.Parse(creds.SetupURI, creds.SetupPIN)
	if err != nil {
		t.Fatal(err)
	}
	if settings["encrypt"] != false {
		t.Errorf("encrypt = %v, want false", settings["encrypt"])
	}
	// Path obfuscation hashes filenames with the same passphrase, so it cannot
	// stay on without one.
	if settings["usePathObfuscation"] != false {
		t.Errorf("usePathObfuscation = %v, want false", settings["usePathObfuscation"])
	}
}

func containsPlaintext(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	return len(haystack) >= len(needle) && stringContains(haystack, needle)
}

func stringContains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
