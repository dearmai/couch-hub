package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dearmai/couch-hub/internal/couch"
	"github.com/dearmai/couch-hub/internal/store"
	"github.com/dearmai/couch-hub/internal/vault"
)

// migrationReplicationID names the copy document in the target's _replicator so
// it is recognisable there and unambiguous between vaults.
func migrationReplicationID(vaultID string) string { return "couchhub:migrate:" + vaultID }

// migrationView is one move, with enough context for the UI to narrate it.
type migrationView struct {
	VaultID string `json:"vaultId"`

	SourceProfileID string `json:"sourceProfileId"`
	SourceName      string `json:"sourceName"`
	TargetProfileID string `json:"targetProfileId"`
	TargetName      string `json:"targetName"`

	DBName       string    `json:"dbName"`
	DeleteSource bool      `json:"deleteSource"`
	StartedAt    time.Time `json:"startedAt"`

	// Status is CouchDB's own view of the copy. It is the authority on
	// progress; CouchHub only records that a move was started.
	Status couch.ReplicationStatus `json:"status"`
	// SourceDocCount is what the copy is working towards, so a document count
	// can be shown as progress rather than as a bare number. Zero when the
	// source could not be read.
	SourceDocCount int64 `json:"sourceDocCount"`

	// Ready is true once the copy has finished and only the switch-over is left.
	Ready bool `json:"ready"`
	// SetupURIChanged warns that finishing changes the address clients use, so
	// every device needs the Setup URI again.
	SetupURIChanged bool `json:"setupUriChanged"`
}

func (s *Server) migrationView(ctx context.Context, m store.Migration) (migrationView, error) {
	source, err := s.store.Profile(m.SourceProfileID)
	if err != nil {
		return migrationView{}, err
	}
	target, err := s.store.Profile(m.TargetProfileID)
	if err != nil {
		return migrationView{}, err
	}

	out := migrationView{
		VaultID:         m.VaultID,
		SourceProfileID: source.ID, SourceName: source.Name,
		TargetProfileID: target.ID, TargetName: target.Name,
		DBName:          m.DBName,
		DeleteSource:    m.DeleteSource,
		StartedAt:       m.StartedAt,
		SetupURIChanged: source.PublicBaseURL != target.PublicBaseURL,
	}

	targetClient, err := s.clientFor(target)
	if err != nil {
		return migrationView{}, err
	}
	status, err := targetClient.ReplicationStatus(ctx, m.ReplicationID)
	if err != nil {
		return migrationView{}, err
	}
	out.Status = status
	out.Ready = status.Done()

	// A total to measure against is a nicety, not part of the answer: a source
	// that has gone away mid-move must not turn a status call into an error.
	if sourceClient, err := s.clientFor(source); err == nil {
		if info, err := sourceClient.DBInfo(ctx, m.DBName); err == nil {
			out.SourceDocCount = info.DocCount
		}
	}
	return out, nil
}

type startMigrationRequest struct {
	TargetProfileID string `json:"targetProfileId"`
	// DeleteSource drops the original database when the move is finished.
	DeleteSource bool `json:"deleteSource"`
}

