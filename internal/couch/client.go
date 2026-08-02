// Package couch is a small CouchDB HTTP client covering only what CouchHub
// needs: reading and writing server configuration, creating databases and
// users, and reading per-database statistics.
//
// A hand-rolled client rather than a driver dependency, because the surface is
// this narrow and the config endpoints most drivers omit are exactly the ones
// the install wizard depends on.
package couch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to one CouchDB server as an administrator.
type Client struct {
	baseURL  *url.URL
	user     string
	password string
	http     *http.Client
}

// Option customises a Client.
type Option func(*Client)

// WithHTTPClient overrides the underlying HTTP client, mainly for tests.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// New builds a client for baseURL, e.g. "http://couchdb:5984".
func New(baseURL, user, password string, opts ...Option) (*Client, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("couch: parse base URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("couch: base URL %q must start with http:// or https://", baseURL)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("couch: base URL %q has no host", baseURL)
	}

	c := &Client{
		baseURL:  u,
		user:     user,
		password: password,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// BaseURL returns the server address this client was built for.
func (c *Client) BaseURL() string { return c.baseURL.String() }

// Error is a non-2xx response from CouchDB.
type Error struct {
	StatusCode int    `json:"status"`
	Err        string `json:"error"`
	Reason     string `json:"reason"`
}

func (e *Error) Error() string {
	switch {
	case e.Reason != "" && e.Err != "":
		return fmt.Sprintf("couchdb %d %s: %s", e.StatusCode, e.Err, e.Reason)
	case e.Err != "":
		return fmt.Sprintf("couchdb %d %s", e.StatusCode, e.Err)
	default:
		return fmt.Sprintf("couchdb %d", e.StatusCode)
	}
}

// IsNotFound reports a 404, which CouchDB uses for both missing databases and
// missing documents.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.StatusCode == http.StatusNotFound
}

// IsUnauthorized reports a 401 or 403.
func IsUnauthorized(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden)
}

// IsConflict reports a 412 or 409 - "already exists" for databases and
// documents respectively.
func IsConflict(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.StatusCode == http.StatusConflict || e.StatusCode == http.StatusPreconditionFailed)
}

// do issues a request against path (already escaped) and decodes a JSON
// response into out, which may be nil.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("couch: marshal request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL.String()+path, reader)
	if err != nil {
		return fmt.Errorf("couch: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.password)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("couch: %s %s: %w", method, path, err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		// Cap the error body: a misrouted request can land on something that
		// streams megabytes of HTML.
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<10))
		apiErr := &Error{StatusCode: res.StatusCode}
		_ = json.Unmarshal(raw, apiErr)
		apiErr.StatusCode = res.StatusCode
		if apiErr.Err == "" {
			apiErr.Err = strings.TrimSpace(string(raw))
		}
		return apiErr
	}

	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("couch: decode %s %s: %w", method, path, err)
	}
	return nil
}

// escapeDB escapes a database name for use in a path. CouchDB allows '/' and
// '+' in database names, both of which must be percent-encoded.
func escapeDB(name string) string {
	return strings.ReplaceAll(url.PathEscape(name), "+", "%2B")
}

// --- server ----------------------------------------------------------------

// Welcome is GET /.
type Welcome struct {
	CouchDB  string   `json:"couchdb"`
	Version  string   `json:"version"`
	UUID     string   `json:"uuid"`
	Features []string `json:"features"`
}

// Ping fetches the server banner, confirming both reachability and credentials.
func (c *Client) Ping(ctx context.Context) (Welcome, error) {
	var w Welcome
	err := c.do(ctx, http.MethodGet, "/", nil, &w)
	return w, err
}

// Up reports whether the server is ready to serve requests (GET /_up).
func (c *Client) Up(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/_up", nil, nil)
}

// Membership is GET /_membership.
type Membership struct {
	AllNodes     []string `json:"all_nodes"`
	ClusterNodes []string `json:"cluster_nodes"`
}

func (c *Client) Membership(ctx context.Context) (Membership, error) {
	var m Membership
	err := c.do(ctx, http.MethodGet, "/_membership", nil, &m)
	return m, err
}

// --- configuration ---------------------------------------------------------

// Config returns the whole local node configuration as section -> key -> value.
//
// _local refers to "the node handling this request", which is what a
// single-node homelab deployment wants; a real cluster would need each node
// configured individually.
func (c *Client) Config(ctx context.Context) (map[string]map[string]string, error) {
	var cfg map[string]map[string]string
	err := c.do(ctx, http.MethodGet, "/_node/_local/_config", nil, &cfg)
	return cfg, err
}

// SetConfig writes one configuration value.
func (c *Client) SetConfig(ctx context.Context, section, key, value string) error {
	path := "/_node/_local/_config/" + url.PathEscape(section) + "/" + url.PathEscape(key)
	// CouchDB expects a bare JSON string as the body and echoes the previous
	// value, which we do not need.
	return c.do(ctx, http.MethodPut, path, value, nil)
}

// --- cluster ---------------------------------------------------------------

// EnableSingleNode runs the single-node cluster setup, which also creates the
// _users, _replicator and _global_changes system databases.
//
// It is idempotent in effect but not in status: a server that has already been
// set up answers 400, which the caller should treat as success.
func (c *Client) EnableSingleNode(ctx context.Context, user, password string) error {
	body := map[string]any{
		"action":       "enable_single_node",
		"username":     user,
		"password":     password,
		"bind_address": "0.0.0.0",
		"singlenode":   true,
	}
	return c.do(ctx, http.MethodPost, "/_cluster_setup", body, nil)
}

// --- databases -------------------------------------------------------------

