package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dearmai/couch-hub/internal/couch"
	"github.com/dearmai/couch-hub/internal/idgen"
	"github.com/dearmai/couch-hub/internal/provision"
	"github.com/dearmai/couch-hub/internal/store"
)

// connectRequest is what the wizard's connection form submits.
//
// It carries every field of that form, including ones only /setup/apply acts
// on, because the UI posts the whole form to both endpoints and decodeJSON
// rejects unknown fields.
type connectRequest struct {
	// Name labels the stored profile. Ignored by /setup/diagnose.
	Name string `json:"name"`
	// AdminBaseURL is how CouchHub reaches CouchDB, typically a private address.
	AdminBaseURL string `json:"adminBaseUrl"`
	// PublicBaseURL is what goes into Setup URIs, i.e. the address Obsidian on a
	// phone can reach. Optional at diagnose time, required to save a profile.
	PublicBaseURL string `json:"publicBaseUrl"`
	AdminUser     string `json:"adminUser"`
	AdminPassword string `json:"adminPassword"`
}

func (r connectRequest) client() (*couch.Client, error) {
	if strings.TrimSpace(r.AdminBaseURL) == "" {
		return nil, errors.New("CouchHub 연동용 CouchDB 주소를 입력하세요")
	}
	if strings.TrimSpace(r.AdminUser) == "" {
		return nil, errors.New("관리자 계정을 입력하세요")
	}
	return couch.New(r.AdminBaseURL, r.AdminUser, r.AdminPassword)
}

// handleSetupDesired serves the configuration table for the guide step, so the
// UI does not carry its own copy that could drift from what Apply writes.
func (s *Server) handleSetupDesired(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"settings":        provision.Desired,
		"systemDatabases": provision.SystemDatabases,
	})
}

// handleSetupDiagnose reads a server and reports what would change, without
// modifying anything.
func (s *Server) handleSetupDiagnose(w http.ResponseWriter, r *http.Request) {
	var req connectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	client, err := req.client()
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	diag, err := provision.Diagnose(ctx, client)
	if err != nil {
		writeConnectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, diag)
}

// applyRequest saves a profile and provisions the server behind it.
type applyRequest struct {
	connectRequest
	// ProfileID updates an existing profile instead of creating one.
	ProfileID string `json:"profileId"`
}

type applyResponse struct {
	ProfileID string                 `json:"profileId"`
	Steps     []provision.StepResult `json:"steps"`
	Diagnosis provision.Diagnosis    `json:"diagnosis"`
}

func (s *Server) handleSetupApply(w http.ResponseWriter, r *http.Request) {
	var req applyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
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

	// Provisioning writes a dozen config values and creates databases; give it
	// more room than a plain diagnose.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	steps, applyErr := provision.Apply(ctx, client, req.AdminUser, req.AdminPassword)
	if applyErr != nil {
		// Return the partial step list so the UI can show how far it got.
		writeJSON(w, http.StatusBadGateway, applyResponse{Steps: steps})
		return
	}

	// Re-read rather than assuming: this is what marks the profile provisioned,
	// so it should reflect the server, not our intent.
	diag, err := provision.Diagnose(ctx, client)
	if err != nil {
		fail(w, err)
		return
	}

	sealed, err := s.sealer.SealString(req.AdminPassword)
	if err != nil {
		fail(w, err)
		return
	}

	now := time.Now().UTC()
	profile := store.Profile{
		ID:                  req.ProfileID,
		Name:                strings.TrimSpace(req.Name),
		AdminBaseURL:        strings.TrimRight(strings.TrimSpace(req.AdminBaseURL), "/"),
		PublicBaseURL:       strings.TrimRight(strings.TrimSpace(req.PublicBaseURL), "/"),
		AdminUser:           req.AdminUser,
		AdminPasswordSealed: sealed,
		Provisioned:         diag.Ready,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if profile.ID == "" {
		profile.ID = idgen.New("profile")
	} else if existing, err := s.store.Profile(profile.ID); err == nil {
		profile.CreatedAt = existing.CreatedAt
	}
	if profile.Name == "" {
		profile.Name = "CouchDB"
	}

	if err := s.store.PutProfile(profile); err != nil {
		fail(w, err)
		return
	}

	writeJSON(w, http.StatusOK, applyResponse{ProfileID: profile.ID, Steps: steps, Diagnosis: diag})
}

// profileView is a Profile without its sealed credential.
type profileView struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	AdminBaseURL  string    `json:"adminBaseUrl"`
	PublicBaseURL string    `json:"publicBaseUrl"`
	AdminUser     string    `json:"adminUser"`
	Provisioned   bool      `json:"provisioned"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func toProfileView(p store.Profile) profileView {
	return profileView{
		ID: p.ID, Name: p.Name,
		AdminBaseURL: p.AdminBaseURL, PublicBaseURL: p.PublicBaseURL,
		AdminUser: p.AdminUser, Provisioned: p.Provisioned,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.store.Profiles()
	if err != nil {
		fail(w, err)
		return
	}
	views := make([]profileView, 0, len(profiles))
	for _, p := range profiles {
		views = append(views, toProfileView(p))
	}
	writeJSON(w, http.StatusOK, views)
}

// clientFor builds an admin client for a stored profile.
func (s *Server) clientFor(p store.Profile) (*couch.Client, error) {
	password, err := s.sealer.OpenString(p.AdminPasswordSealed)
	if err != nil {
		return nil, fmt.Errorf("프로필 %q의 관리자 비밀번호를 열 수 없습니다: %w", p.Name, err)
	}
	return couch.New(p.AdminBaseURL, p.AdminUser, password)
}

// writeConnectError turns a CouchDB failure into something actionable, since
// this is the step operators get stuck on most.
func writeConnectError(w http.ResponseWriter, err error) {
	switch {
	case couch.IsUnauthorized(err):
		writeError(w, http.StatusUnauthorized, "unauthorized",
			errors.New("관리자 계정 또는 비밀번호가 올바르지 않습니다"))
	case couch.IsNotFound(err):
		writeError(w, http.StatusBadGateway, "not_couchdb",
			errors.New("해당 주소에서 CouchDB를 찾을 수 없습니다. 주소와 경로를 확인하세요"))
	default:
		writeError(w, http.StatusBadGateway, "unreachable", err)
	}
}
