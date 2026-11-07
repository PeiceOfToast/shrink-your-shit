// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// openAccess: loopback, no token stored (the API is open).
func openAccess(t *testing.T) *Access {
	t.Helper()
	a, _, err := OpenAccess(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// server starts the app; exposed means listening beyond loopback, so a token is made.
func server(t *testing.T, exposed bool) (*httptest.Server, *Access, *jobs.Store) {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a, _, err := OpenAccess(t.TempDir(), exposed)
	if err != nil {
		t.Fatal(err)
	}
	a.FailDelay = 0
	srv := httptest.NewServer(Handler(st, []string{"/lib"}, a))
	t.Cleanup(srv.Close)
	return srv, a, st
}

type call struct {
	method, path, body, ctype, bearer, origin, fetchSite string
}

func do(t *testing.T, c *http.Client, srv *httptest.Server, k call) (int, string, *http.Response) {
	t.Helper()
	var body io.Reader
	if k.body != "" {
		body = strings.NewReader(k.body)
	}
	req, _ := http.NewRequest(k.method, srv.URL+k.path, body)
	if k.ctype != "" {
		req.Header.Set("Content-Type", k.ctype)
	}
	if k.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+k.bearer)
	}
	if k.origin != "" {
		req.Header.Set("Origin", k.origin)
	}
	if k.fetchSite != "" {
		req.Header.Set("Sec-Fetch-Site", k.fetchSite)
	}
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, string(b), resp
}

func errCode(body string) string {
	var e struct {
		Error struct{ Code, Message string }
	}
	json.Unmarshal([]byte(body), &e)
	return e.Error.Code
}

func jar(t *testing.T) *http.Client {
	j, _ := cookiejar.New(nil)
	return &http.Client{Jar: j}
}

func TestOpenAccessMakesATokenOnlyWhenExposed(t *testing.T) {
	dir := t.TempDir()
	a, created, err := OpenAccess(dir, false)
	if err != nil || created || a.Required() {
		t.Fatalf("loopback must not make a token: %v %v %v", created, a.Required(), err)
	}
	a, created, _ = OpenAccess(dir, true)
	tok := a.Token()
	if !created || len(tok) != 43 || strings.ContainsAny(tok, "+/=") {
		t.Fatalf("want a new 32-byte URL-safe token, got created=%v %q", created, tok)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(filepath.Join(dir, TokenFile)); st.Mode().Perm() != 0o600 {
			t.Errorf("token file mode %v, want 0600", st.Mode().Perm())
		}
	}
	a, created, _ = OpenAccess(dir, true)
	if created || a.Token() != tok {
		t.Errorf("a stored token must be kept: created=%v", created)
	}
	if got, _ := ReadToken(dir); got != tok {
		t.Errorf("ReadToken = %q", got)
	}
}

func TestNoAuthIs401(t *testing.T) {
	srv, _, _ := server(t, true)
	for _, k := range []call{
		{method: "GET", path: "/api/status"},
		{method: "GET", path: "/api/settings"},
		{method: "GET", path: "/api/settings/token"},
		{method: "POST", path: "/api/jobs/1/retry", body: "{}", ctype: "application/json"},
		{method: "POST", path: "/api/settings/token/rotate", body: "{}", ctype: "application/json"},
	} {
		code, body, _ := do(t, nil, srv, k)
		if code != 401 || errCode(body) != "auth" {
			t.Errorf("%s %s: %d %s, want 401 auth", k.method, k.path, code, body)
		}
	}
}

