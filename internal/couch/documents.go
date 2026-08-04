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

// allDocIDsPage is how many ids one _all_docs request asks for. Large, because
// each row is a short string and the round trip dominates.
const allDocIDsPage = 10000

// AllDocIDs lists every document id in a database, paging until it runs out.
//
// Ids only, deliberately. A livesync vault is overwhelmingly content chunks, so
// include_docs here would pull the whole vault over the wire before anything
// had been selected - and the caller's first act is to throw the chunks away.
func (c *Client) AllDocIDs(ctx context.Context, db string) ([]string, error) {
	var out []string
	startKey := ""

	for {
		q := url.Values{}
		// One extra row: it is the next page's first id, which is how the walk
		// resumes without skip= and without re-reading a row.
		q.Set("limit", strconv.Itoa(allDocIDsPage+1))
		if startKey != "" {
			raw, err := json.Marshal(startKey)
			if err != nil {
				return nil, fmt.Errorf("couch: encode start key: %w", err)
			}
			q.Set("start_key", string(raw))
		}

		var res struct {
			Rows []struct {
				ID string `json:"id"`
			} `json:"rows"`
		}
		path := "/" + escapeDB(db) + "/_all_docs?" + q.Encode()
		if err := c.do(ctx, http.MethodGet, path, nil, &res); err != nil {
			return nil, err
		}
		if len(res.Rows) == 0 {
			return out, nil
		}

		rows := res.Rows
		next := ""
		if len(rows) > allDocIDsPage {
			next = rows[len(rows)-1].ID
			rows = rows[:allDocIDsPage]
		}
		for _, row := range rows {
			out = append(out, row.ID)
		}
		if next == "" {
			return out, nil
		}
		startKey = next
	}
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

// --- local documents --------------------------------------------------------
//
// A _local document is a database's own state: no revision history, and -
// crucially - replication never copies it. Whatever a client keeps there
// survives a move only if something carries it across deliberately.

// LocalDocIDs lists the _local documents in a database.
func (c *Client) LocalDocIDs(ctx context.Context, db string) ([]string, error) {
	var out struct {
		Rows []struct {
			ID string `json:"id"`
		} `json:"rows"`
	}
	if err := c.do(ctx, http.MethodGet, "/"+escapeDB(db)+"/_local_docs", nil, &out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Rows))
	for _, row := range out.Rows {
		ids = append(ids, row.ID)
	}
	return ids, nil
}

// LocalDoc fetches one _local document.
func (c *Client) LocalDoc(ctx context.Context, db, id string) (map[string]any, error) {
	var doc map[string]any
	err := c.do(ctx, http.MethodGet, "/"+escapeDB(db)+"/"+url.PathEscape(id), nil, &doc)
	return doc, err
}

// PutLocalDoc writes one _local document, replacing what is there.
//
// The revision is read from the target rather than taken from the document:
// a _local revision is per-database bookkeeping, so one copied from elsewhere
// is meaningless here and CouchDB rejects it as a conflict.
func (c *Client) PutLocalDoc(ctx context.Context, db, id string, doc map[string]any) error {
	path := "/" + escapeDB(db) + "/" + url.PathEscape(id)

	var existing struct {
		Rev string `json:"_rev"`
	}
	switch err := c.do(ctx, http.MethodGet, path, nil, &existing); {
	case err == nil:
		doc["_rev"] = existing.Rev
	case IsNotFound(err):
		delete(doc, "_rev")
	default:
		return err
	}

	if err := c.do(ctx, http.MethodPut, path, doc, nil); err != nil {
		return fmt.Errorf("couch: put %s: %w", id, err)
	}
	return nil
}
