// Package provision applies the CouchDB configuration obsidian-livesync
// requires, and reports how a server currently differs from it.
//
// The wizard shows the diff before touching anything. That matters most for the
// "connect an existing CouchDB" case, where the operator needs to see which of
// their settings CouchHub is about to overwrite rather than find out afterwards.
package provision

import (
	"context"
	"fmt"
	"strings"

	"github.com/dearmai/couch-hub/internal/couch"
)

// Setting is one CouchDB configuration value livesync needs.
type Setting struct {
	Section string `json:"section"`
	Key     string `json:"key"`
	Value   string `json:"value"`
	// Why explains the setting in the UI, so an operator can judge whether they
	// are willing to have it changed.
	Why string `json:"why"`
}

// CORSOrigins are the origins the Obsidian client presents.
//
//   - app://obsidian.md        desktop
//   - capacitor://localhost    iOS
//   - http://localhost         Android
const CORSOrigins = "app://obsidian.md,capacitor://localhost,http://localhost"

// Desired is the configuration CouchHub applies, taken from
// obsidian-livesync's utils/couchdb/provision.ts and docs/setup_own_server.md.
//
// One entry goes beyond that reference: require_valid_user_except_for_up.
// Turning on require_valid_user makes /_up answer 401, which silently breaks
// every health check pointed at the server - compose, orchestrator probes,
// uptime monitors - so they report a healthy CouchDB as down. The exemption
// covers /_up only; every other endpoint still demands credentials.
var Desired = []Setting{
	{"chttpd", "require_valid_user", "true",
		"익명 접근 차단. livesync가 자격증명을 항상 보내도록 강제합니다."},
	{"chttpd_auth", "require_valid_user", "true",
		"인증 엔드포인트에도 동일하게 적용합니다."},
	{"chttpd", "require_valid_user_except_for_up", "true",
		"헬스체크 경로 /_up 만 인증 없이 허용합니다. 이게 없으면 compose healthcheck와 " +
			"모니터링이 401을 받아 서버가 죽은 것으로 오인합니다."},
	{"httpd", "WWW-Authenticate", `Basic realm="couchdb"`,
		"401 응답에 Basic 인증 챌린지를 붙여 클라이언트가 재인증하게 합니다."},
	{"httpd", "enable_cors", "true",
		"CORS 활성화 (구 HTTP 스택)."},
	{"chttpd", "enable_cors", "true",
		"CORS 활성화 (클러스터 HTTP 스택). Obsidian은 별도 오리진에서 요청합니다."},
	{"chttpd", "max_http_request_size", "4294967296",
		"대용량 첨부파일 동기화 시 요청이 잘리지 않도록 4GiB로 확장합니다."},
	{"couchdb", "max_document_size", "50000000",
		"livesync 청크 문서 상한 (50MB)."},
	{"cors", "credentials", "true",
		"인증 정보를 포함한 교차 오리진 요청을 허용합니다."},
	{"cors", "origins", CORSOrigins,
		"Obsidian 데스크톱/iOS/Android 오리진만 허용합니다."},
	{"cors", "headers", "accept, authorization, content-type, origin, referer",
		"livesync가 보내는 요청 헤더를 허용합니다."},
	{"cors", "methods", "GET,PUT,POST,HEAD,DELETE",
		"복제에 필요한 HTTP 메서드를 허용합니다."},
	{"cors", "max_age", "3600",
		"프리플라이트 응답을 1시간 캐시해 왕복을 줄입니다."},
}

// SystemDatabases must exist before CouchDB stops logging errors and before
// replication works at all.
var SystemDatabases = []string{"_users", "_replicator", "_global_changes"}

// Check is one setting compared against the live server.
type Check struct {
	Setting
	Current string `json:"current"`
	// Matches is true when the server already has the desired value.
	Matches bool `json:"matches"`
	// Present is false when the section or key is absent entirely, which reads
	// differently in the UI from "set, but to something else".
	Present bool `json:"present"`
}

// Diagnosis is what the wizard shows before applying anything.
type Diagnosis struct {
	Version string `json:"version"`
	// SingleNode is false when _membership reports more than one node; CouchHub
	// only configures the local node, so a cluster needs manual attention.
	SingleNode bool `json:"singleNode"`
	NodeCount  int  `json:"nodeCount"`

	Checks []Check `json:"checks"`
	// MissingSystemDBs lists system databases that still need creating.
	MissingSystemDBs []string `json:"missingSystemDbs"`

	// Ready is true when nothing needs changing.
	Ready bool `json:"ready"`
}

