// Package httpapi serves CouchHub's REST API and the embedded web UI.
package httpapi

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/dearmai/couch-hub/internal/config"
	"github.com/dearmai/couch-hub/internal/metrics"
	"github.com/dearmai/couch-hub/internal/secret"
	"github.com/dearmai/couch-hub/internal/store"
)

// Server holds the dependencies shared by every handler.
type Server struct {
	cfg    config.Config
	store  *store.Store
	sealer secret.Sealer
	poller *metrics.Poller
}

func NewServer(cfg config.Config, st *store.Store, sealer secret.Sealer, poller *metrics.Poller) *Server {
	return &Server{cfg: cfg, store: st, sealer: sealer, poller: poller}
}

// Handler builds the router.
func (s *Server) Handler() (http.Handler, error) {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Route("/api", func(r chi.Router) {
		// Without these, an unmatched API path falls through to chi's plain-text
		// "404 page not found", which the UI can only report as a bare
		// "404 Not Found". That is exactly what a stale server looks like -
		// hot-reloaded UI calling an endpoint the running binary predates - so
		// say so instead of leaving the operator to guess.
		r.NotFound(func(w http.ResponseWriter, req *http.Request) {
			writeError(w, http.StatusNotFound, "unknown_endpoint",
				fmt.Errorf("%s %s 엔드포인트가 없습니다. 서버가 UI보다 오래된 빌드일 수 있습니다 (dev: process-compose restart api)",
					req.Method, req.URL.Path))
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed",
				fmt.Errorf("%s는 %s에서 지원되지 않습니다", req.Method, req.URL.Path))
		})

		r.Get("/health", s.handleHealth)
		r.Get("/status", s.handleStatus)

		r.Get("/profiles", s.handleListProfiles)

		// Peer-facing: authenticated by a shared zone token, not by the session
		// the rest of the API assumes.
		r.Get("/zone/export", s.handleZoneExport)

		r.Route("/zones", func(r chi.Router) {
			r.Get("/", s.handleListZones)
			r.Post("/", s.handleCreateZone)
			r.Delete("/{id}", s.handleDeleteZone)
			r.Post("/{id}/sync", s.handleSyncZone)
		})

		r.Get("/dashboard", s.handleDashboard)
		r.Post("/metrics/refresh", s.handleRefreshMetrics)

		// Existing databases on the server, for adopting one that predates
		// CouchHub.
		r.Get("/couch/databases", s.handleListDatabases)

		r.Route("/vaults", func(r chi.Router) {
			r.Get("/", s.handleListVaults)
			r.Post("/", s.handleCreateVault)
			r.Post("/adopt", s.handleAdoptVault)
			r.Get("/{id}", s.handleGetVault)
			r.Delete("/{id}", s.handleDeleteVault)
			r.Post("/{id}/setup-uri", s.handleReissueSetupURI)
			r.Get("/{id}/stats", s.handleVaultStats)
			r.Get("/{id}/documents", s.handleListDocuments)
			// The document id is a query parameter, not a path segment: livesync
			// keys notes by their vault path, so ids contain slashes.
			r.Get("/{id}/document", s.handleGetDocument)
		})

		r.Route("/setup", func(r chi.Router) {
			r.Get("/desired", s.handleSetupDesired)
			r.Get("/guide", s.handleSetupGuide)
			r.Post("/diagnose", s.handleSetupDiagnose)
			r.Post("/apply", s.handleSetupApply)
		})
	})

	ui, err := uiHandler(s.cfg.DevProxy)
	if err != nil {
		return nil, err
	}
	r.Handle("/*", ui)

	return r, nil
}

// buildRevision identifies the running binary so a mismatch between a
// hot-reloaded UI and a long-running API process is visible rather than
// showing up later as a puzzling 404.
var buildRevision = func() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	var rev, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				modified = "-dirty"
			}
		}
	}
	if rev == "" {
		return "devel"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	return rev + modified
}()

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"revision":  buildRevision,
		"startedAt": startedAt,
	})
}

var startedAt = time.Now().UTC()

// statusResponse tells the UI which screen to show on load and whether
// credential persistence is available.
type statusResponse struct {
	// NeedsSetup is true until a CouchDB server has been provisioned, which is
	// what routes a first-run visitor to the install wizard.
	NeedsSetup bool `json:"needsSetup"`

	ProfileCount int `json:"profileCount"`
	VaultCount   int `json:"vaultCount"`

	// SecretEnabled reports whether COUCHHUB_SECRET is set. When false, vault
	// credentials are shown once and never stored, so Setup URIs cannot be
	// reissued - the UI warns about this before a vault is created, not after.
	SecretEnabled bool `json:"secretEnabled"`

	// DocumentsEnabled mirrors COUCHHUB_DOCUMENTS so the UI hides the tab
	// instead of offering one that answers 403.
	DocumentsEnabled bool `json:"documentsEnabled"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.store.Profiles()
	if err != nil {
		fail(w, err)
		return
	}
	vaults, err := s.store.Vaults()
	if err != nil {
		fail(w, err)
		return
	}

	provisioned := false
	for _, p := range profiles {
		if p.Provisioned {
			provisioned = true
			break
		}
	}

	writeJSON(w, http.StatusOK, statusResponse{
		NeedsSetup:       !provisioned,
		ProfileCount:     len(profiles),
		VaultCount:       len(vaults),
		SecretEnabled:    s.sealer.Enabled(),
		DocumentsEnabled: s.cfg.DocumentsEnabled,
	})
}
