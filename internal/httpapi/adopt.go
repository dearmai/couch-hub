package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/dearmai/couch-hub/internal/idgen"
	"github.com/dearmai/couch-hub/internal/provision"
	"github.com/dearmai/couch-hub/internal/store"
	"github.com/dearmai/couch-hub/internal/vault"
)

// databaseCandidate is an existing CouchDB database offered for adoption.
type databaseCandidate struct {
	Name     string `json:"name"`
	DocCount int64  `json:"docCount"`
	SizeFile int64  `json:"sizeFile"`
	// Registered is true when a vault already manages this database.
	Registered bool `json:"registered"`
}

// handleListDatabases lists what is on one server, so adopting does not require
// the operator to remember exact database names.
//
// The server is chosen with ?profileId=; without one it is the primary.
func (s *Server) handleListDatabases(w http.ResponseWriter, r *http.Request) {
	profile, err := s.resolveProfile(r.URL.Query().Get("profileId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "no_profile", err)
		return
	}
	client, err := s.clientFor(profile)
	if err != nil {
		fail(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	names, err := client.AllDBs(ctx)
	if err != nil {
		writeConnectError(w, err)
		return
	}

	vaults, err := s.store.Vaults()
	if err != nil {
		fail(w, err)
		return
	}
	// Registration is per server: the same database name on another CouchDB is
	// a different database, and hiding it would make the second server look
	// emptier than it is.
	registered := make(map[string]bool, len(vaults))
	for _, v := range vaults {
		if v.ProfileID == profile.ID {
			registered[v.DBName] = true
		}
	}

	system := map[string]bool{}
	for _, name := range provision.SystemDatabases {
		system[name] = true
	}

	out := make([]databaseCandidate, 0, len(names))
	for _, name := range names {
		// CouchDB's own databases are never vaults, and neither is anything
		// else whose name it reserves with a leading underscore.
		if system[name] || strings.HasPrefix(name, "_") {
			continue
		}
		candidate := databaseCandidate{Name: name, Registered: registered[name]}
		if info, err := client.DBInfo(ctx, name); err == nil {
			candidate.DocCount = info.DocCount
			candidate.SizeFile = info.Sizes.File
		}
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	writeJSON(w, http.StatusOK, out)
}

type adoptVaultRequest struct {
	ProfileID string `json:"profileId"`
	// DBName is the existing database to take over.
	DBName string `json:"dbName"`
	// Name is the display name; defaults to DBName.
	Name string `json:"name"`
	// E2EEPassphrase must be the passphrase the vault is already using. A wrong
	// one produces a Setup URI that connects but cannot read anything already
	// stored, so it is required rather than generated.
	E2EEPassphrase string `json:"e2eePassphrase"`
	// E2EEDisabled adopts a vault that stores plaintext.
	E2EEDisabled bool `json:"e2eeDisabled"`
}

// handleAdoptVault brings an existing database under management.
//
// The database is never created, never emptied, and its _security document is
// merged rather than replaced.
func (s *Server) handleAdoptVault(w http.ResponseWriter, r *http.Request) {
	var req adoptVaultRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}

	dbName := strings.TrimSpace(req.DBName)
	if err := vault.ValidateDBName(dbName); err != nil {
		writeError(w, http.StatusBadRequest, "bad_db_name", err)
		return
	}

	switch {
	case req.E2EEDisabled && req.E2EEPassphrase != "":
		writeError(w, http.StatusBadRequest, "bad_request",
			errors.New("암호화를 사용하지 않는 Vault에는 패스프레이즈를 입력할 수 없습니다"))
		return
	case !req.E2EEDisabled && strings.TrimSpace(req.E2EEPassphrase) == "":
		writeError(w, http.StatusBadRequest, "passphrase_required",
			errors.New("기존 Vault가 쓰던 E2EE 패스프레이즈를 입력하세요. 틀리면 이미 저장된 노트를 읽을 수 없습니다"))
		return
	}

	profile, err := s.resolveProfile(req.ProfileID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "no_profile", err)
		return
	}

	existing, err := s.store.Vaults()
	if err != nil {
		fail(w, err)
		return
	}
	for _, v := range existing {
		if v.DBName == dbName && v.ProfileID == profile.ID {
			writeError(w, http.StatusConflict, "duplicate",
				fmt.Errorf("데이터베이스 %q는 이미 등록되어 있습니다", dbName))
			return
		}
	}

	client, err := s.clientFor(profile)
	if err != nil {
		fail(w, err)
		return
	}

	creds, err := vault.NewCredentials(dbName)
	if err != nil {
		fail(w, err)
		return
	}
	// The generated passphrase is discarded: an adopted vault already has one,
	// and inventing a new one would make its existing contents unreadable.
	creds.E2EEPassphrase = strings.TrimSpace(req.E2EEPassphrase)

	if err := creds.BuildSetupURI(profile.PublicBaseURL, dbName, req.E2EEDisabled); err != nil {
		writeError(w, http.StatusBadRequest, "setup_uri", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	if err := vault.Adopt(ctx, client, dbName, creds); err != nil {
		// Roll back only what we added. The database itself is untouched.
		_ = vault.Detach(context.WithoutCancel(ctx), client, dbName, creds.CouchUser)
		writeError(w, http.StatusBadGateway, "adopt_failed", err)
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = dbName
	}

	now := time.Now().UTC()
	record := store.Vault{
		ID:           idgen.New("vault"),
		ProfileID:    profile.ID,
		Name:         name,
		DBName:       dbName,
		CouchUser:    creds.CouchUser,
		Adopted:      true,
		E2EEDisabled: req.E2EEDisabled,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if s.sealer.Enabled() {
		var sealErr error
		seal := func(v string) []byte {
			if sealErr != nil || v == "" {
				return nil
			}
			out, err := s.sealer.SealString(v)
			sealErr = err
			return out
		}
		record.CouchPasswordSealed = seal(creds.CouchPassword)
		record.E2EEPassphraseSealed = seal(creds.E2EEPassphrase)
		record.SetupPINSealed = seal(creds.SetupPIN)
		// The PIN is on screen from this moment, so its clock starts here.
		record.SetupPINExpiresAt = now.Add(SetupPINLifetime)
		if sealErr != nil {
			_ = vault.Detach(context.WithoutCancel(ctx), client, dbName, creds.CouchUser)
			fail(w, sealErr)
			return
		}
		record.SecretsPersisted = true
	}

	if err := s.store.PutVault(record); err != nil {
		_ = vault.Detach(context.WithoutCancel(ctx), client, dbName, creds.CouchUser)
		fail(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, vaultWithCredentials{
		Vault:            toVaultView(record),
		Credentials:      creds,
		SecretsPersisted: record.SecretsPersisted,
	})
}
