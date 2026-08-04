package export

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dearmai/couch-hub/internal/couch"
	"github.com/dearmai/couch-hub/internal/livesync"
)

// fakeCouch serves the three endpoints a vault export reads: the local
// sync-parameters document, the id listing, and the bulk fetch.
//
// An unencrypted vault, deliberately. The crypto has its own coverage against
// the published library (scripts/verify-livesync-crypto.mjs); what is under
// test here is the walk, the reassembly and the archive.
func fakeCouch(t *testing.T, docs map[string]map[string]any) *couch.Client {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.Contains(r.URL.Path, "_local/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not_found"}`))

		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/_all_docs"):
			var body struct {
				Keys []string `json:"keys"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode bulk request: %v", err)
			}
			rows := make([]map[string]any, 0, len(body.Keys))
			for _, k := range body.Keys {
				doc, ok := docs[k]
				if !ok {
					continue
				}
				rows = append(rows, map[string]any{"id": k, "key": k, "doc": doc})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"rows": rows})

		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/_all_docs"):
			// Every id in one page: the walk stops as soon as a page comes back
			// shorter than it asked for.
			rows := make([]map[string]any, 0, len(docs))
			for id := range docs {
				rows = append(rows, map[string]any{"id": id, "key": id})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"rows": rows})

		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	client, err := couch.New(srv.URL, "admin", "pw")
	if err != nil {
		t.Fatalf("couch.New: %v", err)
	}
	return client
}

func chunk(data string) map[string]any {
	return map[string]any{"_id": "", "type": "leaf", "data": data}
}

// binaryPieces encodes bytes the way the plugin does: the *bytes* are sliced
// first and each slice is base64-encoded on its own, so every chunk carries its
// own padding.
func binaryPieces(data []byte, sliceLen int) []string {
	var out []string
	for start := 0; start < len(data); start += sliceLen {
		end := min(start+sliceLen, len(data))
		out = append(out, base64.StdEncoding.EncodeToString(data[start:end]))
	}
	return out
}

func TestPackWritesTheVault(t *testing.T) {
	binary := base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G'})

	docs := map[string]map[string]any{
		"notes/a.md": {
			"type": "plain", "path": "notes/a.md",
			"children": []string{"h:a1", "h:a2"},
			"mtime":    1700000000000,
		},
		"img.png": {
			"type": "newnote", "datatype": "newnote", "path": "img.png",
			"children": []string{"h:b1"},
			"mtime":    1700000001000,
		},
		// A tombstone: the vault no longer has this file, so the archive must
		// not resurrect it.
		"gone.md": {
			"type": "plain", "path": "gone.md",
			"children": []string{"h:a1"},
			"deleted":  true,
		},
		// A path that would write outside whatever directory the zip is
		// unpacked into.
		"evil": {
			"type": "plain", "path": "../../escape.md",
			"children": []string{"h:a1"},
		},
		// Not a file: the plugin's own bookkeeping.
		"milestone":   {"type": "milestoneinfo"},
		"h:a1":        chunk("Hello, "),
		"h:a2":        chunk("world!"),
		"h:b1":        chunk(binary),
		"_design/idx": {"views": map[string]any{}},
	}

	// A binary file big enough to be split. Slice length 5 is not a multiple of
	// 3, so every piece but the last ends in base64 padding - which is exactly
	// what makes joining the chunks and decoding once fail.
	big := make([]byte, 64)
	for i := range big {
		big[i] = byte(i * 7)
	}
	pieces := binaryPieces(big, 5)
	bigChildren := make([]string, 0, len(pieces))
	for i, p := range pieces {
		id := fmt.Sprintf("h:big%d", i)
		bigChildren = append(bigChildren, id)
		docs[id] = chunk(p)
	}
	docs["big.bin"] = map[string]any{
		"type": "newnote", "datatype": "newnote", "path": "big.bin",
		"children": bigChildren, "mtime": 1700000002000,
	}

	reader, err := livesync.NewReader(context.Background(), fakeCouch(t, docs), "vault", "", false)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	m, err := NewManager(t.TempDir() + "/exports")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if _, err := m.Start("v1", "vault.zip", reader); err != nil {
		t.Fatalf("Start: %v", err)
	}

	status := waitFor(t, m, "v1")
	if status.State != StateReady {
		t.Fatalf("state = %s, error = %q", status.State, status.Error)
	}
	if status.Done != 3 {
		t.Errorf("packed %d files, want 3 (%v)", status.Done, status.Problems)
	}
	// The traversal path is skipped and said so; the tombstone and the
	// bookkeeping document are simply not files.
	if status.Skipped != 1 {
		t.Errorf("skipped %d, want 1 (%v)", status.Skipped, status.Problems)
	}

	f, _, err := m.Open("v1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}

	got := map[string]string{}
	for _, entry := range zr.File {
		rc, err := entry.Open()
		if err != nil {
			t.Fatalf("open %s: %v", entry.Name, err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name, err)
		}
		got[entry.Name] = string(body)
	}

	if want := "Hello, world!"; got["notes/a.md"] != want {
		t.Errorf("notes/a.md = %q, want %q", got["notes/a.md"], want)
	}
	// A binary file is base64 in the chunks and bytes in the archive.
	if want := string([]byte{0x89, 'P', 'N', 'G'}); got["img.png"] != want {
		t.Errorf("img.png = %q, want the decoded bytes", got["img.png"])
	}
	// The regression that matters: every chunk of a split binary is its own
	// base64 document, so they are decoded one by one and the bytes joined.
	// Joining first and decoding once fails here, and did.
	if got["big.bin"] != string(big) {
		t.Errorf("big.bin is %d bytes, want %d", len(got["big.bin"]), len(big))
	}
	for _, unwanted := range []string{"gone.md", "../../escape.md", "escape.md"} {
		if _, ok := got[unwanted]; ok {
			t.Errorf("archive contains %q", unwanted)
		}
	}
	if report, ok := got[reportName]; !ok {
		t.Error("archive has no manifest")
	} else if !strings.Contains(report, "escape.md") {
		t.Errorf("manifest does not mention the skipped file:\n%s", report)
	}
}

func TestStartRefusesASecondExport(t *testing.T) {
	// A chunk fetch that never answers keeps the first export in flight for the
	// length of the test.
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "_local/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		<-blocked
	}))
	t.Cleanup(srv.Close)

	client, err := couch.New(srv.URL, "admin", "pw")
	if err != nil {
		t.Fatalf("couch.New: %v", err)
	}
	reader, err := livesync.NewReader(context.Background(), client, "vault", "", false)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	m, err := NewManager(t.TempDir() + "/exports")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if _, err := m.Start("v1", "vault.zip", reader); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if _, err := m.Start("v1", "vault.zip", reader); err != ErrRunning {
		t.Errorf("second Start = %v, want ErrRunning", err)
	}

	// Discarding cancels it, so nothing is left running or on disk.
	m.Discard("v1")
	if _, err := m.Status("v1"); err != ErrNotFound {
		t.Errorf("Status after Discard = %v, want ErrNotFound", err)
	}
}

func waitFor(t *testing.T, m *Manager, vaultID string) Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		status, err := m.Status(vaultID)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if !status.State.Active() {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("export stuck in %s", status.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