// CreateDB creates a database, reporting an existing one via IsConflict.
func (c *Client) CreateDB(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodPut, "/"+escapeDB(name), nil, nil)
}

// DeleteDB removes a database and all its documents.
func (c *Client) DeleteDB(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/"+escapeDB(name), nil, nil)
}

// DBExists reports whether a database is present.
func (c *Client) DBExists(ctx context.Context, name string) (bool, error) {
	err := c.do(ctx, http.MethodHead, "/"+escapeDB(name), nil, nil)
	switch {
	case err == nil:
		return true, nil
	case IsNotFound(err):
		return false, nil
	default:
		return false, err
	}
}

// DBInfo is GET /{db}.
type DBInfo struct {
	DBName      string `json:"db_name"`
	DocCount    int64  `json:"doc_count"`
	DocDelCount int64  `json:"doc_del_count"`
	// UpdateSeq is opaque in CouchDB 3.x, formatted as "1234-g1AAAA...".
	UpdateSeq string `json:"update_seq"`
	Sizes     struct {
		File     int64 `json:"file"`
		External int64 `json:"external"`
		Active   int64 `json:"active"`
	} `json:"sizes"`
}

// SeqNum extracts the numeric prefix of UpdateSeq.
//
// The delta of this number between polls is CouchHub's write counter: it costs
// one request regardless of how busy the database is, whereas counting via
// _changes would scale with the number of documents. It is a lower bound, since
// CouchDB may advance the sequence for internal reasons, and it resets if the
// database is recreated - callers must discard negative deltas.
func (i DBInfo) SeqNum() int64 {
	prefix, _, _ := strings.Cut(i.UpdateSeq, "-")
	n, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func (c *Client) DBInfo(ctx context.Context, name string) (DBInfo, error) {
	var info DBInfo
	err := c.do(ctx, http.MethodGet, "/"+escapeDB(name), nil, &info)
	return info, err
}

// AllDBs lists databases, system databases included.
func (c *Client) AllDBs(ctx context.Context) ([]string, error) {
	var names []string
	err := c.do(ctx, http.MethodGet, "/_all_dbs", nil, &names)
	return names, err
}

// --- security --------------------------------------------------------------

// SecurityNames is one half of a _security document.
type SecurityNames struct {
	Names []string `json:"names"`
	Roles []string `json:"roles"`
}

// Security is a database's _security document.
type Security struct {
	Admins  SecurityNames `json:"admins"`
	Members SecurityNames `json:"members"`
}

// SetSecurity replaces a database's _security document.
func (c *Client) SetSecurity(ctx context.Context, db string, sec Security) error {
	// CouchDB rejects a _security document with null name/role arrays.
	if sec.Admins.Names == nil {
		sec.Admins.Names = []string{}
	}
	if sec.Admins.Roles == nil {
		sec.Admins.Roles = []string{}
	}
	if sec.Members.Names == nil {
		sec.Members.Names = []string{}
	}
	if sec.Members.Roles == nil {
		sec.Members.Roles = []string{}
	}
	return c.do(ctx, http.MethodPut, "/"+escapeDB(db)+"/_security", sec, nil)
}

func (c *Client) Security(ctx context.Context, db string) (Security, error) {
	var sec Security
	err := c.do(ctx, http.MethodGet, "/"+escapeDB(db)+"/_security", nil, &sec)
	return sec, err
}

// --- users -----------------------------------------------------------------

const userDocPrefix = "org.couchdb.user:"

// CreateUser adds a non-admin account to _users, or resets the password of one
// that is already there.
//
// The reset is the point. An account left behind by an earlier vault - a
// database adopted twice, a teardown that half finished - makes a plain PUT
// answer 409, and treating that as success stores a password that authenticates
// nowhere. Nothing reports it until a client or a replication tries to log in
// and gets "Name or password is incorrect" against an account that plainly
// exists.
//
// Names are CouchHub's own (vault_<database>), so an existing document under
// one is a leftover to reclaim rather than someone else's account.
//
// CouchDB hashes the password itself when the document carries a plain
// `password` field, so it is never stored in the clear.
func (c *Client) CreateUser(ctx context.Context, name, password string, roles []string) error {
	if roles == nil {
		roles = []string{}
	}
	doc := map[string]any{
		"_id":      userDocPrefix + name,
		"name":     name,
		"type":     "user",
		"roles":    roles,
		"password": password,
	}
	path := "/_users/" + url.PathEscape(userDocPrefix+name)

	var existing struct {
		Rev string `json:"_rev"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &existing); err == nil {
		doc["_rev"] = existing.Rev
	} else if !IsNotFound(err) {
		return err
	}

	return c.do(ctx, http.MethodPut, path, doc, nil)
}

// DeleteUser removes an account from _users. A missing account is not an error,
// so vault teardown stays idempotent.
func (c *Client) DeleteUser(ctx context.Context, name string) error {
	path := "/_users/" + url.PathEscape(userDocPrefix+name)

	var doc struct {
		Rev string `json:"_rev"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &doc); err != nil {
		if IsNotFound(err) {
			return nil
		}
		return err
	}
	return c.do(ctx, http.MethodDelete, path+"?rev="+url.QueryEscape(doc.Rev), nil, nil)
}

// UserExists reports whether an account is present in _users.
func (c *Client) UserExists(ctx context.Context, name string) (bool, error) {
	path := "/_users/" + url.PathEscape(userDocPrefix+name)
	err := c.do(ctx, http.MethodHead, path, nil, nil)
	switch {
	case err == nil:
		return true, nil
	case IsNotFound(err):
		return false, nil
	default:
		return false, err
	}
}
