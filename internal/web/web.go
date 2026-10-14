// Package web serves the console, a single-page app built by Vite into dist/ and embedded in the binary.
package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// dist holds the Vite build output. `all:` keeps .gitkeep, so the package compiles before the UI is built.
//
//go:embed all:dist
var dist embed.FS

const (
	cacheImmutable = "public, max-age=31536000, immutable"
	cacheFile      = "public, max-age=3600"
	cacheIndex     = "no-cache"
	notBuiltBody   = "web UI is not built: run `bun run build` in web/\n"
)

// Handler serves the embedded console.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// fs.Sub fails only on an invalid directory name; "dist" is valid.
		panic(err)
	}
	return New(sub)
}

// New serves the single-page app in fsys, which is rooted at the build output directory. Assets under /assets/
// are cached for a year, other files for an hour, and every other extension-less path falls back to index.html,
// which is always revalidated. Paths under /api/ and /v1/ never fall back.
func New(fsys fs.FS) http.Handler {
	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			http.Error(w, notBuiltBody, http.StatusServiceUnavailable)
		})
	}
	sum := sha256.Sum256(index)
	return &handler{fsys: fsys, index: index, etag: `"` + hex.EncodeToString(sum[:16]) + `"`}
}

type handler struct {
	fsys  fs.FS
	index []byte
	etag  string // lets a no-cache revalidation end in 304
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	p := path.Clean("/" + r.URL.Path)
	if strings.HasPrefix(p, "/api/") || p == "/api" || strings.HasPrefix(p, "/v1/") || p == "/v1" {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(p, "/")
	if name != "" && name != "index.html" && h.isFile(name) {
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", cacheImmutable)
		} else {
			w.Header().Set("Cache-Control", cacheFile)
		}
		http.ServeFileFS(w, r, h.fsys, name) //nolint:gosec // G703: name is cleaned and fsys is an fs.FS, which rejects "..".
		return
	}
	if strings.Contains(path.Base(p), ".") && name != "index.html" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", cacheIndex)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("ETag", h.etag)
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(h.index))
}

// isFile reports whether name is a regular file in the build output; directories are never listed.
func (h *handler) isFile(name string) bool {
	fi, err := fs.Stat(h.fsys, name)
	return err == nil && fi.Mode().IsRegular()
}
