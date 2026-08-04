package livesync

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/dearmai/couch-hub/internal/couch"
)

// Entry types the plugin writes. From livesync-commonlib common/models/db.const.
const (
	TypeNoteLegacy   = "notes"
	TypeNoteBinary   = "newnote"
	TypeNotePlain    = "plain"
	TypeInternalFile = "internalfile"
	TypeChunk        = "leaf"
)

// docIDSyncParameters holds the PBKDF2 salt shared by every %= blob in a vault.
// From DOCID_SYNC_PARAMETERS in livesync-commonlib.
const docIDSyncParameters = "_local/obsidian_livesync_sync_parameters"

// Document is one note as CouchHub lists it.
type Document struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Type string `json:"type"`
	// Chunks is how many pieces the content is split into.
	Chunks int   `json:"chunks"`
	Size   int64 `json:"size"`
	// Mtime is the plugin's own modification time, in milliseconds.
	Mtime int64 `json:"mtime"`
	// Deleted marks a tombstone the plugin keeps rather than removing.
	Deleted bool `json:"deleted"`
	// PathError explains a path that could not be decrypted, so the UI can show
	// the document exists without pretending to know its name.
	PathError string `json:"pathError,omitempty"`
}

// Content is a note's reassembled body.
type Content struct {
	Document
	// Text is the file content. Empty when Binary is true.
	Text string `json:"text"`
	// Binary marks content that is not text and therefore not rendered.
	Binary bool `json:"binary"`

	// pieces are the decrypted chunks, in order, kept unjoined for a binary
	// file because each one is separately base64-encoded. See Bytes.
	pieces []string
}

// Reader reads one vault's documents.
type Reader struct {
	client *couch.Client
	dbName string

	passphrase string
	opts       DecryptOptions
	// encrypted is false for a vault storing plaintext, where every decrypt step
	// is skipped rather than attempted and reported as a failure.
	encrypted bool
}

// NewReader prepares a reader. passphrase may be empty for an unencrypted vault.
//
// It fetches the vault's sync-parameters document so %= chunks can be decrypted;
// a vault that has none simply never uses that format.
func NewReader(ctx context.Context, client *couch.Client, dbName, passphrase string, dynamicIterations bool) (*Reader, error) {
	r := &Reader{
		client:     client,
		dbName:     dbName,
		passphrase: passphrase,
		encrypted:  passphrase != "",
		// One cache per reader: a listing decrypts every entry's metadata with the
		// same salt, and re-deriving the key each time dominates everything else.
		opts: DecryptOptions{DynamicIterations: dynamicIterations, Keys: NewKeyCache()},
	}

	var params struct {
		PBKDF2Salt string `json:"pbkdf2salt"`
	}
	err := client.GetDoc(ctx, dbName, docIDSyncParameters, &params)
	switch {
	case err == nil && params.PBKDF2Salt != "":
		salt, decodeErr := decodeBase64(params.PBKDF2Salt)
		if decodeErr != nil {
			return nil, fmt.Errorf("livesync: sync-parameters salt: %w", decodeErr)
		}
		r.opts.PBKDF2Salt = salt
	case err == nil, couch.IsNotFound(err):
		// No salt: the vault predates that format or never used it.
	default:
		return nil, err
	}

	return r, nil
}

// encryptedMeta is what a `/\:`-prefixed path field decrypts to.
//
// With metadata encryption on, the entry's plaintext fields are placeholders:
// the real path, timestamps, size and - critically - the chunk list all live
// inside this bundle. Reading only the outer document yields a nameless,
// zero-byte, chunk-less entry, which is exactly what it looks like when this
// step is missing.
type encryptedMeta struct {
	Path     string   `json:"path"`
	Mtime    float64  `json:"mtime"`
	Ctime    float64  `json:"ctime"`
	Size     float64  `json:"size"`
	Children []string `json:"children"`
}

// rawEntry is the subset of a note entry this package reads.
type rawEntry struct {
	ID       string   `json:"_id"`
	Path     string   `json:"path"`
	Type     string   `json:"type"`
	Children []string `json:"children"`
	Data     any      `json:"data"`
	Size     int64    `json:"size"`
	Mtime    int64    `json:"mtime"`
	Deleted  bool     `json:"deleted"`
	Type2    string   `json:"datatype"`
}

