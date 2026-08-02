package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dearmai/couch-hub/internal/couch"
	"github.com/dearmai/couch-hub/internal/idgen"
	"github.com/dearmai/couch-hub/internal/provision"
	"github.com/dearmai/couch-hub/internal/store"
)

// profileView is a Profile without its sealed credential.
type profileView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	AdminBaseURL  string `json:"adminBaseUrl"`
	PublicBaseURL string `json:"publicBaseUrl"`
	AdminUser     string `json:"adminUser"`
	Provisioned   bool   `json:"provisioned"`
	// Primary marks the server new vaults land on when none is chosen.
	Primary bool `json:"primary"`
	// VaultCount decides whether a server can be removed, so it travels with the
	// list rather than leaving the UI to join two endpoints and guess.
	VaultCount int       `json:"vaultCount"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

func toProfileView(p store.Profile, vaultCount int) profileView {
	return profileView{
		ID: p.ID, Name: p.Name,
		AdminBaseURL: p.AdminBaseURL, PublicBaseURL: p.PublicBaseURL,
		AdminUser: p.AdminUser, Provisioned: p.Provisioned, Primary: p.Primary,
		VaultCount: vaultCount,
		CreatedAt:  p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

// vaultsPerProfile counts registered vaults by server.
func (s *Server) vaultsPerProfile() (map[string]int, error) {
	vaults, err := s.store.Vaults()
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(vaults))
	for _, v := range vaults {
		counts[v.ProfileID]++
	}
	return counts, nil
}

func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.store.Profiles()
	if err != nil {
		fail(w, err)
		return
	}
	counts, err := s.vaultsPerProfile()
	if err != nil {
		fail(w, err)
		return
	}

	// Primary first, then oldest first: the list is a stable thing operators
	// read top to bottom, not something that reshuffles as servers are edited.
	sort.SliceStable(profiles, func(i, j int) bool {
		if profiles[i].Primary != profiles[j].Primary {
			return profiles[i].Primary
		}
		return profiles[i].CreatedAt.Before(profiles[j].CreatedAt)
	})

	views := make([]profileView, 0, len(profiles))
	for _, p := range profiles {
		views = append(views, toProfileView(p, counts[p.ID]))
	}
	writeJSON(w, http.StatusOK, views)
}

// profileResponse is what adding or reconfiguring a server returns: the stored
// record plus what was actually done to the server behind it.
type profileResponse struct {
	Profile profileView            `json:"profile"`
	Steps   []provision.StepResult `json:"steps,omitempty"`
	// Diagnosis reports what still differs from the livesync configuration.
	Diagnosis provision.Diagnosis `json:"diagnosis"`
}

// handleCreateProfile registers another CouchDB server and provisions it.
//
// Adding a server is the same work the install wizard does, so it runs the same
// provisioning: a server that is registered but not configured for livesync
// would accept a vault and then fail the first client that connected to it.
func (s *Server) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	var req connectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	s.saveProfile(w, r, "", req)
}

// handleUpdateProfile reconfigures a registered server.
//
// An empty password keeps the stored one: the form cannot show what is sealed,
// so an untouched password field must not read as "clear it".
func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	var req connectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}

	id := chi.URLParam(r, "id")
	existing, err := s.store.Profile(id)
	if err != nil {
		fail(w, err)
		return
	}
	if strings.TrimSpace(req.AdminPassword) == "" {
		password, err := s.sealer.OpenString(existing.AdminPasswordSealed)
		if err != nil {
			writeError(w, http.StatusPreconditionFailed, "password_required",
				errors.New("저장된 관리자 비밀번호를 열 수 없습니다. 비밀번호를 다시 입력하세요"))
			return
		}
		req.AdminPassword = password
	}
	s.saveProfile(w, r, id, req)
}

// saveProfile provisions a server and stores it, creating or updating.
func (s *Server) saveProfile(w http.ResponseWriter, r *http.Request, id string, req connectRequest) {
	client, err := req.client()
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	if strings.TrimSpace(req.PublicBaseURL) == "" {
		writeError(w, http.StatusBadRequest, "bad_request",
			errors.New("Obsidian 연동용 주소를 입력하세요. Setup URI에 들어가는 주소입니다"))
		return
	}
	if !s.sealer.Enabled() {
		writeError(w, http.StatusPreconditionFailed, "secret_disabled",
			errors.New("COUCHHUB_SECRET이 설정되지 않아 관리자 자격증명을 저장할 수 없습니다"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	steps, applyErr := provision.Apply(ctx, client, req.AdminUser, req.AdminPassword)
	if applyErr != nil {
		// The partial step list is the useful part of the failure: it says how
		// far the server got before refusing.
		writeJSON(w, http.StatusBadGateway, profileResponse{Steps: steps})
		return
	}

	diag, err := provision.Diagnose(ctx, client)
	if err != nil {
		writeConnectError(w, err)
		return
	}

	profile, err := s.upsertProfile(id, req, diag.Ready)
	if err != nil {
		fail(w, err)
		return
	}

	counts, err := s.vaultsPerProfile()
	if err != nil {
		fail(w, err)
		return
	}

	status := http.StatusOK
	if id == "" {
		status = http.StatusCreated
	}
	writeJSON(w, status, profileResponse{
		Profile:   toProfileView(profile, counts[profile.ID]),
		Steps:     steps,
		Diagnosis: diag,
	})
}

// upsertProfile writes the record, sealing the admin password.
//
// The first server stored becomes the primary, and an update never moves the
// flag: promoting a server is its own deliberate action, not a side effect of
// editing an address.
func (s *Server) upsertProfile(id string, req connectRequest, provisioned bool) (store.Profile, error) {
	sealed, err := s.sealer.SealString(req.AdminPassword)
	if err != nil {
		return store.Profile{}, err
	}

	now := time.Now().UTC()
	profile := store.Profile{
		ID:                  id,
		Name:                strings.TrimSpace(req.Name),
		AdminBaseURL:        strings.TrimRight(strings.TrimSpace(req.AdminBaseURL), "/"),
		PublicBaseURL:       strings.TrimRight(strings.TrimSpace(req.PublicBaseURL), "/"),
		AdminUser:           req.AdminUser,
		AdminPasswordSealed: sealed,
		Provisioned:         provisioned,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	existing, err := s.store.Profiles()
	if err != nil {
		return store.Profile{}, err
	}

	switch {
	case profile.ID == "":
		profile.ID = idgen.New("profile")
		profile.Primary = !anyPrimary(existing)
	default:
		for _, p := range existing {
			if p.ID == profile.ID {
				profile.CreatedAt = p.CreatedAt
				profile.Primary = p.Primary
			}
		}
		// A server registered before this field existed, or one whose primary
		// was deleted, still needs someone to hold the flag.
		if !profile.Primary && !anyPrimary(existing) {
			profile.Primary = true
		}
	}

	if profile.Name == "" {
		profile.Name = "CouchDB"
	}
	if err := s.store.PutProfile(profile); err != nil {
		return store.Profile{}, err
	}
	return profile, nil
}

func anyPrimary(profiles []store.Profile) bool {
	for _, p := range profiles {
		if p.Primary {
			return true
		}
	}
	return false
}

// handleSetPrimaryProfile moves the primary flag, which decides where a vault
// is created when no server is named.
func (s *Server) handleSetPrimaryProfile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.store.Profile(id); err != nil {
		fail(w, err)
		return
	}

	profiles, err := s.store.Profiles()
	if err != nil {
		fail(w, err)
		return
	}

	now := time.Now().UTC()
	for _, p := range profiles {
		want := p.ID == id
		if p.Primary == want {
			continue
		}
		p.Primary = want
		p.UpdatedAt = now
		if err := s.store.PutProfile(p); err != nil {
			fail(w, err)
			return
		}
	}

	s.handleListProfiles(w, r)
}

// handleDeleteProfile forgets a server. It never touches the server itself -
// CouchHub did not necessarily install it, and databases on it may be in use.
func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	profile, err := s.store.Profile(id)
	if err != nil {
		fail(w, err)
		return
	}

	counts, err := s.vaultsPerProfile()
	if err != nil {
		fail(w, err)
		return
	}
	if n := counts[id]; n > 0 {
		writeError(w, http.StatusConflict, "profile_in_use",
			fmt.Errorf("%s에 Vault %d개가 등록되어 있습니다. 먼저 옮기거나 삭제하세요", profile.Name, n))
		return
	}

	if err := s.store.DeleteProfile(id); err != nil {
		fail(w, err)
		return
	}

	// Removing the primary leaves nothing to fall back on, so hand the flag to
	// the oldest survivor rather than making every later request fail.
	if profile.Primary {
		remaining, err := s.store.Profiles()
		if err != nil {
			fail(w, err)
			return
		}
		if len(remaining) > 0 {
			sort.SliceStable(remaining, func(i, j int) bool {
				return remaining[i].CreatedAt.Before(remaining[j].CreatedAt)
			})
			next := remaining[0]
			next.Primary = true
			next.UpdatedAt = time.Now().UTC()
			if err := s.store.PutProfile(next); err != nil {
				fail(w, err)
				return
			}
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleDiagnoseProfile re-reads a registered server, so the list can say
// whether it still matches what livesync needs.
func (s *Server) handleDiagnoseProfile(w http.ResponseWriter, r *http.Request) {
	profile, err := s.store.Profile(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}
	client, err := s.clientFor(profile)
	if err != nil {
		fail(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	diag, err := provision.Diagnose(ctx, client)
	if err != nil {
		writeConnectError(w, err)
		return
	}

	// Record what was found: a server that drifted out of configuration should
	// stop claiming to be provisioned.
	if profile.Provisioned != diag.Ready {
		profile.Provisioned = diag.Ready
		profile.UpdatedAt = time.Now().UTC()
		if err := s.store.PutProfile(profile); err != nil {
			fail(w, err)
			return
		}
	}

	counts, err := s.vaultsPerProfile()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profileResponse{
		Profile:   toProfileView(profile, counts[profile.ID]),
		Diagnosis: diag,
	})
}

// resolveProfile returns the requested server, or the one new vaults default to.
func (s *Server) resolveProfile(id string) (store.Profile, error) {
	if id != "" {
		p, err := s.store.Profile(id)
		if err != nil {
			return store.Profile{}, fmt.Errorf("CouchDB %q를 찾을 수 없습니다", id)
		}
		return p, nil
	}

	profiles, err := s.store.Profiles()
	if err != nil {
		return store.Profile{}, err
	}
	switch len(profiles) {
	case 0:
		return store.Profile{}, errors.New("CouchDB가 없습니다. 설치 마법사를 먼저 완료하세요")
	case 1:
		return profiles[0], nil
	}
	for _, p := range profiles {
		if p.Primary {
			return p, nil
		}
	}
	return store.Profile{}, errors.New("주 CouchDB가 지정되지 않았습니다. CouchDB 관리에서 주 서버를 정하거나 사용할 서버를 지정하세요")
}

// defaultClient resolves the primary server and a client for it.
func (s *Server) defaultClient() (store.Profile, *couch.Client, error) {
	profile, err := s.resolveProfile("")
	if err != nil {
		return store.Profile{}, nil, err
	}
	client, err := s.clientFor(profile)
	if err != nil {
		return store.Profile{}, nil, err
	}
	return profile, client, nil
}

// clientFor builds an admin client for a stored server.
func (s *Server) clientFor(p store.Profile) (*couch.Client, error) {
	password, err := s.sealer.OpenString(p.AdminPasswordSealed)
	if err != nil {
		return nil, fmt.Errorf("%q의 관리자 비밀번호를 열 수 없습니다: %w", p.Name, err)
	}
	return couch.New(p.AdminBaseURL, p.AdminUser, password)
}