func TestCookieLogin(t *testing.T) {
	srv, a, _ := server(t, true)
	c := jar(t)
	code, body, _ := do(t, c, srv, call{method: "POST", path: "/api/login", body: `{"token":"wrong"}`, ctype: "application/json"})
	if code != 401 || errCode(body) != "bad_token" {
		t.Fatalf("wrong token: %d %s", code, body)
	}
	code, _, resp := do(t, c, srv, call{method: "POST", path: "/api/login", body: `{"token":"` + a.Token() + `"}`, ctype: "application/json"})
	if code != 200 {
		t.Fatalf("login: %d", code)
	}
	ck := resp.Cookies()
	if len(ck) != 1 || !ck[0].HttpOnly || ck[0].SameSite != http.SameSiteStrictMode || strings.Contains(ck[0].Value, a.Token()) {
		t.Fatalf("want one HttpOnly SameSite=Strict cookie that is not the token: %+v", ck)
	}
	if code, body, _ := do(t, c, srv, call{method: "GET", path: "/api/settings"}); code != 200 || !strings.Contains(body, `"authRequired":true`) {
		t.Errorf("signed in: %d %s", code, body)
	}
	do(t, c, srv, call{method: "POST", path: "/api/logout", body: "{}", ctype: "application/json"})
	if code, _, _ := do(t, c, srv, call{method: "GET", path: "/api/status"}); code != 401 {
		t.Errorf("after logout: %d, want 401", code)
	}
}

func TestBearer(t *testing.T) {
	srv, a, _ := server(t, true)
	if code, _, _ := do(t, nil, srv, call{method: "GET", path: "/api/status", bearer: a.Token()}); code != 200 {
		t.Errorf("bearer: %d", code)
	}
	if code, _, _ := do(t, nil, srv, call{method: "GET", path: "/api/status", bearer: "nope"}); code != 401 {
		t.Errorf("wrong bearer: %d", code)
	}
	code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/settings/token", bearer: a.Token()})
	if code != 200 || !strings.Contains(body, a.Token()) {
		t.Errorf("token: %d %s", code, body)
	}
}

func TestCSRF(t *testing.T) {
	srv, a, st := server(t, true)
	c := jar(t)
	do(t, c, srv, call{method: "POST", path: "/api/login", body: `{"token":"` + a.Token() + `"}`, ctype: "application/json"})
	id, _ := st.Enqueue(jobs.Job{Library: "/lib", Path: "/lib/a.mkv"})
	st.Claim("w")
	st.Fail(id, "bad")
	retry := "/api/jobs/1/retry"
	for name, k := range map[string]call{
		"another site's Origin": {method: "POST", path: retry, body: "{}", ctype: "application/json", origin: "http://evil.example"},
		"cross-site fetch":      {method: "POST", path: retry, body: "{}", ctype: "application/json", fetchSite: "cross-site"},
		"not JSON":              {method: "POST", path: retry, body: "{}", ctype: "text/plain"},
		"no Content-Type":       {method: "POST", path: retry, body: "{}"},
		"login from elsewhere":  {method: "POST", path: "/api/login", body: `{"token":"` + a.Token() + `"}`, ctype: "application/json", origin: "http://evil.example"},
		"bearer, not JSON":      {method: "POST", path: retry, body: "{}", ctype: "text/plain", bearer: a.Token()},
	} {
		if code, body, _ := do(t, c, srv, k); code != 403 || errCode(body) != "csrf" {
			t.Errorf("%s: %d %s, want 403 csrf", name, code, body)
		}
	}
	if j, _ := st.Get(id); j.Status != jobs.Failed {
		t.Fatalf("a refused request changed the job: %s", j.Status)
	}
	// this site's own page, and a script with the token from anywhere
	if code, body, _ := do(t, c, srv, call{method: "POST", path: retry, body: "{}", ctype: "application/json", origin: srv.URL, fetchSite: "same-origin"}); code != 200 {
		t.Errorf("same-origin write: %d %s", code, body)
	}
	st.Claim("w")
	st.Fail(id, "bad")
	if code, body, _ := do(t, nil, srv, call{method: "POST", path: retry, body: "{}", ctype: "application/json; charset=utf-8", bearer: a.Token(), origin: "http://evil.example"}); code != 200 {
		t.Errorf("bearer is exempt from the Origin check: %d %s", code, body)
	}
}

