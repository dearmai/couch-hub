// Package export packages a vault's decrypted contents into a zip file.
//
// It runs detached from the request that asks for it. A vault is thousands of
// chunks, every one of which has to be fetched from CouchDB and decrypted, so
// the work routinely outlives any sensible HTTP timeout - and a download that
// starts before the archive is complete cannot be resumed or verified. The
// browser polls a status instead and fetches the finished file afterwards.
//
// A finished archive is the vault in the clear, sitting on disk. That is the
// whole point of an export, and it is also why one is short-lived: it is
// deleted once its lifetime is up, when a newer export replaces it, and when
// the process that owns it restarts.
package export

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/dearmai/couch-hub/internal/livesync"
)

const (
	// workers is how many files are assembled at once. The cost is dominated by
	// round trips to CouchDB rather than by AES, so a handful in flight is the
	// difference between minutes and seconds on a real vault.
	workers = 4

	// readyTTL is how long a finished archive stays on disk, and how long a
	// failed one stays around to be read as an explanation.
	readyTTL = 30 * time.Minute

	// sweepInterval is how often expiries are checked. An archive lingering a
	// minute past its deadline is cheap; a ticker per second is not.
	sweepInterval = time.Minute

	// shownProblems caps the list in the status JSON, which the browser polls
	// once a second. The archive's own report carries every one of them.
	shownProblems = 200

	// keptProblems bounds the full list. A vault whose stored passphrase is
	// wrong fails on every single file, and an unbounded list would grow with
	// the vault; the report says so when it truncates.
	keptProblems = 100_000

	// reportName is the manifest written into every archive.
	reportName = "_couchhub-export.txt"
)

var (
	// ErrRunning marks a second export started while the first is still going.
	ErrRunning = errors.New("이미 내보내는 중입니다")
	// ErrNotFound marks a vault with no export to report on.
	ErrNotFound = errors.New("내보내기 기록이 없습니다")
	// ErrNotReady marks a download asked for before the archive is complete.
	ErrNotReady = errors.New("내보내기가 아직 끝나지 않았습니다")
)

// State is where an export has got to.
type State string

const (
	// StateListing is the walk over the vault's entries, before any file has
	// been fetched. There is no total to show a percentage against yet.
	StateListing State = "listing"
	// StatePacking is the per-file work, which is what the progress bar tracks.
	StatePacking State = "packing"
	StateReady   State = "ready"
	StateFailed  State = "failed"
	// StateCanceled is an export the operator abandoned.
	StateCanceled State = "canceled"
)

// Active reports whether the export is still doing work.
func (s State) Active() bool { return s == StateListing || s == StatePacking }

// Status is one export as the UI sees it.
type Status struct {
	VaultID string `json:"vaultId"`
	State   State  `json:"state"`

	// Total is the number of files to pack, known only from StatePacking on.
	Total int `json:"total"`
	Done  int `json:"done"`
	// Skipped counts files left out of the archive: a tombstone is not one, but
	// a path that would not decrypt is.
	Skipped int `json:"skipped"`
	// Bytes is the content packed so far, before compression.
	Bytes int64 `json:"bytes"`

	// Filename is what the download is called.
	Filename string `json:"filename"`
	// SizeBytes is the finished archive's size on disk.
	SizeBytes int64 `json:"sizeBytes"`

	Error string `json:"error,omitempty"`
	// Problems lists the skipped files. Capped for the browser; the archive's
	// report has the complete list.
	Problems []string `json:"problems,omitempty"`

	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt,omitzero"`
	// ExpiresAt is when the archive is deleted from disk.
	ExpiresAt time.Time `json:"expiresAt,omitzero"`
}

// Job is one export, in flight or finished.
type Job struct {
	// path and cancel are fixed at construction, so they need no lock.
	path   string
	cancel context.CancelFunc

	mu     sync.Mutex
	status Status
	// problems is every skipped file, not the shortened list the browser gets.
	problems []string
}

// Status returns a snapshot safe to hand to a caller, with the problem list
// shortened to what a poll should carry.
func (j *Job) Status() Status {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := j.status
	out.Problems = append([]string(nil), j.problems[:min(len(j.problems), shownProblems)]...)
	return out
}

// problemList returns every problem recorded so far, for the archive's report.
func (j *Job) problemList() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.problems...)
}

func (j *Job) begin(total int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status.State = StatePacking
	j.status.Total = total
}

func (j *Job) wrote(n int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status.Done++
	j.status.Bytes += n
}

func (j *Job) problem(format string, args ...any) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status.Skipped++
	if len(j.problems) < keptProblems {
		j.problems = append(j.problems, fmt.Sprintf(format, args...))
	}
}

// discard stops the job and removes its archive.
//
// A run still in flight holds the file open, so its remaining writes land on an
// inode nothing can reach and are freed when it closes.
func (j *Job) discard() {
	j.cancel()
	_ = os.Remove(j.path)
}

