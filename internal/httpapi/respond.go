package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/dearmai/couch-hub/internal/store"
)

// apiError is the single error shape the UI has to handle.
type apiError struct {
	Error string `json:"error"`
	// Code lets the UI branch without string-matching the message.
	Code string `json:"code,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already out, so all we can do is record it.
		slog.Error("write response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, code string, err error) {
	writeJSON(w, status, apiError{Error: err.Error(), Code: code})
}

// fail maps an internal error to a status code. Anything unrecognised becomes a
// 500 with its message passed through: CouchHub is a single-operator tool, so a
// useful error beats hiding detail from an attacker who is already the admin.
func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err)
	default:
		slog.Error("request failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", err)
	}
}

// decodeJSON reads a request body, rejecting unknown fields so a typo in the UI
// surfaces immediately instead of being silently ignored.
func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
