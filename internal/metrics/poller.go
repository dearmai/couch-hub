// Package metrics keeps per-vault statistics fresh in the local store.
//
// CouchDB has no history of its own, so the dashboard's charts come from
// snapshots CouchHub takes on a timer. The write counter behind the activity
// heatmap is derived from update_seq rather than by walking _changes: one
// request per vault per poll regardless of how busy the vault is.
package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/dearmai/couch-hub/internal/couch"
	"github.com/dearmai/couch-hub/internal/secret"
	"github.com/dearmai/couch-hub/internal/store"
)

// Retention bounds. Both are generous relative to the data volume: a snapshot is
// a few dozen bytes and a day counter is four.
const (
	// SnapshotRetention keeps enough history for a year of size trends.
	SnapshotRetention = 400 * 24 * time.Hour
	// ActivityRetention covers the 53-week heatmap with room to spare.
	ActivityRetention = 400 * 24 * time.Hour
)

type Poller struct {
	store    *store.Store
	sealer   secret.Sealer
	interval time.Duration
}

func NewPoller(st *store.Store, sealer secret.Sealer, interval time.Duration) *Poller {
	return &Poller{store: st, sealer: sealer, interval: interval}
}

// Run polls until ctx is cancelled. It polls once immediately so a freshly
// started server has numbers to show rather than an empty dashboard.
func (p *Poller) Run(ctx context.Context) {
	p.PollAll(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.PollAll(ctx)
		}
	}
}

// PollAll refreshes every vault once. Failures are logged and skipped: one
// unreachable server must not stop the others from updating.
func (p *Poller) PollAll(ctx context.Context) {
	vaults, err := p.store.Vaults()
	if err != nil {
		slog.Error("metrics: list vaults", "err", err)
		return
	}
	if len(vaults) == 0 {
		return
	}

	// One client per profile, not per vault: vaults usually share a server.
	clients := map[string]*couch.Client{}

	for _, v := range vaults {
		client, ok := clients[v.ProfileID]
		if !ok {
			profile, err := p.store.Profile(v.ProfileID)
			if err != nil {
				slog.Warn("metrics: unknown profile", "vault", v.Name, "profile", v.ProfileID, "err", err)
				continue
			}
			password, err := p.sealer.OpenString(profile.AdminPasswordSealed)
			if err != nil {
				// Expected when the server runs without COUCHHUB_SECRET; there is
				// nothing to poll with, so stay quiet about it at warn level.
				slog.Debug("metrics: no usable admin credentials", "profile", profile.Name, "err", err)
				continue
			}
			client, err = couch.New(profile.AdminBaseURL, profile.AdminUser, password)
			if err != nil {
				slog.Warn("metrics: build client", "profile", profile.Name, "err", err)
				continue
			}
			clients[v.ProfileID] = client
		}

		if err := p.pollVault(ctx, client, v); err != nil {
			slog.Warn("metrics: poll vault", "vault", v.Name, "err", err)
		}
	}
}

func (p *Poller) pollVault(ctx context.Context, client *couch.Client, v store.Vault) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	info, err := client.DBInfo(ctx, v.DBName)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	snap := store.Snapshot{
		VaultID:      v.ID,
		At:           now,
		DocCount:     info.DocCount,
		DocDelCount:  info.DocDelCount,
		SizeFile:     info.Sizes.File,
		SizeActive:   info.Sizes.Active,
		SizeExternal: info.Sizes.External,
		UpdateSeqNum: info.SeqNum(),
	}

	// The write counter is the growth in update_seq since the last poll.
	previous, err := p.store.LatestSnapshot(v.ID)
	switch {
	case err == nil:
		delta := snap.UpdateSeqNum - previous.UpdateSeqNum
		// A negative delta means the sequence restarted - the database was
		// recreated, or the cluster was reshuffled. Counting that as activity
		// would produce a bogus spike, so drop the interval instead.
		if delta > 0 {
			if err := p.store.AddActivity(v.ID, store.DayKey(now), uint32(min(delta, 1<<32-1))); err != nil {
				return err
			}
		}
	case err == store.ErrNotFound:
		// First poll: there is no baseline, so this vault's existing history is
		// not retroactively counted as activity today.
	default:
		return err
	}

	if err := p.store.AppendSnapshot(snap); err != nil {
		return err
	}

	// Pruning on every poll is cheap and keeps the file from growing unbounded
	// without a separate maintenance path.
	if err := p.store.PruneSnapshots(v.ID, now.Add(-SnapshotRetention)); err != nil {
		return err
	}
	return p.store.PruneActivity(v.ID, now.Add(-ActivityRetention))
}
