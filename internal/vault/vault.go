// Package vault creates and tears down the CouchDB side of an Obsidian vault:
// a database, a dedicated account limited to it, and the credentials that go
// into the client's Setup URI.
//
// Each vault gets its own CouchDB account rather than sharing the admin one, so
// a Setup URI that leaks costs one vault instead of the whole server.
package vault

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/dearmai/couch-hub/internal/couch"
	"github.com/dearmai/couch-hub/internal/idgen"
	"github.com/dearmai/couch-hub/internal/setupuri"
)

// dbNamePattern is CouchDB's rule for user-created databases: a lowercase
// letter followed by lowercase letters, digits, and _$()+-/
var dbNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_$()+/-]*$`)

// reservedDBNames are CouchDB's own databases, which a vault must never claim.
var reservedDBNames = map[string]bool{
	"_users": true, "_replicator": true, "_global_changes": true,
}

// NormalizeDBName derives a legal CouchDB database name from a display name.
//
// Obsidian vault names are free-form (spaces, Korean, emoji), while CouchDB
// accepts a narrow ASCII set, so the two cannot be the same string.
func NormalizeDBName(displayName string) (string, error) {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(displayName)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ', r == '.':
			b.WriteRune('_')
		default:
			// Anything outside CouchDB's alphabet - including non-Latin scripts -
			// is dropped rather than transliterated, so the result stays
			// predictable.
		}
	}
	name := strings.Trim(b.String(), "_-")

	switch {
	case name == "":
		// Nothing survived - a fully non-Latin name such as "업무 노트", which is
		// the common case here. A bare "vault" would collide the moment a second
		// such vault is created, so make it unique.
		name = "vault_" + strings.ToLower(idgen.Password(6))
	case name[0] < 'a' || name[0] > 'z':
		// CouchDB requires a leading lowercase letter; a name starting with a
		// digit needs a prefix.
		name = "vault_" + name
	}
	if len(name) > 200 {
		name = name[:200]
	}

	if err := ValidateDBName(name); err != nil {
		return "", err
	}
	return name, nil
}

// ValidateDBName checks an explicitly supplied database name.
func ValidateDBName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("데이터베이스 이름이 비어 있습니다")
	case reservedDBNames[name]:
		return fmt.Errorf("%q는 CouchDB 시스템 데이터베이스입니다", name)
	case strings.HasPrefix(name, "_"):
		return fmt.Errorf("데이터베이스 이름은 _ 로 시작할 수 없습니다")
	case !dbNamePattern.MatchString(name):
		return fmt.Errorf("데이터베이스 이름 %q는 소문자로 시작하고 a-z 0-9 _ $ ( ) + - / 만 쓸 수 있습니다", name)
	}
	return nil
}

// Credentials are everything needed to configure a client. They are returned
// once at creation and, when COUCHHUB_SECRET is set, can be reissued later.
//
// Two transfer formats are offered, because livesync supports both and they
// trade off differently:
//
//	SetupURI      encrypted with SetupPIN; the client prompts for the PIN
//	PlainSetupURI unencrypted; the client reads the passphrase straight from it
//
// Neither involves an in-app camera - the plugin has none. Both are scanned
// with the phone's camera app, which hands the obsidian:// URL to the plugin.
type Credentials struct {
	CouchUser      string `json:"couchUser"`
	CouchPassword  string `json:"couchPassword"`
	E2EEPassphrase string `json:"e2eePassphrase"`
	SetupPIN       string `json:"setupPin"`

	SetupURI string `json:"setupUri"`
	// QRSVG is empty when the URI does not fit in a QR code; the UI then offers
	// the URI as copyable text only.
	QRSVG string `json:"qrSvg"`
	// QRModules is the code's width in modules, quiet zone included. The UI
	// sizes itself from it: the same pixel width is a different physical module
	// size for every URI length, and a camera stops resolving modules well
	// before the code stops fitting on screen.
	QRModules int `json:"qrModules"`
	// QRError explains an empty QRSVG.
	QRError string `json:"qrError,omitempty"`

	// PlainSetupURI is livesync's `?settingsQR=` form. It carries the CouchDB
	// password and the E2EE passphrase in the clear, which is precisely what
	// removes the PIN prompt - and what makes a photographed code enough to take
	// over the vault.
	PlainSetupURI  string `json:"plainSetupUri"`
	PlainQRSVG     string `json:"plainQrSvg"`
	PlainQRModules int    `json:"plainQrModules"`
	PlainQRError   string `json:"plainQrError,omitempty"`
}

// NewCredentials mints the secrets for a vault without touching CouchDB.
func NewCredentials(dbName string) (Credentials, error) {
	passphrase, err := setupuri.GenerateSecret()
	if err != nil {
		return Credentials{}, err
	}
	pin, err := setupuri.GeneratePIN(6)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{
		// A per-vault account, prefixed so it is obvious in _users which vault
		// an account belongs to.
		CouchUser:      "vault_" + dbName,
		CouchPassword:  idgen.Password(32),
		E2EEPassphrase: passphrase,
		SetupPIN:       pin,
	}, nil
}

// BuildSetupURI fills in the Setup URI and QR for a set of credentials.
//
// publicBaseURL must be the address Obsidian can reach, not the one CouchHub
// administers through.
func (c *Credentials) BuildSetupURI(publicBaseURL, dbName string, e2eeDisabled bool) error {
	cfg := setupuri.VaultConfig{
		CouchDBURI:     publicBaseURL,
		User:           c.CouchUser,
		Password:       c.CouchPassword,
		DBName:         dbName,
		E2EEPassphrase: c.E2EEPassphrase,
		E2EEDisabled:   e2eeDisabled,
	}

	uri, err := setupuri.Build(cfg, c.SetupPIN)
	if err != nil {
		return err
	}
	c.SetupURI = uri
	c.QRSVG, c.QRModules, c.QRError = renderQR(uri)

	plain, err := setupuri.BuildQR(cfg)
	if err != nil {
		return err
	}
	c.PlainSetupURI = plain
	c.PlainQRSVG, c.PlainQRModules, c.PlainQRError = renderQR(plain)

	return nil
}

// renderQR returns the SVG and its module count, or an empty string and a
// reason. A URI too long for a QR is not fatal - it can still be copied as text.
func renderQR(uri string) (svg string, modules int, failure string) {
	out, err := setupuri.QRSVG(uri, 8)
	if err != nil {
		return "", 0, err.Error()
	}
	modules, err = setupuri.QRModules(uri)
	if err != nil {
		return "", 0, err.Error()
	}
	return out, modules, ""
}

// Provision creates the database, the dedicated account, and the security
// document restricting the database to that account.
//
// It is not transactional. On failure the caller should run Teardown to avoid
// leaving a half-created vault behind.
func Provision(ctx context.Context, c *couch.Client, dbName string, creds Credentials) error {
	if err := ValidateDBName(dbName); err != nil {
		return err
	}

	if err := c.CreateDB(ctx, dbName); err != nil && !couch.IsConflict(err) {
		return fmt.Errorf("데이터베이스 %q 생성 실패: %w", dbName, err)
	}
	if err := c.CreateUser(ctx, creds.CouchUser, creds.CouchPassword, nil); err != nil && !couch.IsConflict(err) {
		return fmt.Errorf("계정 %q 생성 실패: %w", creds.CouchUser, err)
	}

	// Members-only, admins empty: the account may read and write documents but
	// cannot change the database's own security settings.
	if err := c.SetSecurity(ctx, dbName, couch.Security{
		Members: couch.SecurityNames{Names: []string{creds.CouchUser}},
	}); err != nil {
		return fmt.Errorf("%q 권한 설정 실패: %w", dbName, err)
	}
	return nil
}

// Adopt brings a database that already exists under CouchHub's management.
//
// Unlike Provision it never creates the database, and it *merges* into the
// existing _security document instead of replacing it: an adopted database may
// already grant access to accounts CouchHub knows nothing about, and silently
// revoking them would break whatever is using them.
func Adopt(ctx context.Context, c *couch.Client, dbName string, creds Credentials) error {
	if err := ValidateDBName(dbName); err != nil {
		return err
	}

	exists, err := c.DBExists(ctx, dbName)
	if err != nil {
		return fmt.Errorf("데이터베이스 %q 확인 실패: %w", dbName, err)
	}
	if !exists {
		return fmt.Errorf("데이터베이스 %q가 존재하지 않습니다", dbName)
	}

	if err := c.CreateUser(ctx, creds.CouchUser, creds.CouchPassword, nil); err != nil && !couch.IsConflict(err) {
		return fmt.Errorf("계정 %q 생성 실패: %w", creds.CouchUser, err)
	}

	security, err := c.Security(ctx, dbName)
	if err != nil {
		return fmt.Errorf("%q 권한 조회 실패: %w", dbName, err)
	}
	if !contains(security.Members.Names, creds.CouchUser) {
		security.Members.Names = append(security.Members.Names, creds.CouchUser)
	}
	if err := c.SetSecurity(ctx, dbName, security); err != nil {
		return fmt.Errorf("%q 권한 설정 실패: %w", dbName, err)
	}
	return nil
}

// Detach removes only the account CouchHub added, leaving the database and its
// documents alone. It is the counterpart to Adopt: forgetting a vault CouchHub
// did not create must not destroy data it did not create either.
func Detach(ctx context.Context, c *couch.Client, dbName, couchUser string) error {
	if couchUser == "" {
		return nil
	}

	security, err := c.Security(ctx, dbName)
	if err == nil {
		filtered := security.Members.Names[:0]
		for _, n := range security.Members.Names {
			if n != couchUser {
				filtered = append(filtered, n)
			}
		}
		security.Members.Names = filtered
		if err := c.SetSecurity(ctx, dbName, security); err != nil {
			return fmt.Errorf("%q 권한 정리 실패: %w", dbName, err)
		}
	} else if !couch.IsNotFound(err) {
		return err
	}

	return c.DeleteUser(ctx, couchUser)
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// Teardown removes a vault's database and account. Missing pieces are ignored
// so it can clean up after a partial Provision.
func Teardown(ctx context.Context, c *couch.Client, dbName, couchUser string) error {
	var problems []string

	if err := c.DeleteDB(ctx, dbName); err != nil && !couch.IsNotFound(err) {
		problems = append(problems, fmt.Sprintf("데이터베이스 %q: %v", dbName, err))
	}
	if couchUser != "" {
		if err := c.DeleteUser(ctx, couchUser); err != nil {
			problems = append(problems, fmt.Sprintf("계정 %q: %v", couchUser, err))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("정리 중 오류: %s", strings.Join(problems, "; "))
	}
	return nil
}
