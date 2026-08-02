package httpapi

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
)

// webdist holds the built React app. `make build` copies web/dist into it.
//
// Only .gitkeep is committed: the built files are artifacts, but //go:embed
// needs the directory to exist, so a fresh clone still compiles - and serves a
// "UI not built" message until `make build` fills it in.
//
//go:embed all:webdist
var webdist embed.FS

// uiHandler serves the single-page app: real files are served as-is, everything
// else falls back to index.html so client-side routes survive a page reload.
//
// When devProxy is set, requests go to the Vite dev server instead, which is
// what `make dev-server` relies on for hot reload.
func uiHandler(devProxy string) (http.Handler, error) {
	if devProxy != "" {
		target, err := url.Parse(devProxy)
		if err != nil {
			return nil, err
		}
		slog.Warn("serving UI from the Vite dev server", "target", devProxy)
		return httputil.NewSingleHostReverseProxy(target), nil
	}

	sub, err := fs.Sub(webdist, "webdist")
	if err != nil {
		return nil, err
	}
	files := http.FS(sub)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean("/" + r.URL.Path)

		f, err := files.Open(name)
		if err == nil {
			if st, statErr := f.Stat(); statErr == nil && !st.IsDir() {
				// Vite emits content-hashed asset names, so those can be cached
				// hard. index.html must not be, or a redeploy is invisible.
				if strings.HasPrefix(name, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				http.ServeContent(w, r, name, st.ModTime(), f)
				f.Close()
				return
			}
			f.Close()
		}

		index, err := sub.(fs.ReadFileFS).ReadFile("index.html")
		if err != nil {
			http.Error(w, "UI not built - run `make build`", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	}), nil
}