// Manager owns the exports and the directory they are staged in. One export per
// vault at a time.
type Manager struct {
	dir string

	mu   sync.Mutex
	jobs map[string]*Job
}

// NewManager prepares the staging directory, emptying it first.
//
// Anything already in it was written by a process that is no longer running,
// which makes it both stale and the last thing that should quietly survive a
// restart: it is a vault in plaintext.
func NewManager(dir string) (*Manager, error) {
	if err := os.RemoveAll(dir); err != nil {
		return nil, fmt.Errorf("export: clear %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("export: create %s: %w", dir, err)
	}
	return &Manager{dir: dir, jobs: make(map[string]*Job)}, nil
}

// Start begins an export, replacing whatever finished one is there.
//
// reader must be usable after the calling request ends; it is read from for as
// long as the export runs.
func (m *Manager) Start(vaultID, filename string, reader *livesync.Reader) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.jobs[vaultID]; ok {
		if existing.Status().State.Active() {
			return Status{}, ErrRunning
		}
		existing.discard()
		delete(m.jobs, vaultID)
	}

	f, err := os.CreateTemp(m.dir, "vault-*.zip")
	if err != nil {
		return Status{}, fmt.Errorf("export: create archive: %w", err)
	}

	// Background rather than the request's context: the export is the point of
	// the request, not part of it, and the browser hangs up immediately.
	ctx, cancel := context.WithCancel(context.Background())
	j := &Job{
		path:   f.Name(),
		cancel: cancel,
		status: Status{
			VaultID:   vaultID,
			State:     StateListing,
			Filename:  filename,
			StartedAt: time.Now().UTC(),
		},
	}
	m.jobs[vaultID] = j

	go j.run(ctx, reader, f)
	return j.Status(), nil
}

// Status reports one vault's export.
func (m *Manager) Status(vaultID string) (Status, error) {
	m.mu.Lock()
	j, ok := m.jobs[vaultID]
	m.mu.Unlock()
	if !ok {
		return Status{}, ErrNotFound
	}
	return j.Status(), nil
}

// Open returns the finished archive. The caller closes it.
func (m *Manager) Open(vaultID string) (*os.File, Status, error) {
	m.mu.Lock()
	j, ok := m.jobs[vaultID]
	m.mu.Unlock()
	if !ok {
		return nil, Status{}, ErrNotFound
	}

	status := j.Status()
	if status.State != StateReady {
		return nil, status, ErrNotReady
	}
	f, err := os.Open(j.path)
	if err != nil {
		return nil, status, fmt.Errorf("export: open archive: %w", err)
	}
	return f, status, nil
}

// Discard cancels an export and deletes its archive. A vault with none is not
// an error: this is also how a deleted vault is cleaned up.
func (m *Manager) Discard(vaultID string) {
	m.mu.Lock()
	j, ok := m.jobs[vaultID]
	delete(m.jobs, vaultID)
	m.mu.Unlock()

	if ok {
		j.discard()
	}
}

// Run deletes archives once their lifetime is up, and everything on shutdown.
// Blocking - meant to be run in a goroutine.
func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.discardAll()
			return
		case <-ticker.C:
			m.sweep(time.Now().UTC())
		}
	}
}

func (m *Manager) sweep(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, j := range m.jobs {
		status := j.Status()
		if status.State.Active() {
			continue
		}
		// A failed or cancelled job has no archive; it is kept only long enough
		// for the browser to read why, then it is just noise in the list.
		deadline := status.ExpiresAt
		if deadline.IsZero() {
			deadline = status.FinishedAt.Add(readyTTL)
		}
		if now.After(deadline) {
			j.discard()
			delete(m.jobs, id)
		}
	}
}

func (m *Manager) discardAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, j := range m.jobs {
		j.discard()
		delete(m.jobs, id)
	}
}

// run packs the archive and records how it ended.
func (j *Job) run(ctx context.Context, r *livesync.Reader, f *os.File) {
	err := j.pack(ctx, r, f)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	j.status.FinishedAt = time.Now().UTC()

	switch {
	case errors.Is(err, context.Canceled):
		j.status.State = StateCanceled
	case err != nil:
		j.status.State = StateFailed
		j.status.Error = err.Error()
	default:
		j.status.State = StateReady
		j.status.ExpiresAt = j.status.FinishedAt.Add(readyTTL)
		if info, statErr := os.Stat(j.path); statErr == nil {
			j.status.SizeBytes = info.Size()
		}
	}

	// Only a complete archive is worth keeping: a truncated zip is not a
	// partial backup, it is a file that fails to open.
	if j.status.State != StateReady {
		_ = os.Remove(j.path)
	}
}

