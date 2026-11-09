// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// nextApp serves the new single-page app at /next/ (UI rebuild; research ts-build-embed.md), from
// files: Vite's build, or nothing when it wasn't built (then a plain "not built" page).
//   - a file that exists is served (gzip as /assets/ are); Vite's hashed assets/ are cached for a year,
//     everything else must be checked again;
//   - any other path without an extension is a route inside the app: index.html, so a reload works;
//   - a missing file with an extension is a 404, never index.html in place of a script.
//
// /api/ and the live-update stream are routes of their own, so an API 404 is never this page.
func nextApp(files fs.FS) http.HandlerFunc {
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(notBuiltPage))
		}
	}
	serveIndex := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(index)
	}
	fileServer := http.StripPrefix("/next/", http.FileServer(http.FS(files)))
	inner := func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/next/")
		if name == "" || name == "index.html" {
			serveIndex(w)
			return
		}
		if st, err := fs.Stat(files, name); err == nil && !st.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		serveIndex(w)
	}
	zipped := gzipAssets("/next/", files, inner)
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/next/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable") // Vite names them by content
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		zipped(w, r) // a missing file (an app route) and /next/ itself reach inner
	}
}

const notBuiltPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Shrink Your Shit: new interface not built</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 36rem; margin: 4rem auto; padding: 0 1rem; line-height: 1.5">
<h1>The new interface isn't in this copy</h1>
<p>This program was built without the new interface. The current one works as before: <a href="/">open it</a>.</p>
<p>To build the new one, run the web build (<code>npm run build</code> in <code>web/</code>) before <code>go build</code>.</p>
</body></html>
`
