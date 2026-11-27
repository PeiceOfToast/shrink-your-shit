// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

// testUI is a small build: the page shell and two hashed assets.
func testUI() fstest.MapFS {
	return fstest.MapFS{
		"index.html":        {Data: []byte("<!doctype html><title>app</title>")},
		"assets/app-abc.js": {Data: []byte("console.log(1)\n")},
		"assets/style.css":  {Data: []byte("body{}\n")},
	}
}

// The page answers every non-/api path: the shell for / and for a client route, and the API beside
// it stays closed.
func TestStaticServesThePages(t *testing.T) {
	e := newEnv(t, true)
	e.srv.SetUI(testUI())

	// Before setup the first-run page itself must answer, or nobody can start.
	rec := e.do(http.MethodGet, "/", "", nil, false)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>app</title>") {
		t.Fatalf("GET / before setup: %d %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("index type %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != cacheNoCache {
		t.Fatalf("index cache %q", got)
	}
	if rec := e.do(http.MethodGet, "/api/v2/nope", "", nil, false); rec.Code != http.StatusForbidden {
		t.Fatalf("GET /api/v2/nope before setup: %d, want 403", rec.Code)
	}

	e.setup()

	// A signed-out caller still gets the shell: the page shows its sign-in form.
	rec = e.do(http.MethodGet, "/jobs/12", "", nil, false)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>app</title>") {
		t.Fatalf("GET /jobs/12 signed out: %d %s", rec.Code, rec.Body)
	}
	if code, _, _ := errorBody(t, e.do(http.MethodGet, "/api/v2/nope", "", nil, false)); code != "signed_out" {
		t.Fatalf("GET /api/v2/nope signed out: code %q", code)
	}
}

// A signed-in caller sees the assets with their types and cache rules, and the JSON 404 for an
// unknown asset, an /api path and a write.
func TestStaticFilesAndCache(t *testing.T) {
	e := newEnv(t, true)
	e.srv.SetUI(testUI())
	cookie := e.setup()
	signedIn := map[string]string{"Cookie": sessionCookie + "=" + cookie.Value}

	rec := e.do(http.MethodGet, "/assets/app-abc.js", "", signedIn, false)
	if rec.Code != http.StatusOK || rec.Body.String() != "console.log(1)\n" {
		t.Fatalf("GET the script: %d %q", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Fatalf("script type %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != cacheImmutable {
		t.Fatalf("script cache %q", got)
	}

	rec = e.do(http.MethodGet, "/assets/style.css", "", signedIn, false)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/css; charset=utf-8" {
		t.Fatalf("GET the stylesheet: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}

	rec = e.do(http.MethodGet, "/assets/nope.js", "", signedIn, false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset: %d, want 404", rec.Code)
	}
	if code, _, _ := errorBody(t, rec); code != "not_found" {
		t.Fatalf("missing asset: code %q", code)
	}

	// The API's own 404 keeps the JSON envelope rather than handing back the page shell.
	for _, p := range []string{"/api/v2/nope", "/api"} {
		rec = e.do(http.MethodGet, p, "", signedIn, false)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s: %d, want 404", p, rec.Code)
		}
		if code, _, _ := errorBody(t, rec); code != "not_found" {
			t.Fatalf("GET %s: code %q", p, code)
		}
	}

	// A write to a page is not served.
	rec = e.do(http.MethodPost, "/jobs/12", "", write(signedIn), false)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST a page: %d, want 405", rec.Code)
	}
	if code, _, _ := errorBody(t, rec); code != "method_not_allowed" {
		t.Fatalf("POST a page: code %q", code)
	}
}

// A server with no interface answers every page with the JSON 404, as it did before the embed.
func TestStaticWithoutAnInterface(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	rec := e.do(http.MethodGet, "/", "", map[string]string{"Cookie": sessionCookie + "=" + cookie.Value}, false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET / without a UI: %d, want 404", rec.Code)
	}
	if code, _, _ := errorBody(t, rec); code != "not_found" {
		t.Fatalf("GET / without a UI: code %q", code)
	}
}