// handleStartMigration copies a vault's database to another CouchDB.
//
// It only starts the copy. CouchDB does the work, and the vault keeps pointing
// at the old server until /migrate/finish - so a move that stalls or fails
// leaves a working vault behind rather than a half-moved one.
func (s *Server) handleStartMigration(w http.ResponseWriter, r *http.Request) {
	var req startMigrationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}

	v, err := s.store.Vault(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}

	if _, err := s.store.Migration(v.ID); err == nil {
		writeError(w, http.StatusConflict, "migration_in_progress",
			errors.New("이 Vault는 이미 이동 중입니다. 진행 상태를 확인하거나 취소하세요"))
		return
	} else if err != store.ErrNotFound {
		fail(w, err)
		return
	}

	// The copy authenticates as the vault's own account on both sides, so the
	// credentials have to still exist. Admin credentials would work too, but
	// putting them in a replication document hands every database on the server
	// to anyone who can read _replicator.
	if !v.SecretsPersisted {
		writeError(w, http.StatusPreconditionFailed, "secrets_not_persisted",
			errors.New("이 Vault는 자격증명이 저장되지 않아 다른 CouchDB로 옮길 수 없습니다"))
		return
	}

	source, err := s.store.Profile(v.ProfileID)
	if err != nil {
		fail(w, err)
		return
	}
	target, err := s.resolveProfile(req.TargetProfileID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "no_profile", err)
		return
	}
	if target.ID == source.ID {
		writeError(w, http.StatusBadRequest, "bad_request",
			errors.New("Vault가 이미 이 CouchDB에 있습니다"))
		return
	}

	vaults, err := s.store.Vaults()
	if err != nil {
		fail(w, err)
		return
	}
	for _, other := range vaults {
		if other.ID != v.ID && other.ProfileID == target.ID && other.DBName == v.DBName {
			writeError(w, http.StatusConflict, "duplicate",
				fmt.Errorf("대상 CouchDB에 데이터베이스 %q를 쓰는 Vault가 이미 있습니다", v.DBName))
			return
		}
	}

	password, err := s.sealer.OpenString(v.CouchPasswordSealed)
	if err != nil {
		fail(w, err)
		return
	}

	targetClient, err := s.clientFor(target)
	if err != nil {
		fail(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Refuse to copy into an existing database. Replicating into one would
	// merge two histories, and there is no undo for that.
	exists, err := targetClient.DBExists(ctx, v.DBName)
	if err != nil {
		writeConnectError(w, err)
		return
	}
	if exists {
		writeError(w, http.StatusConflict, "target_db_exists",
			fmt.Errorf("대상 CouchDB에 데이터베이스 %q가 이미 있습니다. 먼저 정리하세요", v.DBName))
		return
	}

	creds := vault.Credentials{CouchUser: v.CouchUser, CouchPassword: password}
	if err := vault.Provision(ctx, targetClient, v.DBName, creds); err != nil {
		_ = vault.Teardown(context.WithoutCancel(ctx), targetClient, v.DBName, v.CouchUser)
		writeError(w, http.StatusBadGateway, "provision_failed", err)
		return
	}

	// The document is written on the target, so the target CouchDB is what
	// resolves both addresses: the source through its public address, itself
	// through whichever of its own addresses it can actually reach.
	doc := couch.Replication{
		ID:         migrationReplicationID(v.ID),
		Source:     couch.NewEndpoint(source.PublicBaseURL, v.DBName, v.CouchUser, password),
		Target:     couch.NewEndpoint(selfURL(target), v.DBName, v.CouchUser, password),
		Continuous: false,
		Owner:      "couchhub",
	}
	if err := targetClient.PutReplication(ctx, doc); err != nil {
		_ = vault.Teardown(context.WithoutCancel(ctx), targetClient, v.DBName, v.CouchUser)
		writeError(w, http.StatusBadGateway, "replication_failed", err)
		return
	}

	m := store.Migration{
		VaultID:         v.ID,
		SourceProfileID: source.ID,
		TargetProfileID: target.ID,
		DBName:          v.DBName,
		ReplicationID:   doc.ID,
		DeleteSource:    req.DeleteSource,
		StartedAt:       time.Now().UTC(),
	}
	if err := s.store.PutMigration(m); err != nil {
		_ = targetClient.DeleteReplication(context.WithoutCancel(ctx), doc.ID)
		_ = vault.Teardown(context.WithoutCancel(ctx), targetClient, v.DBName, v.CouchUser)
		fail(w, err)
		return
	}

	view, err := s.migrationView(ctx, m)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}

// handleMigrationStatus reports the copy's progress. 404 means no move is in
// flight, which is what the UI polls for.
func (s *Server) handleMigrationStatus(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.Migration(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	view, err := s.migrationView(ctx, m)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

type finishMigrationResponse struct {
	Vault vaultView `json:"vault"`
	// SetupURIChanged is true when the new server is published under a different
	// address, which every client has to be given before it syncs again.
	SetupURIChanged bool `json:"setupUriChanged"`
	// SourceRemoved reports what happened to the original database.
	SourceRemoved bool `json:"sourceRemoved"`
	// SourceError explains a copy that finished but whose cleanup did not. The
	// vault has already moved at that point, so this is a leftover to tidy, not
	// a failed migration.
	SourceError string `json:"sourceError,omitempty"`
}

// handleFinishMigration points the vault at the new server.
//
// It refuses until CouchDB says the copy is complete: switching earlier would
// hand clients a database that is still filling up.
func (s *Server) handleFinishMigration(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.Migration(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}
	v, err := s.store.Vault(m.VaultID)
	if err != nil {
		fail(w, err)
		return
	}
	source, err := s.store.Profile(m.SourceProfileID)
	if err != nil {
		fail(w, err)
		return
	}
	target, err := s.store.Profile(m.TargetProfileID)
	if err != nil {
		fail(w, err)
		return
	}
	targetClient, err := s.clientFor(target)
	if err != nil {
		fail(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	status, err := targetClient.ReplicationStatus(ctx, m.ReplicationID)
	if err != nil {
		writeConnectError(w, err)
		return
	}
	if !status.Done() {
		writeError(w, http.StatusPreconditionFailed, "copy_incomplete",
			fmt.Errorf("복사가 아직 끝나지 않았습니다 (상태: %s)", replicationStateLabel(status)))
		return
	}

	v.ProfileID = target.ID
	v.UpdatedAt = time.Now().UTC()
	if err := s.store.PutVault(v); err != nil {
		fail(w, err)
		return
	}

	// From here the vault has moved. Anything that fails is cleanup, and it is
	// reported rather than rolled back - putting the vault back on the old
	// server because a leftover document could not be deleted would be worse.
	out := finishMigrationResponse{
		Vault:           toVaultView(v),
		SetupURIChanged: source.PublicBaseURL != target.PublicBaseURL,
	}

	var problems []string
	if err := targetClient.DeleteReplication(ctx, m.ReplicationID); err != nil {
		problems = append(problems, fmt.Sprintf("복제 문서 정리: %v", err))
	}

	if m.DeleteSource {
		sourceClient, err := s.clientFor(source)
		switch {
		case err != nil:
			problems = append(problems, err.Error())
		case v.Adopted:
			// The database predates CouchHub, so "delete the source" removes the
			// account CouchHub added and nothing else.
			if err := vault.Detach(ctx, sourceClient, m.DBName, v.CouchUser); err != nil {
				problems = append(problems, err.Error())
			} else {
				out.SourceRemoved = true
			}
		default:
			if err := vault.Teardown(ctx, sourceClient, m.DBName, v.CouchUser); err != nil {
				problems = append(problems, err.Error())
			} else {
				out.SourceRemoved = true
			}
		}
	}

	if err := s.store.DeleteMigration(m.VaultID); err != nil {
		fail(w, err)
		return
	}

	for i, p := range problems {
		if i == 0 {
			out.SourceError = p
			continue
		}
		out.SourceError += "; " + p
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCancelMigration abandons a copy and removes what it created on the
// target. The vault never moved, so nothing else has to be undone.
func (s *Server) handleCancelMigration(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.Migration(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}
	v, err := s.store.Vault(m.VaultID)
	if err != nil {
		fail(w, err)
		return
	}
	target, err := s.store.Profile(m.TargetProfileID)
	if err != nil {
		fail(w, err)
		return
	}
	targetClient, err := s.clientFor(target)
	if err != nil {
		fail(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	if err := targetClient.DeleteReplication(ctx, m.ReplicationID); err != nil {
		writeError(w, http.StatusBadGateway, "teardown_failed", err)
		return
	}
	// The target database and account were created by this migration and hold
	// nothing but the partial copy, so removing them is safe.
	if err := vault.Teardown(ctx, targetClient, m.DBName, v.CouchUser); err != nil {
		writeError(w, http.StatusBadGateway, "teardown_failed", err)
		return
	}
	if err := s.store.DeleteMigration(m.VaultID); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// selfURL is the address a server can be expected to reach itself at.
//
// CouchDB 3.x refuses a bare database name in a replication document, so even
// the local side of a copy needs a URL the server resolves. The administration
// address is normally the right one, and it keeps the copy off the reverse
// proxy - except when it is a loopback address, which means "this machine" to
// CouchHub and something else entirely inside the server's own network
// namespace: a CouchDB administered at 127.0.0.1:5985 through a container port
// mapping is listening on 5984 as far as it is concerned.
func selfURL(p store.Profile) string {
	if u, err := url.Parse(p.AdminBaseURL); err == nil && !isLoopbackHost(u.Hostname()) {
		return p.AdminBaseURL
	}
	if p.PublicBaseURL != "" {
		return p.PublicBaseURL
	}
	return p.AdminBaseURL
}

func isLoopbackHost(host string) bool {
	switch host {
	case "127.0.0.1", "localhost", "::1", "0.0.0.0", "":
		return true
	}
	return false
}

// replicationStateLabel turns CouchDB's scheduler state into something an
// operator can act on.
func replicationStateLabel(s couch.ReplicationStatus) string {
	switch {
	case !s.Exists:
		return "복제 문서가 없습니다"
	case s.Error != "":
		return s.State + ": " + s.Error
	default:
		return s.State
	}
}
