// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
	"github.com/PeiceOfToast/shrink-your-shit/internal/system"
)

const (
	testPassword = "correct horse battery"
	testOrigin   = "http://example.com" // httptest requests use example.com as their host
	remote       = "10.0.0.1:5555"
)

// clock is moved by hand; sleeps are recorded, never waited for.
type clock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *clock) Sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
}

func (c *clock) Sleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

type env struct {
	t         *testing.T
	srv       *Server
	access    *system.Access
	bus       *events.Bus
	closeOnce sync.Once
	clock     *clock
	dir       string
	stderr    *bytes.Buffer
}

// newEnv serves a fresh data folder with the real store, events and system packages.
// ready=false leaves the server answering 503 starting.
func newEnv(t *testing.T, ready bool) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, srv: New("test-version"), clock: &clock{now: time.Date(2026, 11, 27, 4, 0, 0, 0, time.UTC)}, dir: dir, stderr: &bytes.Buffer{}}
	e.bus, err = events.New(events.Options{DataDir: dir, DB: db, Stderr: e.stderr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.closeBus() }) // runs before the store closes (cleanups run last-in, first-out)
	e.access, err = system.Open(db, dir, system.Options{Now: e.clock.Now, Sleep: e.clock.Sleep})
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		e.srv.Ready(e.access, e.bus)
	}
	return e
}

// do sends a request from remote; headers are set as given. secure makes it a TLS request.
func (e *env) do(method, path, body string, headers map[string]string, secure bool) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = remote
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if secure {
		req.TLS = &tls.ConnectionState{}
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	return rec
}

// closeBus flushes the events bus once; the log is read only after this.
func (e *env) closeBus() error {
	var err error
	e.closeOnce.Do(func() { err = e.bus.Close(context.Background()) })
	return err
}

// write is a browser write: same origin, with the X-SYP header the UI sends.
func write(extra map[string]string) map[string]string {
	h := map[string]string{"X-SYP": "1", "Origin": testOrigin}
	for k, v := range extra {
		h[k] = v
	}
	return h
}

// setup sets the password through the api and returns the session cookie it was signed in with.
func (e *env) setup() *http.Cookie {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/api/v2/setup", `{"account":{"password":"`+testPassword+`"},"library":null}`, write(nil), false)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("setup: %d %s", rec.Code, rec.Body)
	}
	return cookieNamed(e.t, rec, sessionCookie)
}

func cookieNamed(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s cookie in the response", name)
	return nil
}

// errorBody decodes the envelope and fails if it is not {error:{code, message}}.
func errorBody(t *testing.T, rec *httptest.ResponseRecorder) (code, message string, raw map[string]any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("body is not JSON: %q", rec.Body)
	}
	e, ok := raw["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error envelope: %q", rec.Body)
	}
	code, _ = e["code"].(string)
	message, _ = e["message"].(string)
	if code == "" || message == "" {
		t.Fatalf("envelope missing code or message: %q", rec.Body)
	}
	return code, message, e
}

func TestStartingAnswers503(t *testing.T) {
	e := newEnv(t, false)
	rec := e.do(http.MethodGet, "/api/v2/health", "", nil, false)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
	if code, _, _ := errorBody(t, rec); code != "starting" {
		t.Fatalf("code %q, want starting", code)
	}
}