func TestLoopbackWithoutTokenIsOpen(t *testing.T) {
	srv, _, _ := server(t, false)
	if code, _, _ := do(t, nil, srv, call{method: "GET", path: "/api/status"}); code != 200 {
		t.Errorf("status: %d", code)
	}
	if code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/settings"}); code != 200 || !strings.Contains(body, `"authRequired":false`) {
		t.Errorf("settings: %d %s", code, body)
	}
	if code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/settings/token"}); code != 404 || errCode(body) != "no_token" {
		t.Errorf("token with none set: %d %s", code, body)
	}
	// writes still have to come from this site: a page on another site could otherwise reach 127.0.0.1
	if code, _, _ := do(t, nil, srv, call{method: "POST", path: "/api/jobs/1/retry", body: "{}", ctype: "application/json", origin: "http://evil.example"}); code != 403 {
		t.Errorf("cross-site write on loopback: %d, want 403", code)
	}
}

func TestStoredTokenIsEnforcedOnLoopback(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, TokenFile), []byte("set-by-hand\n"), 0o600)
	a, created, err := OpenAccess(dir, false)
	if err != nil || created || a.Token() != "set-by-hand" {
		t.Fatalf("%v %v %q", err, created, a.Token())
	}
	srv := httptest.NewServer(Handler(nil, nil, a))
	defer srv.Close()
	if code, _, _ := do(t, nil, srv, call{method: "GET", path: "/api/settings"}); code != 401 {
		t.Errorf("loopback with a stored token: %d, want 401", code)
	}
}

func TestRotateEndsSessions(t *testing.T) {
	srv, a, _ := server(t, true)
	old := a.Token()
	me, other := jar(t), jar(t)
	for _, c := range []*http.Client{me, other} {
		do(t, c, srv, call{method: "POST", path: "/api/login", body: `{"token":"` + old + `"}`, ctype: "application/json"})
	}
	code, body, _ := do(t, me, srv, call{method: "POST", path: "/api/settings/token/rotate", body: "{}", ctype: "application/json"})
	var out struct{ Token string }
	json.Unmarshal([]byte(body), &out)
	if code != 200 || out.Token == "" || out.Token == old || out.Token != a.Token() {
		t.Fatalf("rotate: %d %s", code, body)
	}
	if code, _, _ := do(t, other, srv, call{method: "GET", path: "/api/status"}); code != 401 {
		t.Errorf("another browser's session must end: %d", code)
	}
	if code, _, _ := do(t, nil, srv, call{method: "GET", path: "/api/status", bearer: old}); code != 401 {
		t.Errorf("the old token must stop working: %d", code)
	}
	if code, _, _ := do(t, me, srv, call{method: "GET", path: "/api/status"}); code != 200 {
		t.Errorf("whoever rotated stays signed in: %d", code)
	}
	if got, _ := ReadToken(a.dir); got != out.Token {
		t.Errorf("the new token was not stored: %q", got)
	}
}

func TestHealthzPagesAndAssetsArePublic(t *testing.T) {
	srv, _, _ := server(t, true)
	if code, body, _ := do(t, nil, srv, call{method: "GET", path: "/healthz"}); code != 200 || body != "ok\n" {
		t.Errorf("healthz: %d %q", code, body)
	}
	for _, p := range []string{"/assets/app.css", "/assets/app.js", "/libraries/", "/settings/"} {
		if code, _, _ := do(t, nil, srv, call{method: "GET", path: p}); code != 200 {
			t.Errorf("%s: %d, must load without signing in", p, code)
		}
	}
}

// Rotations racing each other must leave the token in use equal to the one stored for the next start.
// The second rotation is started in the gap between the first one storing its token and using it.
func TestConcurrentRotationsAgreeWithTheStoredToken(t *testing.T) {
	a, _, err := OpenAccess(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	var second sync.WaitGroup
	var started atomic.Bool
	afterTokenStored = func() {
		if started.Swap(true) {
			return // the second rotation
		}
		second.Add(1)
		done := make(chan struct{})
		go func() { defer second.Done(); a.rotate(); close(done) }()
		select { // without the lock, the second rotation finishes here; with it, it has to wait
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
	t.Cleanup(func() { afterTokenStored = nil })
	a.rotate()
	second.Wait()
	if stored, _ := ReadToken(a.dir); stored != a.Token() {
		t.Fatalf("in use %q, stored %q", a.Token(), stored)
	}
}
