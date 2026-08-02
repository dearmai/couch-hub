package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dearmai/couch-hub/internal/couch"
	"github.com/dearmai/couch-hub/internal/idgen"
	"github.com/dearmai/couch-hub/internal/setupuri"
	"github.com/dearmai/couch-hub/internal/store"
	"github.com/dearmai/couch-hub/internal/zone"
)

// zoneView is a Zone without its sealed token.
type zoneView struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	PeerURL       string              `json:"peerUrl"`
	Direction     store.ZoneDirection `json:"direction"`
	VaultIDs      []string            `json:"vaultIds,omitempty"`
	LastSyncAt    time.Time           `json:"lastSyncAt,omitzero"`
	LastSyncError string              `json:"lastSyncError,omitempty"`
	CreatedAt     time.Time           `json:"createdAt"`
	UpdatedAt     time.Time           `json:"updatedAt"`
}

func toZoneView(z store.Zone) zoneView {
	return zoneView{
		ID: z.ID, Name: z.Name, PeerURL: z.PeerURL, Direction: z.Direction,
		VaultIDs: z.VaultIDs, LastSyncAt: z.LastSyncAt, LastSyncError: z.LastSyncError,
		CreatedAt: z.CreatedAt, UpdatedAt: z.UpdatedAt,
	}
}

func (s *Server) handleListZones(w http.ResponseWriter, r *http.Request) {
	zones, err := s.store.Zones()
	if err != nil {
		fail(w, err)
		return
	}
	views := make([]zoneView, 0, len(zones))
	for _, z := range zones {
		views = append(views, toZoneView(z))
	}
	writeJSON(w, http.StatusOK, views)
}

type createZoneRequest struct {
	Name      string              `json:"name"`
	PeerURL   string              `json:"peerUrl"`
	Direction store.ZoneDirection `json:"direction"`
	// Token is shared with the peer. Leave empty to have one generated, then
	// paste it into the peer's matching zone.
	Token string `json:"token"`
}

type createZoneResponse struct {
	Zone zoneView `json:"zone"`
	// Token is echoed once so it can be copied to the peer. It is sealed after
	// this and never returned again.
	Token string `json:"token"`
}

