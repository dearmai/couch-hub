package httpapi

import (
	_ "embed"
	"net/http"
)

// installGuide is served to the wizard's first step. Embedding the same file
// that ships as repository documentation keeps the two from drifting apart.
//
//go:embed guide/couchdb-install.md
var installGuide string

func (s *Server) handleSetupGuide(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(installGuide))
}
