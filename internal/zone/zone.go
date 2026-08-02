// Package zone replicates vaults between two CouchHub instances.
//
// The data itself moves through CouchDB's own replicator: CouchHub writes
// documents into _replicator and CouchDB does the work, so a zone keeps running
// while CouchHub is restarted or upgraded.
//
// What CouchHub adds on top is the registry exchange. A peer cannot replicate a
// vault it does not know exists, so each side exposes its vault list - database
// names and the credentials needed to read them - on a token-protected endpoint,
// and pulls the other side's list on a timer.
package zone

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dearmai/couch-hub/internal/couch"
	"github.com/dearmai/couch-hub/internal/store"
)

// Export is what a peer pulls from /api/zone/export.
//
// It carries live credentials. The endpoint is token-protected and should only
// be reached over TLS; anyone who can read this payload can read every vault in
// it.
type Export struct {
	// PublicBaseURL is the CouchDB address the peer must use. It is the
	// Obsidian-facing one, since the peer is by definition on another host.
	PublicBaseURL string        `json:"publicBaseUrl"`
	Vaults        []ExportVault `json:"vaults"`
	GeneratedAt   time.Time     `json:"generatedAt"`
}

type ExportVault struct {
	Name          string    `json:"name"`
	DBName        string    `json:"dbName"`
	CouchUser     string    `json:"couchUser"`
	CouchPassword string    `json:"couchPassword"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// TokenMatches compares a presented bearer token against a zone's token without
// leaking length or content through timing.
func TokenMatches(presented, expected string) bool {
	if expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(expected)) == 1
}

// FetchExport pulls a peer's vault registry.
func FetchExport(ctx context.Context, peerURL, token string) (Export, error) {
	base := strings.TrimRight(strings.TrimSpace(peerURL), "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/zone/export", nil)
	if err != nil {
		return Export{}, fmt.Errorf("zone: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return Export{}, fmt.Errorf("zone: peer %s: %w", base, err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		switch res.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return Export{}, fmt.Errorf("zone: peer %s rejected the token", base)
		default:
			return Export{}, fmt.Errorf("zone: peer %s returned %d: %s", base, res.StatusCode, strings.TrimSpace(string(body)))
		}
	}

	var out Export
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return Export{}, fmt.Errorf("zone: decode peer response: %w", err)
	}
	return out, nil
}

// replicationID names a replication document so it is recognisable in
// _replicator and unambiguous across zones and vaults.
func replicationID(zoneID, dbName, direction string) string {
	return fmt.Sprintf("couchhub:%s:%s:%s", zoneID, dbName, direction)
}

// Plan is the set of replication documents a zone should have.
type Plan struct {
	Docs []couch.Replication
	// Skipped explains vaults that could not be included, so the UI can show a
	// reason instead of silently syncing fewer vaults than the operator expects.
	Skipped []string
}

// BuildPlan works out the replication documents for one zone.
//
// local is this hub's view: how CouchHub reaches its own CouchDB, plus each
// local vault's credentials. remote is what the peer exported.
func BuildPlan(
	zone store.Zone,
	localBaseURL string,
	localVaults map[string]LocalVault,
	remote Export,
) Plan {
	var plan Plan

	for _, rv := range remote.Vaults {
		lv, ok := localVaults[rv.DBName]
		if !ok {
			// A vault that exists on the peer but not here. Creating it locally
			// would mean inventing credentials and a database on this side; that
			// is a deliberate action, not something a background sync should do.
			plan.Skipped = append(plan.Skipped,
				fmt.Sprintf("%s: 이 서버에 같은 이름의 Vault가 없습니다", rv.DBName))
			continue
		}

		localEP := couch.NewEndpoint(localBaseURL, lv.DBName, lv.CouchUser, lv.CouchPassword)
		remoteEP := couch.NewEndpoint(remote.PublicBaseURL, rv.DBName, rv.CouchUser, rv.CouchPassword)

		if zone.Direction == store.ZonePull || zone.Direction == store.ZoneBoth {
			plan.Docs = append(plan.Docs, couch.Replication{
				ID:         replicationID(zone.ID, rv.DBName, "pull"),
				Source:     remoteEP,
				Target:     localEP,
				Continuous: true,
				Owner:      "couchhub",
			})
		}
		if zone.Direction == store.ZonePush || zone.Direction == store.ZoneBoth {
			plan.Docs = append(plan.Docs, couch.Replication{
				ID:         replicationID(zone.ID, rv.DBName, "push"),
				Source:     localEP,
				Target:     remoteEP,
				Continuous: true,
				Owner:      "couchhub",
			})
		}
	}

	return plan
}

// LocalVault is the local half of a replication pair.
type LocalVault struct {
	DBName        string
	CouchUser     string
	CouchPassword string
}

// Apply writes a plan's replication documents.
func Apply(ctx context.Context, c *couch.Client, plan Plan) error {
	for _, doc := range plan.Docs {
		if err := c.PutReplication(ctx, doc); err != nil {
			return err
		}
	}
	return nil
}

// Remove deletes every replication document belonging to a zone.
func Remove(ctx context.Context, c *couch.Client, zoneID string, dbNames []string) error {
	var problems []string
	for _, db := range dbNames {
		for _, dir := range []string{"pull", "push"} {
			if err := c.DeleteReplication(ctx, replicationID(zoneID, db, dir)); err != nil {
				problems = append(problems, fmt.Sprintf("%s/%s: %v", db, dir, err))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("zone: 복제 문서 정리 실패: %s", strings.Join(problems, "; "))
	}
	return nil
}

// BelongsTo reports whether a scheduler document was created by this zone, so
// the UI can show only the relevant replication states.
func BelongsTo(docID, zoneID string) bool {
	return strings.HasPrefix(docID, "couchhub:"+zoneID+":")
}
