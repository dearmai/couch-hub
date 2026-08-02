package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dearmai/couch-hub/internal/store"
)

// defaultHistoryDays covers a year of trend plus the 53-week heatmap.
const defaultHistoryDays = 371

// vaultStats is one vault's dashboard payload.
type vaultStats struct {
	Vault vaultView `json:"vault"`
	// Latest is absent until the poller has run at least once for this vault.
	Latest    *store.Snapshot     `json:"latest"`
	Snapshots []store.Snapshot    `json:"snapshots"`
	Activity  []store.ActivityDay `json:"activity"`
}

func (s *Server) handleVaultStats(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.Vault(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}

	since := time.Now().UTC().AddDate(0, 0, -historyDays(r))

	snapshots, err := s.store.Snapshots(v.ID, since)
	if err != nil {
		fail(w, err)
		return
	}
	activity, err := s.store.Activity(v.ID, since)
	if err != nil {
		fail(w, err)
		return
	}

	out := vaultStats{
		Vault:     toVaultView(v),
		Snapshots: snapshots,
		Activity:  activity,
	}
	if len(snapshots) > 0 {
		out.Latest = &snapshots[len(snapshots)-1]
	}
	writeJSON(w, http.StatusOK, out)
}

// dashboardResponse is the overview across every vault.
type dashboardResponse struct {
	Totals struct {
		Vaults    int   `json:"vaults"`
		Documents int64 `json:"documents"`
		// SizeFile is on-disk size including not-yet-compacted revisions, which
		// is what actually consumes the host's disk.
		SizeFile int64 `json:"sizeFile"`
		// SizeActive is the live data; the gap between the two is what a
		// compaction would reclaim.
		SizeActive int64 `json:"sizeActive"`
	} `json:"totals"`

	Vaults []vaultSummary `json:"vaults"`
	// Activity is the per-day write count summed over all vaults.
	Activity []store.ActivityDay `json:"activity"`
	// Stale is true when no vault has been polled yet, so the UI can say
	// "collecting" rather than showing convincing zeroes.
	Stale bool `json:"stale"`
}

type vaultSummary struct {
	Vault  vaultView       `json:"vault"`
	Latest *store.Snapshot `json:"latest"`
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	vaults, err := s.store.Vaults()
	if err != nil {
		fail(w, err)
		return
	}

	since := time.Now().UTC().AddDate(0, 0, -historyDays(r))

	out := dashboardResponse{Vaults: make([]vaultSummary, 0, len(vaults)), Stale: true}
	perDay := map[string]uint32{}

	for _, v := range vaults {
		summary := vaultSummary{Vault: toVaultView(v)}

		latest, err := s.store.LatestSnapshot(v.ID)
		switch err {
		case nil:
			summary.Latest = &latest
			out.Stale = false
			out.Totals.Documents += latest.DocCount
			out.Totals.SizeFile += latest.SizeFile
			out.Totals.SizeActive += latest.SizeActive
		case store.ErrNotFound:
			// Not polled yet.
		default:
			fail(w, err)
			return
		}

		days, err := s.store.Activity(v.ID, since)
		if err != nil {
			fail(w, err)
			return
		}
		for _, d := range days {
			// Saturate rather than wrap, matching the store.
			total := uint64(perDay[d.Day]) + uint64(d.Writes)
			if total > 0xFFFFFFFF {
				total = 0xFFFFFFFF
			}
			perDay[d.Day] = uint32(total)
		}

		out.Vaults = append(out.Vaults, summary)
	}

	out.Totals.Vaults = len(vaults)
	out.Activity = sortedActivity(perDay)

	writeJSON(w, http.StatusOK, out)
}

// handleRefreshMetrics polls every vault now instead of waiting for the timer.
func (s *Server) handleRefreshMetrics(w http.ResponseWriter, r *http.Request) {
	if s.poller == nil {
		writeJSON(w, http.StatusOK, map[string]bool{"refreshed": false})
		return
	}
	s.poller.PollAll(r.Context())
	writeJSON(w, http.StatusOK, map[string]bool{"refreshed": true})
}

func historyDays(r *http.Request) int {
	days := defaultHistoryDays
	if raw := r.URL.Query().Get("days"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 1000 {
			days = n
		}
	}
	return days
}

func sortedActivity(perDay map[string]uint32) []store.ActivityDay {
	out := make([]store.ActivityDay, 0, len(perDay))
	for day, writes := range perDay {
		out = append(out, store.ActivityDay{Day: day, Writes: writes})
	}
	// Day keys are YYYY-MM-DD, so lexical order is chronological order.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Day < out[j-1].Day; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