func (s *Server) handleCreateZone(w http.ResponseWriter, r *http.Request) {
	var req createZoneRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", errors.New("존 이름을 입력하세요"))
		return
	}
	if !strings.HasPrefix(req.PeerURL, "http://") && !strings.HasPrefix(req.PeerURL, "https://") {
		writeError(w, http.StatusBadRequest, "bad_request",
			errors.New("상대 CouchHub 주소는 http:// 또는 https:// 로 시작해야 합니다"))
		return
	}
	switch req.Direction {
	case store.ZonePull, store.ZonePush, store.ZoneBoth:
	default:
		req.Direction = store.ZoneBoth
	}
	if !s.sealer.Enabled() {
		writeError(w, http.StatusPreconditionFailed, "secret_disabled",
			errors.New("COUCHHUB_SECRET이 설정되지 않아 존 토큰을 저장할 수 없습니다"))
		return
	}

	token := strings.TrimSpace(req.Token)
	if token == "" {
		generated, err := setupuri.GenerateSecret()
		if err != nil {
			fail(w, err)
			return
		}
		token = generated
	}
	sealed, err := s.sealer.SealString(token)
	if err != nil {
		fail(w, err)
		return
	}

	now := time.Now().UTC()
	z := store.Zone{
		ID:          idgen.New("zone"),
		Name:        strings.TrimSpace(req.Name),
		PeerURL:     strings.TrimRight(strings.TrimSpace(req.PeerURL), "/"),
		Direction:   req.Direction,
		TokenSealed: sealed,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.store.PutZone(z); err != nil {
		fail(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, createZoneResponse{Zone: toZoneView(z), Token: token})
}

func (s *Server) handleDeleteZone(w http.ResponseWriter, r *http.Request) {
	z, err := s.store.Zone(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}

	// Tear down the replications before forgetting the zone, or they keep
	// running with nothing left to manage them.
	if profile, client, err := s.defaultClient(); err == nil {
		_ = profile
		vaults, err := s.store.Vaults()
		if err == nil {
			names := make([]string, 0, len(vaults))
			for _, v := range vaults {
				names = append(names, v.DBName)
			}
			ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
			defer cancel()
			if err := zone.Remove(ctx, client, z.ID, names); err != nil {
				writeError(w, http.StatusBadGateway, "teardown_failed", err)
				return
			}
		}
	}

	if err := s.store.DeleteZone(z.ID); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type syncZoneResponse struct {
	Zone zoneView `json:"zone"`
	// Replications is how many documents were written.
	Replications int      `json:"replications"`
	Skipped      []string `json:"skipped,omitempty"`
	// States is the live replication status for this zone, straight from
	// CouchDB's scheduler.
	States []couch.SchedulerDoc `json:"states"`
}

func (s *Server) handleSyncZone(w http.ResponseWriter, r *http.Request) {
	z, err := s.store.Zone(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	result, syncErr := s.syncZone(ctx, z)

	// Record the outcome either way: a zone that has been failing for a week
	// should say so on the list rather than looking idle.
	z.LastSyncAt = time.Now().UTC()
	z.LastSyncError = ""
	if syncErr != nil {
		z.LastSyncError = syncErr.Error()
	}
	z.UpdatedAt = z.LastSyncAt
	if err := s.store.PutZone(z); err != nil {
		fail(w, err)
		return
	}

	if syncErr != nil {
		writeError(w, http.StatusBadGateway, "sync_failed", syncErr)
		return
	}

	result.Zone = toZoneView(z)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) syncZone(ctx context.Context, z store.Zone) (syncZoneResponse, error) {
	var out syncZoneResponse

	token, err := s.sealer.OpenString(z.TokenSealed)
	if err != nil {
		return out, err
	}

	profile, client, err := s.defaultClient()
	if err != nil {
		return out, err
	}

	remote, err := zone.FetchExport(ctx, z.PeerURL, token)
	if err != nil {
		return out, err
	}

	local, err := s.localVaults()
	if err != nil {
		return out, err
	}

	plan := zone.BuildPlan(z, profile.AdminBaseURL, local, remote)
	if err := zone.Apply(ctx, client, plan); err != nil {
		return out, err
	}

	out.Replications = len(plan.Docs)
	out.Skipped = plan.Skipped

	if docs, err := client.SchedulerDocs(ctx); err == nil {
		for _, d := range docs {
			if zone.BelongsTo(d.DocID, z.ID) {
				out.States = append(out.States, d)
			}
		}
	}
	return out, nil
}

// handleZoneExport is the peer-facing endpoint. It hands out live vault
// credentials, so it is guarded by a zone token and nothing else on this router
// shares that auth path.
func (s *Server) handleZoneExport(w http.ResponseWriter, r *http.Request) {
	presented := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if presented == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", errors.New("존 토큰이 필요합니다"))
		return
	}

	zones, err := s.store.Zones()
	if err != nil {
		fail(w, err)
		return
	}

	matched := false
	for _, z := range zones {
		token, err := s.sealer.OpenString(z.TokenSealed)
		if err != nil {
			continue
		}
		if zone.TokenMatches(presented, token) {
			matched = true
			break
		}
	}
	if !matched {
		writeError(w, http.StatusUnauthorized, "unauthorized", errors.New("존 토큰이 올바르지 않습니다"))
		return
	}

	profile, _, err := s.defaultClient()
	if err != nil {
		fail(w, err)
		return
	}

	vaults, err := s.store.Vaults()
	if err != nil {
		fail(w, err)
		return
	}

	out := zone.Export{PublicBaseURL: profile.PublicBaseURL, GeneratedAt: time.Now().UTC()}
	for _, v := range vaults {
		if !v.SecretsPersisted {
			// Its credentials were never stored, so the peer cannot be told how
			// to reach it.
			continue
		}
		password, err := s.sealer.OpenString(v.CouchPasswordSealed)
		if err != nil {
			continue
		}
		out.Vaults = append(out.Vaults, zone.ExportVault{
			Name:          v.Name,
			DBName:        v.DBName,
			CouchUser:     v.CouchUser,
			CouchPassword: password,
			UpdatedAt:     v.UpdatedAt,
		})
	}

	// Credentials in a response body must never sit in a shared cache.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// localVaults maps database name to the local credentials for it.
func (s *Server) localVaults() (map[string]zone.LocalVault, error) {
	vaults, err := s.store.Vaults()
	if err != nil {
		return nil, err
	}
	out := make(map[string]zone.LocalVault, len(vaults))
	for _, v := range vaults {
		if !v.SecretsPersisted {
			continue
		}
		password, err := s.sealer.OpenString(v.CouchPasswordSealed)
		if err != nil {
			continue
		}
		out[v.DBName] = zone.LocalVault{DBName: v.DBName, CouchUser: v.CouchUser, CouchPassword: password}
	}
	return out, nil
}

// defaultClient resolves the single configured profile and a client for it.
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