// List returns the vault's notes, newest first.
//
// Chunks, design documents and the plugin's own bookkeeping are filtered out:
// they outnumber the notes by orders of magnitude and none of them is a file.
func (r *Reader) List(ctx context.Context, limit int) ([]Document, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}

	rows, err := r.client.AllDocsWithDocs(ctx, r.dbName, limit*4)
	if err != nil {
		return nil, err
	}

	out := make([]Document, 0, limit)
	for _, row := range rows {
		if strings.HasPrefix(row.ID, "_design/") || strings.HasPrefix(row.ID, "_local/") {
			continue
		}
		var e rawEntry
		if err := json.Unmarshal(row.Doc, &e); err != nil {
			continue
		}
		switch e.Type {
		case TypeNotePlain, TypeNoteBinary, TypeNoteLegacy, TypeInternalFile:
		default:
			// Chunks and metadata.
			continue
		}

		doc, _ := r.resolve(row.ID, e)
		out = append(out, doc)

		if len(out) >= limit {
			break
		}
	}

	// Most recently modified first: that is the order someone browsing a vault
	// actually wants.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Mtime > out[j].Mtime })
	return out, nil
}

// Get reassembles one note's content from its chunks.
func (r *Reader) Get(ctx context.Context, docID string) (Content, error) {
	var e rawEntry
	if err := r.client.GetDoc(ctx, r.dbName, docID, &e); err != nil {
		return Content{}, err
	}

	doc, children := r.resolve(docID, e)
	return r.Fetch(ctx, Entry{Document: doc, Children: children, Data: e.Data, Binary: isBinary(e)})
}

// decrypt passes plaintext through untouched: a vault with encryption off
// stores its chunks verbatim, and so does an unencrypted field in an otherwise
// encrypted vault.
func (r *Reader) decrypt(s string) (string, error) {
	if !r.encrypted || !IsEncrypted(s) {
		return s, nil
	}
	return DecryptString(s, r.passphrase, r.opts)
}

// resolve turns a raw entry into the document CouchHub shows, unwrapping the
// encrypted metadata bundle when there is one.
//
// A path that cannot be decrypted is not fatal for the listing: the document
// still exists, and saying so with the reason beats hiding it.
func (r *Reader) resolve(id string, e rawEntry) (Document, []string) {
	doc := Document{
		ID:      id,
		Type:    e.Type,
		Chunks:  len(e.Children),
		Size:    e.Size,
		Mtime:   e.Mtime,
		Deleted: e.Deleted,
	}
	children := e.Children

	switch {
	case strings.HasPrefix(e.Path, PrefixEncryptedMeta):
		plain, err := r.decrypt(strings.TrimPrefix(e.Path, PrefixEncryptedMeta))
		if err != nil {
			doc.PathError = err.Error()
			return doc, children
		}
		var meta encryptedMeta
		if err := json.Unmarshal([]byte(plain), &meta); err != nil {
			doc.PathError = fmt.Sprintf("메타데이터를 해석할 수 없습니다: %v", err)
			return doc, children
		}
		doc.Path = meta.Path
		doc.Mtime = int64(meta.Mtime)
		doc.Size = int64(meta.Size)
		if len(meta.Children) > 0 {
			children = meta.Children
			doc.Chunks = len(children)
		}

	case e.Path != "":
		decoded, err := r.decrypt(e.Path)
		if err != nil {
			doc.PathError = err.Error()
		} else {
			doc.Path = decoded
		}
	}

	return doc, children
}

// inlinePieces normalises the legacy `data` field, which is a string in some
// versions and an array of strings in others.
//
// The array is returned as it is rather than joined: for a binary entry each
// element is its own base64 document, so joining is only correct once the
// caller knows the content is text.
func inlinePieces(v any) ([]string, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case string:
		return []string{t}, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, part := range t {
			s, ok := part.(string)
			if !ok {
				return nil, fmt.Errorf("livesync: unexpected data element %T", part)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("livesync: unexpected data field %T", v)
	}
}
