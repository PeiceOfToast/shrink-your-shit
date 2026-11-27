// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// The web interface: the embedded build served for every path that is not an /api route.

const (
	cacheImmutable = "public, max-age=31536000, immutable" // a hashed asset never changes
	cacheNoCache   = "no-cache"                            // the page shell names the current assets
)

// staticTypes are the types the built interface has. The extension map is explicit so a server's
// mime table cannot change what a browser is told.
var staticTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".gif":         "image/gif",
	".webp":        "image/webp",
	".ico":         "image/x-icon",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".ttf":         "font/ttf",
	".txt":         "text/plain; charset=utf-8",
	".webmanifest": "application/manifest+json",
}

// SetUI gives the server the built web interface. Call it before the server answers requests.
func (s *Server) SetUI(fsys fs.FS) {
	s.ui = fsys
}

// static serves the web interface for a path that is not an /api route: the file when it is there,
// the page shell for a client route (a path without a file extension), and the JSON 404 otherwise.
// A signed-out caller gets the same shell; the pages fetch their own data, and the API stays gated.
func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") || s.ui == nil {
		notFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "That route takes GET.", "", 0)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "index.html" {
		s.serveUI(w, r, "index.html")
		return
	}
	if !fs.ValidPath(name) {
		notFound(w, r)
		return
	}
	if _, err := fs.Stat(s.ui, name); err == nil {
		s.serveUI(w, r, name)
		return
	}
	if path.Ext(name) == "" {
		s.serveUI(w, r, "index.html") // a client route: /jobs/12, /settings/backup
		return
	}
	notFound(w, r)
}

// serveUI writes one file of the interface, with the type its extension names and the cache rule
// the build expects: hashed assets forever, the shell re-checked every load.
func (s *Server) serveUI(w http.ResponseWriter, r *http.Request, name string) {
	data, err := fs.ReadFile(s.ui, name)
	if err != nil {
		notFound(w, r)
		return
	}
	if ctype, ok := staticTypes[strings.ToLower(path.Ext(name))]; ok {
		w.Header().Set("Content-Type", ctype)
	} else {
		w.Header().Set("Content-Type", http.DetectContentType(data))
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", cacheImmutable)
	} else {
		w.Header().Set("Cache-Control", cacheNoCache)
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
