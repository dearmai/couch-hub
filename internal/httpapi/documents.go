package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dearmai/couch-hub/internal/couch"
	"github.com/dearmai/couch-hub/internal/livesync"
	"github.com/dearmai/couch-hub/internal/store"
)

// readerFor builds a document reader for a vault, unsealing its passphrase.
//
// dynamicIterations only matters for vaults still on the legacy V2 chunk format;
// CouchHub's own vaults never use it, so it is an opt-in query parameter rather
// than stored state.
func (s *Server) readerFor(ctx context.Context, v store.Vault, dynamicIterations bool) (*livesync.Reader, error) {
	profile, err := s.store.Profile(v.ProfileID)
	if err != nil {
		return nil, err
	}
	client, err := s.clientFor(profile)
	if err != nil {
		return nil, err
	}

	passphrase := ""
	if !v.E2EEDisabled {
		if !v.SecretsPersisted {
			return nil, errors.New("이 Vault는 자격증명이 저장되지 않아 내용을 읽을 수 없습니다")
		}
		passphrase, err = s.sealer.OpenString(v.E2EEPassphraseSealed)
		if err != nil {
			return nil, err
		}
	}

	return livesync.NewReader(ctx, client, v.DBName, passphrase, dynamicIterations)
}

// documentsDisabled refuses the request when the browser is switched off.
//
// Gating the UI alone would be theatre: the endpoints decrypt vault contents, so
// they are what actually has to be closed.
func (s *Server) documentsDisabled(w http.ResponseWriter) bool {
	if s.cfg.DocumentsEnabled {
		return false
	}
	writeError(w, http.StatusForbidden, "documents_disabled",
		errors.New("문서 열람이 비활성화되어 있습니다 (COUCHHUB_DOCUMENTS=false)"))
	return true
}

func (s *Server) handleListDocuments(w http.ResponseWriter, r *http.Request) {
	if s.documentsDisabled(w) {
		return
	}
	v, err := s.store.Vault(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	reader, err := s.readerFor(ctx, v, r.URL.Query().Get("dynamicIterations") == "true")
	if err != nil {
		writeError(w, http.StatusBadRequest, "reader_unavailable", err)
		return
	}

	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 && n <= 2000 {
			limit = n
		}
	}

	docs, err := reader.List(ctx, limit)
	if err != nil {
		writeConnectError(w, err)
		return
	}
	// Never cached: this is vault content, decrypted server-side.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, docs)
}

func (s *Server) handleGetDocument(w http.ResponseWriter, r *http.Request) {
	if s.documentsDisabled(w) {
		return
	}
	v, err := s.store.Vault(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	reader, err := s.readerFor(ctx, v, r.URL.Query().Get("dynamicIterations") == "true")
	if err != nil {
		writeError(w, http.StatusBadRequest, "reader_unavailable", err)
		return
	}

	docID := r.URL.Query().Get("docId")
	if docID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", errors.New("docId 파라미터가 필요합니다"))
		return
	}

	content, err := reader.Get(ctx, docID)
	switch {
	case err == nil:
	case couch.IsNotFound(err):
		writeError(w, http.StatusNotFound, "not_found",
			fmt.Errorf("문서 %q를 찾을 수 없습니다", docID))
		return
	case errors.Is(err, livesync.ErrWrongPassphrase):
		writeError(w, http.StatusUnprocessableEntity, "wrong_passphrase",
			errors.New("저장된 패스프레이즈로 복호화할 수 없습니다. 이 Vault가 다른 패스프레이즈로 암호화되어 있을 수 있습니다"))
		return
	case errors.Is(err, livesync.ErrUnsupportedFormat):
		writeError(w, http.StatusUnprocessableEntity, "unsupported_format", err)
		return
	case errors.Is(err, livesync.ErrSaltRequired):
		writeError(w, http.StatusUnprocessableEntity, "salt_required", err)
		return
	default:
		writeConnectError(w, err)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, content)
}
