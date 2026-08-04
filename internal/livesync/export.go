package livesync

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// prefixChunk is the id livesync gives content chunks (PREFIX_CHUNK in
// livesync-commonlib). Encrypted chunks use "h:+", which is inside it.
//
// It is what lets a whole-vault walk stay cheap: the chunks outnumber the files
// by orders of magnitude, and skipping them by id means their bodies are
// fetched once, when the file that owns them is assembled, rather than twice.
const prefixChunk = "h:"

// idBatch caps how many ids go into one _all_docs?keys= request. A large binary
// file is thousands of chunks, and one request for every id at once is a body
// CouchDB may well refuse.
const idBatch = 400

// Entry is one file as an export sees it: the document, plus everything needed
// to reassemble its content without fetching the entry again.
type Entry struct {
	Document
	// Children are the chunk ids, already unwrapped from the encrypted metadata
	// bundle when there is one.
	Children []string
	// Data is a legacy entry's inline content, used when there are no chunks.
	Data any
	// Binary marks content stored as base64 rather than as text.
	Binary bool
}

// isBinary reports whether an entry's chunks hold base64 rather than text.
//
// Both fields are consulted: `type` carries it for a note, while an
// internalfile - anything under .obsidian - says so in `datatype` and leaves
// `type` naming the category instead.
func isBinary(e rawEntry) bool {
	return e.Type == TypeNoteBinary || e.Type2 == TypeNoteBinary
}

// ListAll enumerates every file in the vault, including ones the listing in
// List would have capped away.
//
// Deleted entries are returned rather than filtered: a tombstone is a fact
// about the vault, and it is the caller that decides whether a backup wants it.
func (r *Reader) ListAll(ctx context.Context) ([]Entry, error) {
	ids, err := r.client.AllDocIDs(ctx, r.dbName)
	if err != nil {
		return nil, err
	}

	candidates := make([]string, 0, len(ids)/8)
	for _, id := range ids {
		switch {
		case strings.HasPrefix(id, prefixChunk),
			strings.HasPrefix(id, "_design/"),
			strings.HasPrefix(id, "_local/"):
			continue
		}
		candidates = append(candidates, id)
	}

	out := make([]Entry, 0, len(candidates))
	for start := 0; start < len(candidates); start += idBatch {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows, err := r.client.BulkDocs(ctx, r.dbName, candidates[start:min(start+idBatch, len(candidates))])
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			var e rawEntry
			if len(row.Doc) == 0 || json.Unmarshal(row.Doc, &e) != nil {
				continue
			}
			switch e.Type {
			case TypeNotePlain, TypeNoteBinary, TypeNoteLegacy, TypeInternalFile:
			default:
				// The plugin's own bookkeeping: milestones, sync parameters,
				// and anything else that is not a file.
				continue
			}
			doc, children := r.resolve(row.ID, e)
			out = append(out, Entry{Document: doc, Children: children, Data: e.Data, Binary: isBinary(e)})
		}
	}
	return out, nil
}

// Fetch reassembles one entry's content from its chunks.
//
// Safe to call concurrently on one Reader: the key cache is guarded and the
// HTTP client is shared by design.
func (r *Reader) Fetch(ctx context.Context, e Entry) (Content, error) {
	out := Content{Document: e.Document, Binary: e.Binary}

	// A legacy entry carries its content inline rather than in chunks.
	if len(e.Children) == 0 {
		parts, err := inlinePieces(e.Data)
		if err != nil {
			return out, err
		}
		pieces := make([]string, 0, len(parts))
		for _, part := range parts {
			decrypted, err := r.decrypt(part)
			if err != nil {
				return out, err
			}
			pieces = append(pieces, decrypted)
		}
		out.setPieces(pieces)
		return out, nil
	}

	byID := make(map[string]string, len(e.Children))
	for start := 0; start < len(e.Children); start += idBatch {
		rows, err := r.client.BulkDocs(ctx, r.dbName, e.Children[start:min(start+idBatch, len(e.Children))])
		if err != nil {
			return out, err
		}
		for _, row := range rows {
			var leaf struct {
				Data string `json:"data"`
			}
			if len(row.Doc) == 0 || json.Unmarshal(row.Doc, &leaf) != nil {
				continue
			}
			byID[row.ID] = leaf.Data
		}
	}

	// Chunk order is the file: walk the children list rather than the response,
	// so a reordering cannot silently scramble the content.
	pieces := make([]string, 0, len(e.Children))
	for i, id := range e.Children {
		data, ok := byID[id]
		if !ok {
			return out, fmt.Errorf("livesync: 청크 %d/%d (%s)를 찾을 수 없습니다", i+1, len(e.Children), id)
		}
		decrypted, err := r.decrypt(data)
		if err != nil {
			return out, fmt.Errorf("livesync: 청크 %d/%d 복호화 실패: %w", i+1, len(e.Children), err)
		}
		pieces = append(pieces, decrypted)
	}
	out.setPieces(pieces)
	return out, nil
}

// setPieces stores the reassembled chunks the way the content's kind needs.
//
// Text is joined here and binary is not, deliberately: joining a large binary
// would double the memory for a string nothing reads, and Bytes has to see the
// chunk boundaries anyway.
func (c *Content) setPieces(pieces []string) {
	if c.Binary {
		c.pieces = pieces
		return
	}
	c.Text = strings.Join(pieces, "")
}

// Bytes is the file as it should land on disk.
//
// For a binary file this is not "join the chunks and decode": the plugin slices
// the file's *bytes* and base64-encodes each slice on its own
// (splitPieces2/splitPieces2V2 in livesync-commonlib, and base64ToArrayBuffer
// on the way back decodes an array element by element). Every chunk is
// therefore a complete base64 document carrying its own padding, and a joined
// string stops being valid base64 at the first chunk boundary - which is why
// only single-chunk files survived the naive reading.
func (c Content) Bytes() ([]byte, error) {
	if !c.Binary {
		return []byte(c.Text), nil
	}
	if len(c.pieces) == 0 {
		return nil, nil
	}

	// The plugin's own UTF-16 codec, marked on the first piece only. Decoding
	// it would be guesswork, and a wrong guess writes a corrupt file rather
	// than failing.
	if strings.HasPrefix(c.pieces[0], "%") {
		return nil, fmt.Errorf("%w: 바이너리 UTF-16 코덱", ErrUnsupportedFormat)
	}

	var out []byte
	for i, piece := range c.pieces {
		if piece == "" {
			continue
		}
		raw, err := decodeBase64(piece)
		if err != nil {
			return nil, fmt.Errorf("livesync: 조각 %d/%d: %w", i+1, len(c.pieces), err)
		}
		out = append(out, raw...)
	}
	return out, nil
}
