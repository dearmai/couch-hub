package httpapi

import (
	"context"
	"log/slog"
	"time"

	"github.com/dearmai/couch-hub/internal/store"
	"github.com/dearmai/couch-hub/internal/vault"
)

// SetupPINLifetime is how long an issued Setup PIN stays valid.
//
// Long enough to walk to the other device, short enough that a code left on a
// screen is worth nothing by the time anyone else reads it.
const SetupPINLifetime = 5 * time.Minute

// setupPINSweepInterval is how often expiries are checked. The PIN is six
// digits, so a code lingering half a minute past its deadline is worth
// considerably less than a sweep every second would cost.
const setupPINSweepInterval = 30 * time.Second

// ExpireSetupPINs replaces PINs whose lifetime has run out.
//
// Expiry has to happen here rather than in the browser. A Setup URI is
// self-contained - the client decrypts it with the PIN and never asks this
// server anything - so the code stops working when, and only when, the PIN it
// was encrypted under is replaced. A page that closes before its countdown
// finishes would otherwise leave a working code behind.
//
// Blocking - meant to be run in a goroutine.
func (s *Server) ExpireSetupPINs(ctx context.Context) {
	if !s.sealer.Enabled() {
		// Nothing is stored, so there is no PIN to rotate: those vaults showed
		// their credentials once and cannot reissue at all.
		return
	}

	ticker := time.NewTicker(setupPINSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.expireOnce(time.Now().UTC())
		}
	}
}

func (s *Server) expireOnce(now time.Time) {
	vaults, err := s.store.Vaults()
	if err != nil {
		slog.Error("setup pin: list vaults", "err", err)
		return
	}

	for _, v := range vaults {
		if v.SetupPINExpiresAt.IsZero() || v.SetupPINExpiresAt.After(now) {
			continue
		}
		if err := s.rotateSetupPIN(&v); err != nil {
			slog.Error("setup pin: rotate", "vault", v.Name, "err", err)
			continue
		}
		slog.Info("setup pin: expired", "vault", v.Name)
	}
}

// rotateSetupPIN mints a new PIN and stores it, which invalidates every Setup
// URI issued under the old one. It clears the expiry: the new PIN is not on
// display anywhere, so there is nothing to count down.
func (s *Server) rotateSetupPIN(v *store.Vault) error {
	fresh, err := vault.NewCredentials(v.DBName)
	if err != nil {
		return err
	}
	sealed, err := s.sealer.SealString(fresh.SetupPIN)
	if err != nil {
		return err
	}
	v.SetupPINSealed = sealed
	v.SetupPINExpiresAt = time.Time{}
	v.UpdatedAt = time.Now().UTC()
	return s.store.PutVault(*v)
}