func (j *Job) pack(ctx context.Context, r *livesync.Reader, f *os.File) error {
	// A write failure has to stop the fetchers too, not just the writer.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	entries, err := r.ListAll(ctx)
	if err != nil {
		return err
	}

	files := make([]livesync.Entry, 0, len(entries))
	for _, e := range entries {
		switch {
		case e.Deleted:
			// A tombstone is a file the vault no longer has. livesync keeps it
			// so other clients learn of the deletion; a backup should not
			// resurrect it.
		case e.PathError != "":
			j.problem("%s: 경로를 복호화할 수 없습니다 (%s)", e.ID, e.PathError)
		case archivePath(e.Path) == "":
			j.problem("%s: 압축할 수 없는 경로입니다 (%q)", e.ID, e.Path)
		default:
			files = append(files, e)
		}
	}
	j.begin(len(files))

	type result struct {
		entry   livesync.Entry
		content livesync.Content
		err     error
	}

	in := make(chan livesync.Entry)
	out := make(chan result, workers)

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range in {
				content, err := r.Fetch(ctx, e)
				select {
				case out <- result{entry: e, content: content, err: err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		defer close(in)
		for _, e := range files {
			select {
			case in <- e:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(out)
	}()

	zw := zip.NewWriter(f)
	taken := make(map[string]int, len(files))
	var writeErr error

	for res := range out {
		if writeErr != nil {
			// Keep draining: a worker blocked on send would never see the
			// cancel and this loop would never end.
			continue
		}
		if res.err != nil {
			j.problem("%s: %v", label(res.entry), res.err)
			continue
		}
		body, err := res.content.Bytes()
		if err != nil {
			j.problem("%s: 내용을 디코딩할 수 없습니다 (%v)", label(res.entry), err)
			continue
		}

		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:     claimPath(taken, archivePath(res.entry.Path)),
			Method:   zip.Deflate,
			Modified: modified(res.entry.Mtime),
		})
		if err == nil {
			_, err = w.Write(body)
		}
		if err != nil {
			writeErr = fmt.Errorf("export: %s 쓰기 실패: %w", label(res.entry), err)
			cancel()
			continue
		}
		j.wrote(int64(len(body)))
	}

	if writeErr != nil {
		return writeErr
	}
	// Before the manifest, so a cancelled export is never rounded up into a
	// complete-looking archive.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeReport(zw, j.Status(), j.problemList()); err != nil {
		return err
	}
	return zw.Close()
}

// writeReport records what did and did not make it in.
//
// An archive that is quietly missing files is worse than one that says so:
// nothing else in the download distinguishes "this vault has 400 notes" from
// "600 notes, 200 of which would not decrypt".
//
// problems is the complete list, not the one the browser polls. Truncating
// here would defeat the point - this file is what an operator reads to work
// out which files to go and rescue by hand.
func writeReport(zw *zip.Writer, status Status, problems []string) error {
	now := time.Now().UTC()
	// CreateHeader rather than Create: a zero Modified is written as the
	// 1980-00-00 that MS-DOS dates cannot represent, and file browsers render
	// it as a corrupt entry.
	w, err := zw.CreateHeader(&zip.FileHeader{Name: reportName, Method: zip.Deflate, Modified: now})
	if err != nil {
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "CouchHub vault export\n")
	fmt.Fprintf(&b, "생성: %s\n", now.Format(time.RFC3339))
	fmt.Fprintf(&b, "파일: %d개 (%d바이트)\n", status.Done, status.Bytes)
	fmt.Fprintf(&b, "제외: %d개\n", status.Skipped)
	if len(problems) > 0 {
		b.WriteString("\n제외된 항목:\n")
		for _, p := range problems {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
		if status.Skipped > len(problems) {
			fmt.Fprintf(&b, "\n제외 항목이 %d개를 넘어 목록을 %d개에서 끊었습니다 (총 %d개).\n",
				keptProblems, len(problems), status.Skipped)
		}
	}

	_, err = w.Write([]byte(b.String()))
	return err
}

// archivePath turns a vault path into a zip entry name, or "" if it cannot be
// trusted.
//
// The traversal check is the important half. A vault is client-supplied data,
// and an archive carrying "../../.ssh/authorized_keys" writes outside whatever
// directory it is unpacked into on any extractor that honours the name.
func archivePath(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return ""
	}

	parts := strings.Split(p, "/")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".", "..":
			return ""
		}
		clean = append(clean, part)
	}
	return strings.Join(clean, "/")
}

// claimPath keeps two entries claiming one path from becoming one file.
//
// It happens: an obfuscated id and a plain one can decrypt to the same path
// after a vault has been through a rename, and zip has no opinion about
// duplicates - the extractor simply overwrites, losing a file silently.
func claimPath(taken map[string]int, name string) string {
	n := taken[name]
	taken[name] = n + 1
	if n == 0 {
		return name
	}

	ext := path.Ext(name)
	return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), n+1, ext)
}

// label names an entry in a message, falling back to its id when the path is
// what failed.
func label(e livesync.Entry) string {
	if e.Path != "" {
		return e.Path
	}
	return e.ID
}

// modified converts livesync's millisecond timestamp. A zero or nonsensical one
// becomes the export time rather than 1970, which sorts oddly in every file
// browser.
func modified(mtime int64) time.Time {
	if mtime <= 0 {
		return time.Now().UTC()
	}
	return time.UnixMilli(mtime).UTC()
}
