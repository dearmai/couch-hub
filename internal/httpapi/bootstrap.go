package httpapi

import (
	"context"
	"log/slog"
	"time"

	"github.com/dearmai/couch-hub/internal/provision"
)

// bootstrapAttempts and bootstrapDelay bound the wait for CouchDB.
//
// A container start is a race CouchHub loses roughly half the time: compose
// starts both, and CouchDB spends a few seconds on its own initialisation. One
// attempt would fail on a perfectly healthy deployment, so it retries for about
// a minute and then leaves the wizard to it.
const (
	bootstrapAttempts = 12
	bootstrapDelay    = 5 * time.Second
)

// Bootstrap registers the CouchDB named in the environment, when there is one
// and nothing has been configured yet.
//
// It provisions exactly as the wizard does, so the result is a server that is
// ready rather than one that is merely recorded. Failure is not fatal: the
// wizard is still there, and it is prefilled with the same values.
//
// Blocking - meant to be run in a goroutine.
func (s *Server) Bootstrap(ctx context.Context) {
	b := s.cfg.Bootstrap
	if !b.Complete() {
		return
	}

	profiles, err := s.store.Profiles()
	if err != nil {
		slog.Error("bootstrap: read profiles", "err", err)
		return
	}
	if len(profiles) > 0 {
		// Already configured. Re-provisioning here would silently overwrite an
		// address the operator changed in the panel.
		return
	}

	if !s.sealer.Enabled() {
		slog.Warn("bootstrap: COUCHHUB_SECRET is not set, so the CouchDB credentials cannot be stored; " +
			"finish the install in the panel instead")
		return
	}

	req := connectRequest{
		Name:          b.Name,
		AdminBaseURL:  b.AdminBaseURL,
		PublicBaseURL: b.PublicBaseURL,
		AdminUser:     b.AdminUser,
		AdminPassword: b.AdminPassword,
	}
	client, err := req.client()
	if err != nil {
		slog.Error("bootstrap: invalid CouchDB configuration", "err", err)
		return
	}

	for attempt := 1; attempt <= bootstrapAttempts; attempt++ {
		if _, err := client.Ping(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Info("bootstrap: waiting for CouchDB",
				"url", b.AdminBaseURL, "attempt", attempt, "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(bootstrapDelay):
			}
			continue
		}

		if _, err := provision.Apply(ctx, client, b.AdminUser, b.AdminPassword); err != nil {
			slog.Error("bootstrap: provision CouchDB", "err", err)
			return
		}
		diag, err := provision.Diagnose(ctx, client)
		if err != nil {
			slog.Error("bootstrap: verify CouchDB", "err", err)
			return
		}
		profile, err := s.upsertProfile("", req, diag.Ready)
		if err != nil {
			slog.Error("bootstrap: store profile", "err", err)
			return
		}

		slog.Info("bootstrap: registered CouchDB from the environment",
			"profile", profile.Name, "url", profile.AdminBaseURL,
			"public", profile.PublicBaseURL, "ready", diag.Ready)
		return
	}

	slog.Warn("bootstrap: CouchDB did not answer, finish the install in the panel",
		"url", b.AdminBaseURL)
}
