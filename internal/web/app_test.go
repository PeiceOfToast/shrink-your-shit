// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The app at / (one UI): its files, app routes to index.html, missing scripts as 404, the cache rules, gzip,
// /api/ untouched, and only GET/HEAD.
func TestApp(t *testing.T) {
	build := fstest.MapFS{
		"index.html":          {Data: []byte(`<!doctype html><div id="app"></div><script src="/assets/app-1a2b.js"></script>`)},
		"assets/app-1a2b.js":  {Data: []byte(strings.Repeat("console.log('app');", 100))},
		"assets/app-1a2b.css": {Data: []byte("body{}")},
		"favicon.svg":         {Data: []byte("<svg/>")},
	}
	mux := http.NewServeMux()
	mux.Handle("/", appHandler(build))
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
		{"/", `<div id="app">`, "no-cache", 200},
		{"/workflows/12/graph", `<div id="app">`, "no-cache", 200}, // an app route survives a reload
		{"/assets/app-1a2b.js", "console.log", "immutable", 200},
		{"/favicon.svg", "<svg/>", "no-cache", 200},
		{"/assets/app-old.js", "", "no-cache", 404},     // a stale script is a 404, not HTML, and never cached (review 31668a2)
		{"/assets/", `<div id="app">`, "no-cache", 200}, // the folder: the app, never cached for a year
		{"/assets/sub/route", `<div id="app">`, "no-cache", 200},
		{"/missing.css", "", "", 404},
		{"/api/v2/nothing", `{"error"`, "", 404}, // the API is not the app's fallback
	} {
		code, h, body := get(c.path, false)
		if code != c.code || !strings.Contains(body, c.want) || c.cache != "" && !strings.Contains(h.Get("Cache-Control"), c.cache) {
			t.Errorf("%s: %d %q cache %q", c.path, code, body[:min(len(body), 60)], h.Get("Cache-Control"))
		}
	}
	if res, err := http.Post(srv.URL+"/", "application/json", nil); err != nil || res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /: %v %v", err, res)
	} else {
		res.Body.Close()
	}
	if code, h, body := get("/assets/app-1a2b.js", true); code != 200 || h.Get("Content-Encoding") != "gzip" || !strings.Contains(body, "console.log") ||
		!strings.Contains(h.Get("Content-Type"), "javascript") {
		t.Errorf("gzip: %d %v", code, h)
	}
}

// Without a build (the placeholder: only dist/.keep), the app's address says so plainly; the app's routes are the
// same page. A test filesystem, so this holds whether or not web/ was built here.
func TestAppNotBuilt(t *testing.T) {
	srv := httptest.NewServer(appHandler(fstest.MapFS{".keep": {}}))
	defer srv.Close()
	for _, p := range []string{"/", "/jobs"} {
		res, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(b), "The interface isn't in this copy") {
			t.Errorf("%s: %d %s", p, res.StatusCode, b)
		}
	}
}

// The real handler (one UI): the app at / (built or not: 200 or 503), also with the printed sign-in link's ?token=;
// /next and /next/<path> move to the same path at / with the query; the old pages move to their place in the app;
// /healthz and /api/ are never the app.
func TestAppMounted(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, p := range []string{"/", "/?token=abc", "/jobs?tab=failed", "/workflows/12/graph"} {
		if code, _, _ := do(t, nil, srv, call{method: "GET", path: p}); code != http.StatusOK && code != http.StatusServiceUnavailable {
			t.Errorf("%s: %d, want the app", p, code)
		}
	}
	for from, to := range map[string]string{
		"/next": "/", "/next/": "/", "/next/jobs?tab=failed&job=7": "/jobs?tab=failed&job=7", "/next/signin?token=abc": "/signin?token=abc",
		"/libraries/": "/libraries", "/settings/": "/settings/general", "/activity/": "/jobs", "/activity/?show=processing": "/jobs?tab=running", "/library/?id=3": "/libraries/3", "/library/?id=3&x=1": "/libraries/3?x=1", "/library/": "/libraries",
		"/activity/failed/": "/jobs?tab=failed", "/flow/?id=12": "/workflows/12", "/settings/performance/?a=b": "/settings/processing?a=b",
	} {
		res, err := noFollow.Get(srv.URL + from)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusMovedPermanently || res.Header.Get("Location") != to {
			t.Errorf("%s: %d %q, want 301 %q", from, res.StatusCode, res.Header.Get("Location"), to)
		}
	}
	if code, body, _ := do(t, nil, srv, call{method: "GET", path: "/healthz"}); code != 200 || body != "ok\n" {
		t.Errorf("healthz: %d %q", code, body)
	}
	if code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/no-such-route"}); code != http.StatusNotFound || strings.Contains(body, "<!doctype") || strings.Contains(body, "interface") {
		t.Errorf("an unknown API path: %d %q, want a 404 that isn't the app", code, body)
	}
}

// The languages list the app offers (web/public/languages.json, served at /languages.json): each code is the one the
// server stores, listed once, with a name (moved from the old interface's static/languages.json).
func TestLanguageListCodes(t *testing.T) {
	b, err := os.ReadFile("../../web/public/languages.json")
	if err != nil {
		t.Fatal(err)
	}
	var list [][]string
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, l := range list {
		if len(l) < 2 || len(l) > 3 || l[1] == "" {
			t.Errorf("%v: want [code, name] or [code, name, English name]", l)
			continue
		}
		if got := rules.CanonLang(l[0]); got != l[0] {
			t.Errorf("%s (%s): the server stores it as %q", l[0], l[1], got)
		}
		if seen[l[0]] {
			t.Errorf("%s is listed twice", l[0])
		}
		seen[l[0]] = true
	}
}