func TestHealthNeedsNoAuth(t *testing.T) {
	e := newEnv(t, true)
	rec := e.do(http.MethodGet, "/api/v2/health", "", nil, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["ok"] != true || body["starting"] != false {
		t.Fatalf("health body %v; want ok and starting only", body)
	}
	if _, ok := body["version"]; ok {
		t.Fatalf("version shown without a session: %v", body)
	}
	cookie := e.setup()
	rec = e.do(http.MethodGet, "/api/v2/health", "", map[string]string{"Cookie": cookie.String()}, false)
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["version"] != "test-version" {
		t.Fatalf("signed-in health body %v; want the version", body)
	}
}

// Before setup only page 13's allow-list answers; everything else says set up first (L-ACC-3).
func TestBeforeSetupOnlyTheAllowListWorks(t *testing.T) {
	e := newEnv(t, true)
	rec := e.do(http.MethodGet, "/api/v2/setup", "", nil, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET setup: %d", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["needsAccount"] != true || body["starting"] != false {
		t.Fatalf("GET setup body %v", body)
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v2/library", ""},
		{http.MethodGet, "/", ""},
		{http.MethodPost, "/api/v2/signin", `{"password":"` + testPassword + `"}`},
		{http.MethodGet, "/?token=abc", ""},
	} {
		rec := e.do(c.method, c.path, c.body, write(nil), false)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s before setup: %d, want 403", c.method, c.path, rec.Code)
		}
		if code, _, _ := errorBody(t, rec); code != "setup_needed" {
			t.Fatalf("%s %s: code %q, want setup_needed", c.method, c.path, code)
		}
	}
}

// A cross-origin write is refused, and the browser header is required on a write (L-ACC-2).
func TestCrossOriginWriteRefused(t *testing.T) {
	e := newEnv(t, true)
	rec := e.do(http.MethodPost, "/api/v2/setup", `{"account":{"password":"`+testPassword+`"}}`,
		map[string]string{"X-SYP": "1", "Origin": "https://evil.example"}, false)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin: %d, want 403", rec.Code)
	}
	if code, _, _ := errorBody(t, rec); code != "csrf" {
		t.Fatalf("code %q, want csrf", code)
	}
}

func TestMissingHeaderRefused(t *testing.T) {
	e := newEnv(t, true)
	rec := e.do(http.MethodPost, "/api/v2/setup", `{"account":{"password":"`+testPassword+`"}}`,
		map[string]string{"Origin": testOrigin}, false)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no X-SYP: %d, want 403", rec.Code)
	}
	if code, _, _ := errorBody(t, rec); code != "csrf" {
		t.Fatalf("code %q, want csrf", code)
	}
}

// Setup: a short password, a library and a starter are refused with the envelope; a good one signs in.
func TestSetupRulesAndCookie(t *testing.T) {
	e := newEnv(t, true)
	rec := e.do(http.MethodPost, "/api/v2/setup", `{"account":{"password":"abcdefg"}}`, write(nil), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("7 chars: %d, want 400", rec.Code)
	}
	if code, _, env := errorBody(t, rec); code != "invalid_password" || env["field"] != "account.password" {
		t.Fatalf("7 chars: %v", env)
	}

	rec = e.do(http.MethodPost, "/api/v2/setup", `{"account":{"password":"`+testPassword+`"},"library":{"path":"/x"}}`, write(nil), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("library given: %d, want 400", rec.Code)
	}
	if code, _, env := errorBody(t, rec); code != "invalid_request" || env["field"] != "library" {
		t.Fatalf("library given: %v", env)
	}
	if need, _ := e.access.NeedsAccount(context.Background()); !need {
		t.Fatal("a refused setup must not set the password")
	}

	rec = e.do(http.MethodPost, "/api/v2/setup", `{"account":{"password":"`+testPassword+`"},"library":null}`, write(nil), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body)
	}
	c := cookieNamed(t, rec, sessionCookie)
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Secure {
		t.Fatalf("cookie flags: httponly=%v samesite=%v path=%q secure=%v", c.HttpOnly, c.SameSite, c.Path, c.Secure)
	}

	rec = e.do(http.MethodPost, "/api/v2/setup", `{"account":{"password":"`+testPassword+`"}}`, write(nil), false)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second setup: %d, want 409", rec.Code)
	}
	if code, _, _ := errorBody(t, rec); code != "setup_done" {
		t.Fatalf("code %q, want setup_done", code)
	}
}

