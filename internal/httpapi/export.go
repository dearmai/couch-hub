package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dearmai/couch-hub/internal/export"
	"github.com/dearmai/couch-hub/internal/store"
)

// exportDisabled refuses an export when the document browser is switched off.
//
// It is the same capability under a different shape: packing a vault means
// decrypting every file in it server-side, at once, and writing the result to
// disk. A deployment that has said no to reading one note has not said yes to
// that.
func (s *Server) exportDisabled(w http.ResponseWriter) bool {
	if s.cfg.DocumentsEnabled {
		return false
	}
	writeError(w, http.StatusForbidden, "documents_disabled",
		errors.New("내보내기가 비활성화되어 있습니다 (COUCHHUB_DOCUMENTS=false)"))
	return true
}

// handleStartExport begins packing a vault and answers immediately.
//
// Only the reader is built here, which is the part that can fail in a way worth
// reporting as a bad request: a vault with no stored passphrase cannot be
// exported at all, and finding that out from a job status thirty seconds later
// is worse than finding it out now.
func (s *Server) handleStartExport(w http.ResponseWriter, r *http.Request) {
	if s.exportDisabled(w) {
		return
	}
	v, err := s.store.Vault(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	reader, err := s.readerFor(ctx, v, r.URL.Query().Get("dynamicIterations") == "true")
	if err != nil {
		writeError(w, http.StatusBadRequest, "reader_unavailable", err)
		return
	}

	status, err := s.exports.Start(v.ID, exportFilename(v, time.Now()), reader)
	switch {
	case errors.Is(err, export.ErrRunning):
		writeError(w, http.StatusConflict, "export_in_progress", err)
		return
	case err != nil:
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, status)
}

// handleExportStatus reports progress. 404 means no export exists, which is
// what the UI polls for.
func (s *Server) handleExportStatus(w http.ResponseWriter, r *http.Request) {
	if s.exportDisabled(w) {
		return
	}
	status, err := s.exports.Status(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, status)
}

// handleDownloadExport streams the finished archive.
func (s *Server) handleDownloadExport(w http.ResponseWriter, r *http.Request) {
	if s.exportDisabled(w) {
		return
	}
	f, status, err := s.exports.Open(chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, export.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err)
		return
	case errors.Is(err, export.ErrNotReady):
		writeError(w, http.StatusConflict, "export_not_ready", err)
		return
	case err != nil:
		fail(w, err)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/zip")
	// The archive is the vault in plaintext; it has no business in a proxy or a
	// browser cache.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", contentDisposition(status.Filename))
	// ServeContent rather than io.Copy: it sets the length and handles a Range
	// request, so a download interrupted at 900 MB can resume.
	http.ServeContent(w, r, status.Filename, status.FinishedAt, f)
}

// handleDiscardExport cancels an export in flight, or deletes a finished one.
func (s *Server) handleDiscardExport(w http.ResponseWriter, r *http.Request) {
	s.exports.Discard(chi.URLParam(r, "id"))
	w.WriteHeader(http.StatusNoContent)
}

// exportFilename names the download after the vault and the moment it was
// taken, so two exports of the same vault do not land on top of each other in
// a downloads folder.
func exportFilename(v store.Vault, now time.Time) string {
	name := strings.TrimSpace(v.Name)
	if name == "" {
		name = v.DBName
	}
	// Only the characters that break a filename on some filesystem; the rest of
	// the name - which is routinely Korean here - is left alone.
	name = strings.Map(func(r rune) rune {
		switch {
		case r < 32:
			return -1
		case strings.ContainsRune(`/\:*?"<>|`, r):
			return '-'
		default:
			return r
		}
	}, name)

	return fmt.Sprintf("%s-%s.zip", name, now.Format("20060102-150405"))
}

// contentDisposition names the download twice: an ASCII form every browser
// understands, and the real name in the RFC 5987 form for the ones that do.
//
// A bare filename= with a Korean vault name in it is not something the header
// grammar allows, and browsers that guess at it guess differently.
func contentDisposition(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", ascii, url.PathEscape(name))
}
