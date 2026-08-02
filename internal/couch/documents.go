package couch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Row is one entry of an _all_docs response, with the document included.
type Row struct {
	ID  string          `json:"id"`
	Key string          `json:"key"`
	Doc json.RawMessage `json:"doc"`
}

// GetDoc fetches a single document into out.
func (c *Client) GetDoc(ctx context.Context, db, docID string, out any) error {
	path := "/" + escapeDB(db) + "/" + escapeDocID(docID)
	return c.do(ctx, http.MethodGet, path, nil, out)
}

// AllDocsWithDocs lists documents with their bodies.
//
// limit is applied server-side: a livesync vault holds one document per chunk,
// so an unbounded listing would pull the entire vault over the wire to show a
// page of notes.
func (c *Client) AllDocsWithDocs(ctx context.Context, db string, limit int) ([]Row, error) {
	if limit <= 0 {
		limit = 500
	}
	q := url.Values{}
	q.Set("include_docs", "true")
	q.Set("limit", strconv.Itoa(limit))

	var out struct {
		Rows []Row `json:"rows"`
	}
	path := "/" + escapeDB(db) + "/_all_docs?" + q.Encode()
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Rows, nil
}

// BulkDocs fetches specific documents by id in one request.
//
// POST _all_docs with keys rather than one GET per chunk: a note of any size is
// dozens of chunks, and the round trips dominate everything else.
func (c *Client) BulkDocs(ctx context.Context, db string, ids []string) ([]Row, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	body := map[string]any{"keys": ids}
	var out struct {
		Rows []Row `json:"rows"`
	}
	path := "/" + escapeDB(db) + "/_all_docs?include_docs=true"
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, fmt.Errorf("couch: bulk fetch %d docs: %w", len(ids), err)
	}
	return out.Rows, nil
}

// escapeDocID escapes a document id for use in a path. Ids beginning with
// _design/ or _local/ keep their slash, which CouchDB requires.
func escapeDocID(id string) string {
	if prefix, rest, found := cutDesignOrLocal(id); found {
		return prefix + url.PathEscape(rest)
	}
	return url.PathEscape(id)
}

func cutDesignOrLocal(id string) (prefix, rest string, found bool) {
	for _, p := range []string{"_design/", "_local/"} {
		if len(id) > len(p) && id[:len(p)] == p {
			return p, id[len(p):], true
		}
	}
	return "", id, false
}