// Over TLS the cookie is Secure (L-ACC-1).
func TestCookieSecureOverTLS(t *testing.T) {
	e := newEnv(t, true)
	rec := e.do(http.MethodPost, "/api/v2/setup", `{"account":{"password":"`+testPassword+`"}}`, write(nil), true)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup over TLS: %d", rec.Code)
	}
	if c := cookieNamed(t, rec, sessionCookie); !c.Secure {
		t.Fatal("cookie over TLS must be Secure")
	}
}

// Five wrong passwords: 401 each, the sixth 429 with Retry-After 60; the minute passing lets the next try through.
func TestSignInLockoutAndRecovery(t *testing.T) {
	e := newEnv(t, true)
	e.setup()
	wrong := `{"password":"wrong password!"}`
	for i := 1; i <= 5; i++ {
		rec := e.do(http.MethodPost, "/api/v2/signin", wrong, write(nil), false)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("try %d: %d, want 401", i, rec.Code)
		}
		if code, msg, _ := errorBody(t, rec); code != "bad_password" || msg != "That password isn't right." {
			t.Fatalf("try %d: %q %q", i, code, msg)
		}
	}
	rec := e.do(http.MethodPost, "/api/v2/signin", `{"password":"`+testPassword+`"}`, write(nil), false)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th try: %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("Retry-After %q, want 60", got)
	}
	code, msg, env := errorBody(t, rec)
	if code != "too_many_tries" || msg != "Too many tries. Wait a minute, then try again." || env["retryAfter"] != float64(60) {
		t.Fatalf("6th try envelope: %q %q %v", code, msg, env)
	}

	e.clock.Advance(59 * time.Second)
	if rec := e.do(http.MethodPost, "/api/v2/signin", `{"password":"`+testPassword+`"}`, write(nil), false); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("at 59 s: %d, want still 429", rec.Code)
	}
	e.clock.Advance(time.Second)
	rec = e.do(http.MethodPost, "/api/v2/signin", `{"password":"`+testPassword+`"}`, write(nil), false)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("after the minute: %d, want 204", rec.Code)
	}
	cookieNamed(t, rec, sessionCookie)
}

func TestSignInRejectsBothOrNeither(t *testing.T) {
	e := newEnv(t, true)
	e.setup()
	for _, body := range []string{`{}`, `{"password":"x","token":"y"}`} {
		rec := e.do(http.MethodPost, "/api/v2/signin", body, write(nil), false)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d, want 400", body, rec.Code)
		}
		if code, _, _ := errorBody(t, rec); code != "invalid_request" {
			t.Fatalf("%s: code %q", body, code)
		}
	}
}

// Without a session an api route answers signed_out; with the session it reaches the route.
func TestSessionGatesRoutes(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	rec := e.do(http.MethodGet, "/api/v2/library", "", nil, false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d, want 401", rec.Code)
	}
	if code, _, _ := errorBody(t, rec); code != "signed_out" {
		t.Fatalf("code %q, want signed_out", code)
	}
	rec = e.do(http.MethodGet, "/api/v2/library", "", map[string]string{"Cookie": cookie.Name + "=" + cookie.Value}, false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("with session: %d, want 404 (the route is not built yet)", rec.Code)
	}
	if code, _, _ := errorBody(t, rec); code != "not_found" {
		t.Fatalf("code %q, want not_found", code)
	}
}

// Signing out ends the session: the old cookie no longer passes.
func TestSignoutEndsSession(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	keep := map[string]string{"Cookie": cookie.Name + "=" + cookie.Value}
	rec := e.do(http.MethodPost, "/api/v2/signout", "", write(keep), false)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("signout: %d", rec.Code)
	}
	rec = e.do(http.MethodGet, "/api/v2/library", "", keep, false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("after signout: %d, want 401", rec.Code)
	}
}

