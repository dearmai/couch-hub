package couch

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// Replication is a document in the _replicator database.
//
// CouchDB itself runs the replication once the document exists, which is why
// CouchHub only writes documents here and never proxies data: a zone keeps
// working while CouchHub is down or being upgraded.
type Replication struct {
	ID         string   `json:"_id"`
	Rev        string   `json:"_rev,omitempty"`
	Source     Endpoint `json:"source"`
	Target     Endpoint `json:"target"`
	Continuous bool     `json:"continuous"`
	// CreateTarget is deliberately left false: the target database is created by
	// CouchHub with a _security document, and letting the replicator create it
	// would leave it world-readable to any account on the server.
	CreateTarget bool `json:"create_target"`
	// Owner records which CouchHub wrote the document, purely for humans reading
	// _replicator directly.
	Owner string `json:"couchhub_owner,omitempty"`
}

// Endpoint is one side of a replication, with its credentials.
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

// SchedulerDoc is one entry of GET /_scheduler/docs.
type SchedulerDoc struct {
	DocID string `json:"doc_id"`
	// State is one of initializing, running, pending, crashing, error, completed.
	State string `json:"state"`
	Info  any    `json:"info"`
	// ErrorCount is how many times the job has failed since it last succeeded.
	ErrorCount  int    `json:"error_count"`
	LastUpdated string `json:"last_updated"`
	StartTime   string `json:"start_time"`
}

// SchedulerDocs reports the live state of every replication on the server.
func (c *Client) SchedulerDocs(ctx context.Context) ([]SchedulerDoc, error) {
	var out struct {
		Docs []SchedulerDoc `json:"docs"`
	}
	if err := c.do(ctx, http.MethodGet, "/_scheduler/docs", nil, &out); err != nil {
		return nil, err
	}
	return out.Docs, nil
}
