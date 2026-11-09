// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// The new app at /next/ (V0): its files, app routes to index.html, missing scripts as 404, the cache
// rules, gzip, and /api/ untouched.
func TestNextApp(t *testing.T) {
	build := fstest.MapFS{
		"index.html":          {Data: []byte(`<!doctype html><div id="app"></div><script src="/next/assets/app-1a2b.js"></script>`)},
		"assets/app-1a2b.js":  {Data: []byte(strings.Repeat("console.log('app');", 100))},
		"assets/app-1a2b.css": {Data: []byte("body{}")},
		"favicon.svg":         {Data: []byte("<svg/>")},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /next/", nextApp(build))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{"error":{}}`, http.StatusNotFound) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	get := func(path string, gz bool) (int, http.Header, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if gz {
			req.Header.Set("Accept-Encoding", "gzip")
		} else {
			req.Header.Set("Accept-Encoding", "identity")
		}
		res, err := http.DefaultTransport.RoundTrip(req) // no transparent gunzip: see what was sent
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var r io.Reader = res.Body
		if res.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			r = zr
		}
		b, _ := io.ReadAll(r)
		return res.StatusCode, res.Header, string(b)
	}
	for _, c := range []struct {
		path, want, cache string
		code              int
	}{
		{"/next/", `<div id="app">`, "no-cache", 200},
		{"/next/workflows/12/graph", `<div id="app">`, "no-cache", 200}, // an app route survives a reload
		{"/next/assets/app-1a2b.js", "console.log", "immutable", 200},
		{"/next/favicon.svg", "<svg/>", "no-cache", 200},
		{"/next/assets/app-old.js", "", "no-cache", 404},     // a stale script is a 404, not HTML, and never cached (review 31668a2)
		{"/next/assets/", `<div id="app">`, "no-cache", 200}, // the folder: the app, never cached for a year
		{"/next/assets/sub/route", `<div id="app">`, "no-cache", 200},
		{"/next/missing.css", "", "", 404},
		{"/api/v2/nothing", `{"error"`, "", 404}, // the API is not the app's fallback
	} {
		code, h, body := get(c.path, false)
		if code != c.code || !strings.Contains(body, c.want) || c.cache != "" && !strings.Contains(h.Get("Cache-Control"), c.cache) {
			t.Errorf("%s: %d %q cache %q", c.path, code, body[:min(len(body), 60)], h.Get("Cache-Control"))
		}
	}
	if code, h, body := get("/next/assets/app-1a2b.js", true); code != 200 || h.Get("Content-Encoding") != "gzip" || !strings.Contains(body, "console.log") ||
		!strings.Contains(h.Get("Content-Type"), "javascript") {
		t.Errorf("gzip: %d %v", code, h)
	}
}

// Without a build (the placeholder: only dist/.keep), /next/ says so plainly and points at the current
// interface, which still works; the app's routes are the same page.
func TestNextAppNotBuilt(t *testing.T) {
	srv, _, _, _ := libServer(t, false) // the real Handler, with the placeholder embedded
	for _, p := range []string{"/next/", "/next/queue"} {
		code, body, _ := do(t, nil, srv, call{method: "GET", path: p})
		if code != http.StatusServiceUnavailable || !strings.Contains(body, "The new interface isn't in this copy") {
			t.Errorf("%s: %d %s", p, code, body)
		}
	}
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Get(srv.URL + "/next")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMovedPermanently || res.Header.Get("Location") != "/next/" {
		t.Errorf("/next: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
}