// Cross-origin writes are refused even with a session cookie (L-ACC-2).
func TestCrossOriginWriteRefusedWithSession(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	rec := e.do(http.MethodPost, "/api/v2/signout", "", map[string]string{
		"X-SYP": "1", "Origin": "https://evil.example", "Cookie": cookie.Name + "=" + cookie.Value,
	}, false)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin signout: %d, want 403", rec.Code)
	}
}

// The access token is exempt from the browser checks (L-ACC-2): a bearer write with no header and a
// foreign origin still works.
func TestBearerExemptFromCSRF(t *testing.T) {
	e := newEnv(t, true)
	e.setup()
	token, err := e.access.RotateToken(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	rec := e.do(http.MethodPost, "/api/v2/signout", "", map[string]string{
		"Authorization": "Bearer " + token, "Origin": "https://evil.example",
	}, false)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("bearer write: %d, want 204", rec.Code)
	}
}

// A wrong bearer is refused with bad_token and slowed 500 ms (L-ACC-2).
func TestWrongBearerDelayed(t *testing.T) {
	e := newEnv(t, true)
	e.setup()
	rec := e.do(http.MethodGet, "/api/v2/library", "", map[string]string{"Authorization": "Bearer nope"}, false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong bearer: %d, want 401", rec.Code)
	}
	if code, _, _ := errorBody(t, rec); code != "bad_token" {
		t.Fatalf("code %q, want bad_token", code)
	}
	sleeps := e.clock.Sleeps()
	if len(sleeps) == 0 || sleeps[len(sleeps)-1] != 500*time.Millisecond {
		t.Fatalf("sleeps %v; want the last to be 500ms", sleeps)
	}
}

// ?token= swaps for a session cookie and redirects without the token; the token stays out of the logs.
func TestTokenLinkRedirectsAndStaysOutOfLogs(t *testing.T) {
	e := newEnv(t, true)
	e.setup()
	token, err := e.access.RotateToken(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	rec := e.do(http.MethodGet, "/?token="+token+"&view=home", "", nil, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("token link: %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/?view=home" {
		t.Fatalf("redirect %q, want /?view=home", loc)
	}
	cookieNamed(t, rec, sessionCookie)

	// A refused token also redirects (to the page's own sign-in form), with a warning and no cookie.
	bad := e.do(http.MethodGet, "/?token=not-the-token", "", nil, false)
	if bad.Code != http.StatusSeeOther || bad.Header().Get("Location") != "/" {
		t.Fatalf("bad token link: %d %q", bad.Code, bad.Header().Get("Location"))
	}
	for _, c := range bad.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			t.Fatal("a refused token must not set a session cookie")
		}
	}

	if err := e.closeBus(); err != nil {
		t.Fatal(err)
	}
	logBytes, err := os.ReadFile(filepath.Join(e.dir, "logs", "system.log"))
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"system.log": string(logBytes), "stderr": e.stderr.String()} {
		if strings.Contains(text, token) {
			t.Fatalf("the token appeared in %s", name)
		}
		if strings.Contains(text, "not-the-token") {
			t.Fatalf("the refused token appeared in %s", name)
		}
	}
	if !strings.Contains(string(logBytes), "sign-in refused from 10.0.0.1") {
		t.Fatalf("the refused sign-in was not logged: %q", logBytes)
	}
}

// Every failure path uses the envelope (page 00).
func TestErrorEnvelopeOnFailurePaths(t *testing.T) {
	e := newEnv(t, true)
	e.setup()
	for _, rec := range []*httptest.ResponseRecorder{
		e.do(http.MethodPost, "/api/v2/signin", `{"password":"wrong password!"}`, write(nil), false),
		e.do(http.MethodPost, "/api/v2/setup", `not json`, write(nil), false),
		e.do(http.MethodGet, "/api/v2/library", "", nil, false),
		e.do(http.MethodGet, "/no/such/page", "", nil, false),
	} {
		errorBody(t, rec)
	}
}
