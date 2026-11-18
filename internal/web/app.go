// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// appHandler serves the single-page app, the whole interface, at / (one UI, 2026-11-12; research ts-build-embed.md),
// from files: Vite's build, or nothing when it wasn't built (then a plain "not built" page). Only GET and HEAD.
//   - a file that exists is served (gzip as /assets/ are); Vite's hashed assets/ are cached for a year,
//     everything else must be checked again;
//   - any other path without an extension is a route inside the app: index.html, so a reload works;
//   - a missing file with an extension is a 404, never index.html in place of a script.
//
// /api/ and the live-update stream are routes of their own, so an API 404 is never this page.
func appHandler(files fs.FS) http.HandlerFunc {
	h := appFiles(files)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

func appFiles(files fs.FS) http.HandlerFunc {
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
	fileServer := http.StripPrefix("/", http.FileServer(http.FS(files)))
	inner := func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
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
	zipped := gzipAssets("/", files, inner)
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if st, err := fs.Stat(files, name); strings.HasPrefix(name, "assets/") && err == nil && !st.IsDir() {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable") // Vite names them by content
		} else { // the app page, a 404 or a folder: checked again each time (review 31668a2)
			w.Header().Set("Cache-Control", "no-cache")
		}
		zipped(w, r) // a missing file (an app route) and / itself reach inner
	}
}

const notBuiltPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Shrink Your Shit: interface not built</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 36rem; margin: 4rem auto; padding: 0 1rem; line-height: 1.5">
<h1>The interface isn't in this copy</h1>
<p>This program was built without its web interface. Its API works as before.</p>
<p>To build it, run the web build (<code>npm run build</code> in <code>web/</code>) before <code>go build</code>.</p>
</body></html>
`

// oldPagesOr answers an old page's exact address (with its trailing slash) with 301 to its place in the app, and
// everything else with the app. (Router patterns for them would loop: see web.go.)
func oldPagesOr(app http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if to, ok := oldPages[r.URL.Path]; ok && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			http.Redirect(w, r, withQuery(to(r), r), http.StatusMovedPermanently)
			return
		}
		app(w, r)
	}
}

// withQuery keeps a request's query (the sign-in link's ?token=, a tab) on a redirect.
func withQuery(to string, r *http.Request) string {
	if r.URL.RawQuery == "" {
		return to
	}
	if strings.Contains(to, "?") {
		return to + "&" + r.URL.RawQuery
	}
	return to + "?" + r.URL.RawQuery
}

// oldPages are the old interface's page addresses and where each now lives in the app (ui-shop's old -> new URL
// map), answered with 301 so bookmarks keep working. A page that took an id (?id=N) keeps it in the path.
var oldPages = map[string]func(*http.Request) string{
	"/welcome/":               fixed("/start"),
	"/libraries/":             fixed("/libraries"),
	"/library/":               byID("/libraries/", "/libraries"),
	"/activity/":              activity,
	"/activity/skipped/":      fixed("/jobs?tab=skipped"),
	"/activity/failed/":       fixed("/jobs?tab=failed"),
	"/activity/done/":         fixed("/jobs?tab=completed"),
	"/activity/kept/":         fixed("/jobs?tab=completed"),
	"/flows/":                 fixed("/workflows"),
	"/flow/":                  byID("/workflows/", "/workflows"),
	"/workers/":               fixed("/workers"),
	"/settings/":              fixed("/settings/general"),
	"/settings/safety/":       fixed("/settings/safety"),
	"/settings/performance/":  fixed("/settings/processing"),
	"/settings/integrations/": fixed("/settings/integrations"),
	"/settings/system/":       fixed("/settings/about"),
}

// activity: the old Activity page's ?show= becomes the Jobs tab it meant; without one, Jobs.
func activity(r *http.Request) string {
	q := r.URL.Query()
	tab := map[string]string{"unprocessed": "waiting", "processing": "running", "processed": "completed", "skipped": "skipped", "failed": "failed"}[q.Get("show")]
	q.Del("show")
	r.URL.RawQuery = q.Encode()
	if tab == "" {
		return "/jobs"
	}
	return "/jobs?tab=" + tab
}

func fixed(to string) func(*http.Request) string { return func(*http.Request) string { return to } }

// byID: the old page's ?id= becomes the app's path (/library/?id=3 -> /libraries/3); without one, the list.
func byID(prefix, list string) func(*http.Request) string {
	return func(r *http.Request) string {
		q := r.URL.Query()
		id := q.Get("id")
		if id == "" || strings.ContainsAny(id, "/?#\\") {
			return list
		}
		q.Del("id")
		r.URL.RawQuery = q.Encode()
		return prefix + url.PathEscape(id)
	}
}