// Diagnose reads the server and compares it against Desired without modifying
// anything.
func Diagnose(ctx context.Context, c *couch.Client) (Diagnosis, error) {
	welcome, err := c.Ping(ctx)
	if err != nil {
		return Diagnosis{}, err
	}

	d := Diagnosis{Version: welcome.Version, SingleNode: true, NodeCount: 1}

	// A fresh CouchDB answers _membership before cluster setup, so a failure
	// here is informational rather than fatal.
	if m, err := c.Membership(ctx); err == nil {
		d.NodeCount = len(m.ClusterNodes)
		d.SingleNode = d.NodeCount <= 1
	}

	cfg, err := c.Config(ctx)
	if err != nil {
		return Diagnosis{}, fmt.Errorf("provision: read configuration: %w", err)
	}

	d.Checks = make([]Check, 0, len(Desired))
	for _, want := range Desired {
		current, present := cfg[want.Section][want.Key]
		d.Checks = append(d.Checks, Check{
			Setting: want,
			Current: current,
			Present: present,
			Matches: present && configEqual(want.Section, want.Key, current, want.Value),
		})
	}

	existing, err := c.AllDBs(ctx)
	if err != nil {
		return Diagnosis{}, fmt.Errorf("provision: list databases: %w", err)
	}
	have := make(map[string]bool, len(existing))
	for _, name := range existing {
		have[name] = true
	}
	for _, name := range SystemDatabases {
		if !have[name] {
			d.MissingSystemDBs = append(d.MissingSystemDBs, name)
		}
	}

	d.Ready = len(d.MissingSystemDBs) == 0
	for _, chk := range d.Checks {
		if !chk.Matches {
			d.Ready = false
			break
		}
	}
	return d, nil
}

// configEqual compares a live value with the desired one, tolerating the
// formatting CouchDB is known to normalise.
func configEqual(section, key, current, want string) bool {
	if current == want {
		return true
	}
	// CouchDB stores the WWW-Authenticate value verbatim but operators (and some
	// images) vary the quoting, and the comma-separated lists tolerate spacing
	// differences that make no functional difference.
	switch {
	case section == "cors" && (key == "origins" || key == "headers" || key == "methods"):
		return normaliseList(current) == normaliseList(want)
	default:
		return false
	}
}

func normaliseList(s string) string {
	parts := strings.Split(s, ",")
	for i, p := range parts {
		parts[i] = strings.ToLower(strings.TrimSpace(p))
	}
	return strings.Join(parts, ",")
}

// StepResult records the outcome of one action during Apply.
type StepResult struct {
	Step    string `json:"step"`
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped"`
	Error   string `json:"error,omitempty"`
}

// Apply brings a server up to the livesync configuration.
//
// It is safe to re-run: single-node setup and database creation both treat
// "already done" as success, and configuration writes are idempotent.
func Apply(ctx context.Context, c *couch.Client, adminUser, adminPassword string) ([]StepResult, error) {
	var results []StepResult

	// enable_single_node answers 400 on an already-configured server. That is
	// the normal path when reconnecting to an existing CouchDB, not a failure.
	err := c.EnableSingleNode(ctx, adminUser, adminPassword)
	switch {
	case err == nil:
		results = append(results, StepResult{Step: "cluster: enable single node", OK: true})
	case couch.IsConflict(err) || isAlreadySetUp(err):
		results = append(results, StepResult{Step: "cluster: enable single node", OK: true, Skipped: true})
	default:
		results = append(results, StepResult{Step: "cluster: enable single node", Error: err.Error()})
		return results, fmt.Errorf("provision: single node setup: %w", err)
	}

	for _, db := range SystemDatabases {
		step := "database: " + db
		err := c.CreateDB(ctx, db)
		switch {
		case err == nil:
			results = append(results, StepResult{Step: step, OK: true})
		case couch.IsConflict(err):
			results = append(results, StepResult{Step: step, OK: true, Skipped: true})
		default:
			results = append(results, StepResult{Step: step, Error: err.Error()})
			return results, fmt.Errorf("provision: create %s: %w", db, err)
		}
	}

	for _, s := range Desired {
		step := fmt.Sprintf("config: [%s] %s", s.Section, s.Key)
		if err := c.SetConfig(ctx, s.Section, s.Key, s.Value); err != nil {
			results = append(results, StepResult{Step: step, Error: err.Error()})
			return results, fmt.Errorf("provision: set [%s] %s: %w", s.Section, s.Key, err)
		}
		results = append(results, StepResult{Step: step, OK: true})
	}

	return results, nil
}

// isAlreadySetUp recognises the 400 CouchDB returns when single-node setup has
// already run.
func isAlreadySetUp(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "cluster is already enabled") ||
		strings.Contains(strings.ToLower(err.Error()), "system_database_exists")
}
