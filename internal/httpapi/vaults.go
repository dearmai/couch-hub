package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dearmai/couch-hub/internal/idgen"
	"github.com/dearmai/couch-hub/internal/store"
	"github.com/dearmai/couch-hub/internal/vault"
)

// vaultView is a Vault without its sealed secrets.
type vaultView struct {
	ID        string `json:"id"`
	ProfileID string `json:"profileId"`
	Name      string `json:"name"`
	DBName    string `json:"dbName"`
	CouchUser string `json:"couchUser"`
	// SecretsPersisted is false for vaults created without COUCHHUB_SECRET.
	// Their Setup URI cannot be reissued, which the UI must say out loud rather
	// than offering a button that always fails.
	SecretsPersisted bool `json:"secretsPersisted"`
	// Adopted marks a database CouchHub did not create; the UI defaults its
	// removal to detaching rather than dropping.
	Adopted bool `json:"adopted"`
	// E2EEDisabled marks a vault stored unencrypted.
	E2EEDisabled bool `json:"e2eeDisabled"`
	// SetupPINExpiresAt is when the PIN currently on display stops working. The
	// UI counts down to it rather than to a deadline of its own, so what the
	// page shows and what the server will enforce cannot drift apart.
	SetupPINExpiresAt time.Time `json:"setupPinExpiresAt,omitzero"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

func toVaultView(v store.Vault) vaultView {
	return vaultView{
		ID: v.ID, ProfileID: v.ProfileID, Name: v.Name, DBName: v.DBName,
		CouchUser: v.CouchUser, SecretsPersisted: v.SecretsPersisted,
		Adopted: v.Adopted, E2EEDisabled: v.E2EEDisabled,
		SetupPINExpiresAt: v.SetupPINExpiresAt,
		CreatedAt:         v.CreatedAt, UpdatedAt: v.UpdatedAt,
	}
}

func (s *Server) handleListVaults(w http.ResponseWriter, r *http.Request) {
	vaults, err := s.store.Vaults()
	if err != nil {
		fail(w, err)
		return
	}
	views := make([]vaultView, 0, len(vaults))
	for _, v := range vaults {
		views = append(views, toVaultView(v))
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleGetVault(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.Vault(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toVaultView(v))
}

type createVaultRequest struct {
	// ProfileID may be omitted when exactly one profile exists.
	ProfileID string `json:"profileId"`
	Name      string `json:"name"`
	// DBName overrides the name derived from Name.
	DBName string `json:"dbName"`
}

type vaultWithCredentials struct {
	Vault       vaultView         `json:"vault"`
	Credentials vault.Credentials `json:"credentials"`
	// SecretsPersisted repeats the vault flag at the top level so the creation
	// screen can decide whether to warn that this is the only time the
	// credentials will be shown.
	SecretsPersisted bool `json:"secretsPersisted"`
}

func (s *Server) handleCreateVault(w http.ResponseWriter, r *http.Request) {
	var req createVaultRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", errors.New("Vault 이름을 입력하세요"))
		return
	}

	profile, err := s.resolveProfile(req.ProfileID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "no_profile", err)
		return
	}

	dbName := strings.TrimSpace(req.DBName)
	if dbName == "" {
		dbName, err = vault.NormalizeDBName(req.Name)
	} else {
		err = vault.ValidateDBName(dbName)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_db_name", err)
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
				fmt.Errorf("데이터베이스 %q를 쓰는 Vault가 이미 있습니다", dbName))
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
	if err := creds.BuildSetupURI(profile.PublicBaseURL, dbName, false); err != nil {
		writeError(w, http.StatusBadRequest, "setup_uri", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	if err := vault.Provision(ctx, client, dbName, creds); err != nil {
		// Roll back so a retry with the same name is not blocked by a
		// half-created database.
		_ = vault.Teardown(context.WithoutCancel(ctx), client, dbName, creds.CouchUser)
		writeError(w, http.StatusBadGateway, "provision_failed", err)
		return
	}

	now := time.Now().UTC()
	record := store.Vault{
		ID:        idgen.New("vault"),
		ProfileID: profile.ID,
		Name:      strings.TrimSpace(req.Name),
		DBName:    dbName,
		CouchUser: creds.CouchUser,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if s.sealer.Enabled() {
		var sealErr error
		seal := func(v string) []byte {
			if sealErr != nil {
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
			_ = vault.Teardown(context.WithoutCancel(ctx), client, dbName, creds.CouchUser)
			fail(w, sealErr)
			return
		}
		record.SecretsPersisted = true
	}

	if err := s.store.PutVault(record); err != nil {
		_ = vault.Teardown(context.WithoutCancel(ctx), client, dbName, creds.CouchUser)
		fail(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, vaultWithCredentials{
		Vault:            toVaultView(record),
		Credentials:      creds,
		SecretsPersisted: record.SecretsPersisted,
	})
}

// handleReissueSetupURI hands out a one-time Setup URI.
//
// Every call mints a new PIN, which invalidates whatever was issued before it,
// and stamps an expiry the sweep enforces. A code on a screen is therefore good
// for one device and a few minutes, rather than being a standing key to the
// vault for anyone who photographs it later.
func (s *Server) handleReissueSetupURI(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.Vault(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}
	if !v.SecretsPersisted {
		writeError(w, http.StatusPreconditionFailed, "secrets_not_persisted",
			errors.New("이 Vault는 COUCHHUB_SECRET 없이 생성되어 자격증명이 저장되지 않았습니다. Setup URI를 재발급하려면 Vault를 다시 만들어야 합니다"))
		return
	}

	profile, err := s.store.Profile(v.ProfileID)
	if err != nil {
		fail(w, err)
		return
	}

	creds := vault.Credentials{CouchUser: v.CouchUser}
	if creds.CouchPassword, err = s.sealer.OpenString(v.CouchPasswordSealed); err != nil {
		fail(w, err)
		return
	}
	// An adopted plaintext vault has no passphrase to unseal.
	if !v.E2EEDisabled {
		if creds.E2EEPassphrase, err = s.sealer.OpenString(v.E2EEPassphraseSealed); err != nil {
			fail(w, err)
			return
		}
	}
	fresh, err := vault.NewCredentials(v.DBName)
	if err != nil {
		fail(w, err)
		return
	}
	creds.SetupPIN = fresh.SetupPIN

	sealed, err := s.sealer.SealString(creds.SetupPIN)
	if err != nil {
		fail(w, err)
		return
	}
	v.SetupPINSealed = sealed
	v.SetupPINExpiresAt = time.Now().UTC().Add(SetupPINLifetime)
	v.UpdatedAt = time.Now().UTC()
	if err := s.store.PutVault(v); err != nil {
		fail(w, err)
		return
	}

	if err := creds.BuildSetupURI(profile.PublicBaseURL, v.DBName, v.E2EEDisabled); err != nil {
		fail(w, err)
		return
	}

	writeJSON(w, http.StatusOK, vaultWithCredentials{
		Vault:            toVaultView(v),
		Credentials:      creds,
		SecretsPersisted: true,
	})
}

// handleRepairVaultAccount re-applies a vault's stored credentials to CouchDB.
//
// It exists for vaults registered while an account of the same name was already
// present: CouchDB answered the account creation with a conflict, the password
// CouchHub had just generated was never applied, and what it stored
// authenticates nowhere. Clients report that as a login failure and replication
// as replication_auth_error, both against an account that plainly exists.
//
// Writing the stored password back is the whole repair, and it is safe to run
// on a healthy vault: the same credentials are simply set again.
func (s *Server) handleRepairVaultAccount(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.Vault(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}
	if !v.SecretsPersisted {
		writeError(w, http.StatusPreconditionFailed, "secrets_not_persisted",
			errors.New("이 Vault는 자격증명이 저장되지 않아 복구할 수 없습니다. Vault를 다시 만들어야 합니다"))
		return
	}

	password, err := s.sealer.OpenString(v.CouchPasswordSealed)
	if err != nil {
		fail(w, err)
		return
	}
	profile, err := s.store.Profile(v.ProfileID)
	if err != nil {
		fail(w, err)
		return
	}
	client, err := s.clientFor(profile)
	if err != nil {
		fail(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Adopt is the repair: it never creates or empties the database, and it
	// merges into the existing _security document rather than replacing it.
	if err := vault.Adopt(ctx, client, v.DBName, vault.Credentials{
		CouchUser:     v.CouchUser,
		CouchPassword: password,
	}); err != nil {
		writeError(w, http.StatusBadGateway, "repair_failed", err)
		return
	}

	v.UpdatedAt = time.Now().UTC()
	if err := s.store.PutVault(v); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toVaultView(v))
}

func (s *Server) handleDeleteVault(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.Vault(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}

	// A move in flight owns a database on another server. Deleting the vault
	// underneath it would leave that copy behind with nothing pointing at it.
	if _, err := s.store.Migration(v.ID); err == nil {
		writeError(w, http.StatusConflict, "migration_in_progress",
			errors.New("이 Vault는 다른 CouchDB로 이동 중입니다. 이동을 끝내거나 취소한 뒤 삭제하세요"))
		return
	} else if err != store.ErrNotFound {
		fail(w, err)
		return
	}

	// Deleting a vault destroys every note in it, so the caller has to echo the
	// name back. A bare DELETE is too easy to fire by accident.
	if confirm := r.URL.Query().Get("confirm"); confirm != v.Name {
		writeError(w, http.StatusBadRequest, "confirm_required",
			fmt.Errorf("삭제를 확인하려면 Vault 이름 %q를 정확히 입력하세요", v.Name))
		return
	}

	profile, err := s.store.Profile(v.ProfileID)
	if err != nil {
		fail(w, err)
		return
	}
	client, err := s.clientFor(profile)
	if err != nil {
		fail(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// keepData detaches instead of dropping: for an adopted database the
	// documents predate CouchHub, so removing the vault must not be the same
	// action as destroying data CouchHub never created.
	keepData := r.URL.Query().Get("keepData") == "true"
	if keepData {
		if err := vault.Detach(ctx, client, v.DBName, v.CouchUser); err != nil {
			writeError(w, http.StatusBadGateway, "detach_failed", err)
			return
		}
	} else if err := vault.Teardown(ctx, client, v.DBName, v.CouchUser); err != nil {
		writeError(w, http.StatusBadGateway, "teardown_failed", err)
		return
	}
	if err := s.store.DeleteVault(v.ID); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
