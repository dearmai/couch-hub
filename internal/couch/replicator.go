package couch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Replication is a document in the _replicator database.
//
// CouchDB itself runs the replication once the document exists, which is why
// CouchHub only writes documents here and never proxies data: a copy keeps
// running while CouchHub is down or being upgraded.
type Replication struct {
	ID     string   `json:"_id"`
	Rev    string   `json:"_rev,omitempty"`
	Source Endpoint `json:"source"`
	Target Endpoint `json:"target"`
	// Continuous keeps the replication running after the initial pass. A
	// one-off copy - moving a vault to another server - leaves it false so the
	// job reaches a terminal state instead of idling forever.
	Continuous bool `json:"continuous"`
	// CreateTarget is deliberately left false: the target database is created by
	// CouchHub with a _security document, and letting the replicator create it
	// would leave it world-readable to any account on the server.
	CreateTarget bool `json:"create_target"`
	// Owner records which CouchHub wrote the document, purely for humans reading
	// _replicator directly.
	Owner string `json:"couchhub_owner,omitempty"`
}

// Endpoint is one side of a replication, with its credentials.
//
// Always a full URL, including for a database on the server holding the
// document: CouchDB 3.x answers 403 local_endpoints_not_supported to the bare
// database name that older versions accepted.
type Endpoint struct {
	URL     string            `json:"url"`
	Auth    *ReplicationAuth  `json:"auth,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type ReplicationAuth struct {
	Basic *BasicAuth `json:"basic,omitempty"`
}

type BasicAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// NewEndpoint builds a replication endpoint for a database on a server.
func NewEndpoint(baseURL, dbName, user, password string) Endpoint {
	base := baseURL
	for len(base) > 0 && base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	ep := Endpoint{URL: base + "/" + escapeDB(dbName)}
	if user != "" {
		ep.Auth = &ReplicationAuth{Basic: &BasicAuth{Username: user, Password: password}}
	}
	return ep
}

// PutReplication creates or updates a replication document.
//
// CouchDB rejects an update without the current _rev, so an existing document
// is fetched first. Replication documents are also immutable in effect - the
// scheduler will not pick up an edit reliably - so an existing one is replaced.
func (c *Client) PutReplication(ctx context.Context, doc Replication) error {
	path := "/_replicator/" + url.PathEscape(doc.ID)

	var existing struct {
		Rev string `json:"_rev"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &existing); err == nil {
		doc.Rev = existing.Rev
	} else if !IsNotFound(err) {
		return err
	}

	if err := c.do(ctx, http.MethodPut, path, doc, nil); err != nil {
		return fmt.Errorf("couch: put replication %q: %w", doc.ID, err)
	}
	return nil
}

// DeleteReplication removes a replication document, stopping the replication.
// A missing document is not an error.
func (c *Client) DeleteReplication(ctx context.Context, id string) error {
	path := "/_replicator/" + url.PathEscape(id)

	var existing struct {
		Rev string `json:"_rev"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &existing); err != nil {
		if IsNotFound(err) {
			return nil
		}
		return err
	}
	return c.do(ctx, http.MethodDelete, path+"?rev="+url.QueryEscape(existing.Rev), nil, nil)
}

// ReplicationStatus is what the scheduler reports about one replication.
type ReplicationStatus struct {
	DocID string `json:"docId"`
	// Exists is false when no replication document is present - either it was
	// never written, or it has already been cleaned up.
	Exists bool `json:"exists"`
	// State is CouchDB's scheduler state: initializing, running, pending,
	// crashing, error, completed, failed.
	State string `json:"state"`
	// Error carries the scheduler's reason for a crashing or failed job.
	Error string `json:"error,omitempty"`

	DocsRead    int64 `json:"docsRead"`
	DocsWritten int64 `json:"docsWritten"`
	// ChangesPending is CouchDB's estimate of the remaining backlog. It is -1
	// when the server does not report one, which includes every finished job -
	// so it is a progress hint, never the completion test.
	ChangesPending int64 `json:"changesPending"`

	LastUpdated string `json:"lastUpdated,omitempty"`
	StartTime   string `json:"startTime,omitempty"`
}

// Done reports a finished copy. Only "completed" counts: a crashing job is one
// the scheduler is still retrying with a backoff, not one that has stopped.
func (s ReplicationStatus) Done() bool { return s.State == "completed" }

// ReplicationStatus reads the scheduler's view of one replication document.
//
// A missing document is reported through Exists rather than as an error: the
// caller usually wants to know whether a job is still around, and a 404 is a
// perfectly ordinary answer to that.
func (c *Client) ReplicationStatus(ctx context.Context, id string) (ReplicationStatus, error) {
	var raw struct {
		DocID       string          `json:"doc_id"`
		State       string          `json:"state"`
		Info        json.RawMessage `json:"info"`
		ErrorCount  int             `json:"error_count"`
		LastUpdated string          `json:"last_updated"`
		StartTime   string          `json:"start_time"`
	}

	path := "/_scheduler/docs/_replicator/" + url.PathEscape(id)
	if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		if IsNotFound(err) {
			return ReplicationStatus{DocID: id, ChangesPending: -1}, nil
		}
		return ReplicationStatus{}, err
	}

	out := ReplicationStatus{
		DocID:          id,
		Exists:         true,
		State:          raw.State,
		ChangesPending: -1,
		LastUpdated:    raw.LastUpdated,
		StartTime:      raw.StartTime,
	}

	// info is an object while the job runs, an object carrying `error` once it
	// crashes, and null before it starts - so a decode failure here is expected
	// and says nothing about the job.
	var info struct {
		Error          string `json:"error"`
		DocsRead       int64  `json:"docs_read"`
		DocsWritten    int64  `json:"docs_written"`
		ChangesPending *int64 `json:"changes_pending"`
	}
	if len(raw.Info) > 0 {
		if err := json.Unmarshal(raw.Info, &info); err == nil {
			out.Error = info.Error
			out.DocsRead = info.DocsRead
			out.DocsWritten = info.DocsWritten
			if info.ChangesPending != nil {
				out.ChangesPending = *info.ChangesPending
			}
		} else {
			// Some versions report a bare string for a failed job.
			var reason string
			if json.Unmarshal(raw.Info, &reason) == nil {
				out.Error = reason
			}
		}
	}
	return out, nil
}
