// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// peerServer serves the API as if every request came from peer (the test server's own peer is loopback).
// trusted names the proxies whose forwarded address is believed.
func peerServer(t *testing.T, a *Access, peer string, trusted ...string) *httptest.Server {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetPaused(false)
	t.Cleanup(func() { st.Close() })
	proxies, err := ParseTrustedProxies(strings.Join(trusted, ","))
	if err != nil {
		t.Fatal(err)
	}
	h := Handler(st, a, Engine{Transport: Transport{TrustedProxies: proxies}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = peer
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newAccess opens a data folder the way the program does; exposed makes a token, as listening beyond
// this computer does.
func newAccess(t *testing.T, exposed bool) *Access {
	t.Helper()
	a, _, err := OpenAccess(t.TempDir(), exposed)
	if err != nil {
		t.Fatal(err)
	}
	a.FailDelay = 0
	return a
}

func authState(t *testing.T, srv *httptest.Server, c *http.Client) map[string]any {
	t.Helper()
	code, body, _ := do(t, c, srv, call{method: "GET", path: "/api/auth"})
	var out map[string]any
	if code != 200 || json.Unmarshal([]byte(body), &out) != nil {
		t.Fatalf("GET /api/auth: %d %s", code, body)
	}
	return out
}

func statusWith(t *testing.T, srv *httptest.Server, headers map[string]string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+"/api/status", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestNewInstallListensAtHome(t *testing.T) {
	a := newAccess(t, false)
	srv := peerServer(t, a, "127.0.0.1:5555")
	got := authState(t, srv, nil)
	if got["mode"] != ModeHome || got["passwordSet"] != false || got["open"] != true || got["signedIn"] != false {
		t.Errorf("new install: %v", got)
	}
}

// A new Docker install starts in password mode: its clients all look private, so home mode would admit them.
func TestNewDockerInstallStartsInPassword(t *testing.T) {
	t.Setenv("SYP_INSTALL", "docker")
	a := newAccess(t, true)
	srv := peerServer(t, a, "127.0.0.1:5555")
	got := authState(t, srv, nil)
	if got["mode"] != ModePassword || got["open"] != false {
		t.Errorf("new Docker install: %v", got)
	}
	if code, _, _ := do(t, nil, srv, call{method: "GET", path: "/api/status"}); code != 401 {
		t.Errorf("new Docker install without a sign-in: %d, want 401", code)
	}
}

// An install from before the sign-in choice (a token file, no signin.json) keeps asking for the token.
func TestExistingInstallStartsAsPasswordWithoutOne(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, TokenFile), []byte("old-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, _, err := OpenAccess(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	a.FailDelay = 0
	srv := peerServer(t, a, "127.0.0.1:5555")
	got := authState(t, srv, nil)
	if got["mode"] != ModePassword || got["passwordSet"] != false || got["open"] != false {
		t.Errorf("existing install: %v", got)
	}
	if code, _, _ := do(t, nil, srv, call{method: "GET", path: "/api/status"}); code != 401 {
		t.Errorf("existing install without a sign-in: %d, want 401", code)
	}
}

// Refusal 1: in home mode a private address gets in with no sign-in; a public address must still sign in.
func TestHomeModeOpensPrivateAddressesOnly(t *testing.T) {
	a := newAccess(t, true)
	if err := a.save(signinState{Mode: ModeHome}); err != nil {
		t.Fatal(err)
	}
	private := peerServer(t, a, "192.168.1.20:5555")
	if code := statusWith(t, private, nil); code != 200 {
		t.Errorf("private address in home mode: %d, want 200", code)
	}
	public := peerServer(t, a, "203.0.113.9:5555")
	if code := statusWith(t, public, nil); code != 401 {
		t.Errorf("public address in home mode: %d, want 401", code)
	}
	if got := authState(t, public, nil); got["open"] != false || got["mode"] != ModeHome {
		t.Errorf("public address: %v", got)
	}
}

// Refusal 2: a public peer that writes a private X-Forwarded-For is not believed (it is not a trusted proxy).
func TestForgedForwardedForFromAPublicPeerIsRefused(t *testing.T) {
	a := newAccess(t, true)
	if err := a.save(signinState{Mode: ModeHome}); err != nil {
		t.Fatal(err)
	}
	srv := peerServer(t, a, "203.0.113.9:5555")
	if code := statusWith(t, srv, map[string]string{"X-Forwarded-For": "192.168.1.20"}); code != 401 {
		t.Errorf("forged private X-Forwarded-For: %d, want 401", code)
	}
}

// Through a trusted proxy the address it reports decides: a private one is let in, a public one is not, and
// a proxy that reports nothing is not taken for its own address.
func TestTrustedProxyDecidesByTheClientItReports(t *testing.T) {
	a := newAccess(t, true)
	if err := a.save(signinState{Mode: ModeHome}); err != nil {
		t.Fatal(err)
	}
	srv := peerServer(t, a, "10.0.0.1:443", "10.0.0.1")
	if code := statusWith(t, srv, map[string]string{"X-Forwarded-For": "192.168.1.20"}); code != 200 {
		t.Errorf("trusted proxy reports a private client: %d, want 200", code)
	}
	if code := statusWith(t, srv, map[string]string{"X-Forwarded-For": "203.0.113.9"}); code != 401 {
		t.Errorf("trusted proxy reports a public client: %d, want 401", code)
	}
	if code := statusWith(t, srv, nil); code != 401 {
		t.Errorf("trusted proxy reports nothing: %d, want 401", code)
	}
}

// Turning sign-in off from outside the home network would lock the caller out: refused, nothing changes.
func TestHomeModeCannotBeSetFromOutside(t *testing.T) {
	a := newAccess(t, true)
	if err := a.save(signinState{Mode: ModePassword}); err != nil {
		t.Fatal(err)
	}
	srv := peerServer(t, a, "203.0.113.9:5555")
	code, body, _ := do(t, nil, srv, call{method: "POST", path: "/api/auth/mode", body: `{"mode":"home"}`,
		ctype: "application/json", bearer: a.Token()})
	if code != 409 || errCode(body) != "would_lock_out" {
		t.Fatalf("home from outside: %d %s", code, body)
	}
	if got := authState(t, srv, nil); got["mode"] != ModePassword {
		t.Errorf("mode after the refusal: %v", got)
	}
}

func modeChange(t *testing.T, srv *httptest.Server, c *http.Client, bearer, body string) (int, string) {
	t.Helper()
	code, b, _ := do(t, c, srv, call{method: "POST", path: "/api/auth/mode", body: body, ctype: "application/json", bearer: bearer})
	return code, b
}

func TestPasswordModeNeedsAPasswordThatIsLongEnough(t *testing.T) {
	a := newAccess(t, true)
	if err := a.save(signinState{Mode: ModePassword}); err != nil {
		t.Fatal(err)
	}
	srv := peerServer(t, a, "127.0.0.1:5555")
	for _, k := range []struct{ body, code string }{
		{`{"mode":"password"}`, "password_needed"},
		{`{"mode":"password","password":"short"}`, "password_too_short"},
		{`{"mode":"password","password":"` + strings.Repeat("a", 73) + `"}`, "password_too_long"},
		{`{"mode":"sideways"}`, "bad_mode"},
	} {
		code, body := modeChange(t, srv, nil, a.Token(), k.body)
		if code != 400 || errCode(body) != k.code {
			t.Errorf("%s: %d %s, want 400 %s", k.body[:min(len(k.body), 40)], code, body, k.code)
		}
	}
	if got := authState(t, srv, nil); got["passwordSet"] != false {
		t.Errorf("a refused change set a password: %v", got)
	}
}

// Sign in with a password, a wrong one is refused, the session cookie is HttpOnly and not the token.
func TestPasswordSignIn(t *testing.T) {
	a := newAccess(t, true)
	srv := peerServer(t, a, "127.0.0.1:5555")
	code, body := modeChange(t, srv, nil, a.Token(), `{"mode":"password","password":"correct horse"}`)
	if code != 200 {
		t.Fatalf("set password: %d %s", code, body)
	}
	c := jar(t)
	if code, body, _ := do(t, c, srv, call{method: "POST", path: "/api/login", body: `{"password":"wrong one here"}`, ctype: "application/json"}); code != 401 || errCode(body) != "bad_password" {
		t.Fatalf("wrong password: %d %s", code, body)
	}
	code, _, resp := do(t, c, srv, call{method: "POST", path: "/api/login", body: `{"password":"correct horse"}`, ctype: "application/json"})
	if code != 204 {
		t.Fatalf("right password: %d", code)
	}
	if ck := resp.Cookies(); len(ck) != 1 || !ck[0].HttpOnly || strings.Contains(ck[0].Value, a.Token()) {
		t.Errorf("cookie: %+v", ck)
	}
	if code, _, _ := do(t, c, srv, call{method: "GET", path: "/api/status"}); code != 200 {
		t.Errorf("signed in with the password: %d", code)
	}
}

// Five wrong tries from one address lock it out for a minute: 429 with retryAfter and Retry-After.
func TestLoginLocksAfterRepeatedWrongPasswords(t *testing.T) {
	a := newAccess(t, false)
	if err := a.save(signinState{Mode: ModePassword}); err != nil {
		t.Fatal(err)
	}
	if err := ResetPassword(a.dir, "correct horse"); err != nil {
		t.Fatal(err)
	}
	srv := peerServer(t, a, "127.0.0.1:5555")
	for i := 0; i < loginMaxFails; i++ {
		if code, body, _ := do(t, nil, srv, call{method: "POST", path: "/api/login", body: `{"password":"nope nope nope"}`, ctype: "application/json"}); code != 401 || errCode(body) != "bad_password" {
			t.Fatalf("try %d: %d %s", i+1, code, body)
		}
	}
	code, body, resp := do(t, nil, srv, call{method: "POST", path: "/api/login", body: `{"password":"correct horse"}`, ctype: "application/json"})
	if code != 429 || errCode(body) != "too_many_tries" || resp.Header.Get("Retry-After") != "60" || !strings.Contains(body, `"retryAfter":60`) {
		t.Errorf("locked: %d %s Retry-After=%q", code, body, resp.Header.Get("Retry-After"))
	}
}

// Changing the password ends the other browsers' sessions; the one that changed it stays signed in.
func TestPasswordChangeEndsOtherSessions(t *testing.T) {
	a := newAccess(t, true)
	if err := ResetPassword(a.dir, "first password"); err != nil {
		t.Fatal(err)
	}
	srv := peerServer(t, a, "127.0.0.1:5555")
	other, me := jar(t), jar(t)
	for _, c := range []*http.Client{other, me} {
		if code, _, _ := do(t, c, srv, call{method: "POST", path: "/api/login", body: `{"password":"first password"}`, ctype: "application/json"}); code != 204 {
			t.Fatalf("sign in: %d", code)
		}
	}
	if code, body := modeChange(t, srv, me, "", `{"mode":"password","password":"second password"}`); code != 200 {
		t.Fatalf("change: %d %s", code, body)
	}
	if code, _, _ := do(t, other, srv, call{method: "GET", path: "/api/status"}); code != 401 {
		t.Errorf("the other browser's session must end: %d", code)
	}
	if code, _, _ := do(t, me, srv, call{method: "GET", path: "/api/status"}); code != 200 {
		t.Errorf("the changing browser must stay signed in: %d", code)
	}
}

// reset-password writes the file from another process; the running server uses it without a restart.
func TestResetPasswordIsUsedWithoutARestart(t *testing.T) {
	a := newAccess(t, false)
	if err := ResetPassword(a.dir, "first password"); err != nil {
		t.Fatal(err)
	}
	srv := peerServer(t, a, "127.0.0.1:5555")
	if code, _, _ := do(t, nil, srv, call{method: "POST", path: "/api/login", body: `{"password":"first password"}`, ctype: "application/json"}); code != 204 {
		t.Fatalf("first password: %d", code)
	}
	if err := ResetPassword(a.dir, "second password"); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := do(t, nil, srv, call{method: "POST", path: "/api/login", body: `{"password":"first password"}`, ctype: "application/json"}); code != 401 {
		t.Errorf("old password after reset: %d, want 401", code)
	}
	if code, _, _ := do(t, nil, srv, call{method: "POST", path: "/api/login", body: `{"password":"second password"}`, ctype: "application/json"}); code != 204 {
		t.Errorf("new password after reset: %d, want 204", code)
	}
}

// Run as root, reset-password hands the file to the folder's owner, or the server could not read it.
func TestResetPasswordHandsTheFileToTheFolderOwner(t *testing.T) {
	dir := t.TempDir()
	oldOwner, oldChown := ownerOf, chownFile
	defer func() { ownerOf, chownFile = oldOwner, oldChown }()
	ownerOf = func(string) (int, int, bool) { return 1000, 1001, true }
	var gotPath string
	var gotUID, gotGID int
	chownFile = func(p string, uid, gid int) error { gotPath, gotUID, gotGID = p, uid, gid; return nil }
	if err := ResetPassword(dir, "first password"); err != nil {
		t.Fatal(err)
	}
	if gotPath != filepath.Join(dir, SigninFile) || gotUID != 1000 || gotGID != 1001 {
		t.Errorf("chown: %q to %d:%d", gotPath, gotUID, gotGID)
	}
}

// When the hand-off fails the reset says so, instead of reporting success the server cannot use.
func TestResetPasswordSaysWhenTheFileCannotBeHanded(t *testing.T) {
	dir := t.TempDir()
	oldOwner, oldChown := ownerOf, chownFile
	defer func() { ownerOf, chownFile = oldOwner, oldChown }()
	ownerOf = func(string) (int, int, bool) { return 1000, 1000, true }
	chownFile = func(string, int, int) error { return errors.New("operation not permitted") }
	err := ResetPassword(dir, "first password")
	if err == nil || !strings.Contains(err.Error(), "cannot read it") {
		t.Errorf("a failed hand-off: %v", err)
	}
}

// A signin.json the server cannot read is logged once per change, and the choice in use stays.
func TestUnreadableSigninFileIsLoggedAndKeepsTheChoice(t *testing.T) {
	a := newAccess(t, false)
	if err := a.save(signinState{Mode: ModePassword}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)
	if err := os.WriteFile(filepath.Join(a.dir, SigninFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.current()
	if got := a.current(); got.mode != ModePassword {
		t.Errorf("the choice in use changed to %q", got.mode)
	}
	if n := strings.Count(buf.String(), "cannot be used"); n != 1 {
		t.Errorf("logged %d times, want once: %q", n, buf.String())
	}
}

func TestResetPasswordRefusesAShortOne(t *testing.T) {
	dir := t.TempDir()
	if err := ResetPassword(dir, "short"); err == nil {
		t.Fatal("a 5-character password was accepted")
	}
	if _, ok, _ := readSignin(dir); ok {
		t.Error("a refused reset wrote the sign-in file")
	}
}

// Behind a trusted proxy the client is the address the proxy appended: the rightmost X-Forwarded-For entry.
// Entries before it are the client's own words, so a LAN first hop cannot make an internet client private.
func TestHomeUsesTheProxysAppendedAddress(t *testing.T) {
	a := newAccess(t, true) // a token exists, so home mode is a choice that needs the address to be private
	if err := a.save(signinState{Mode: ModeHome}); err != nil {
		t.Fatal(err)
	}
	srv := peerServer(t, a, "10.0.0.5:1234", "10.0.0.5")
	c := &http.Client{}
	if open := authState(t, srv, c)["open"]; open != false { // the proxy sent no client address: not private
		t.Fatalf("no forwarded address from the proxy: open=%v", open)
	}
	cases := []struct {
		xff  string
		open bool
	}{
		{"192.168.1.20, 203.0.113.9", false}, // the proxy saw a public client; the LAN hop is the client's word
		{"203.0.113.9, 192.168.1.20", true},  // the proxy appended a LAN address last
	}
	for _, k := range cases {
		code, body, _ := do(t, c, srv, call{method: "GET", path: "/api/auth", forwardedFor: k.xff})
		var out map[string]any
		if code != 200 || json.Unmarshal([]byte(body), &out) != nil || out["open"] != k.open {
			t.Errorf("X-Forwarded-For %q: open want %v, got %d %s", k.xff, k.open, code, body)
		}
	}
}

// Six wrong logins through one trusted proxy, each with a different first hop, still lock out: the limiter
// keys on the appended address, which does not change.
func TestRotatingFirstHopsStillLockOut(t *testing.T) {
	a := newAccess(t, false)
	if err := a.save(signinState{Mode: ModePassword}); err != nil {
		t.Fatal(err)
	}
	if err := ResetPassword(a.dir, "correct horse"); err != nil {
		t.Fatal(err)
	}
	srv := peerServer(t, a, "10.0.0.5:1234", "10.0.0.5")
	for i := 0; i < loginMaxFails; i++ {
		k := call{method: "POST", path: "/api/login", body: `{"password":"nope nope nope"}`, ctype: "application/json",
			forwardedFor: fmt.Sprintf("10.9.9.%d, 203.0.113.9", i+1)}
		if code, body, _ := do(t, nil, srv, k); code != 401 || errCode(body) != "bad_password" {
			t.Fatalf("try %d: %d %s", i+1, code, body)
		}
	}
	code, body, _ := do(t, nil, srv, call{method: "POST", path: "/api/login", body: `{"password":"nope nope nope"}`,
		ctype: "application/json", forwardedFor: "10.9.9.99, 203.0.113.9"})
	if code != 429 || errCode(body) != "too_many_tries" {
		t.Errorf("sixth try with a new first hop: %d %s, want 429", code, body)
	}
}

// A request from an untrusted peer that carries a forwarding header is not private, whatever its address:
// a LAN reverse proxy would show the internet's client that way (review P1). Without a header, the LAN peer is.
func TestUntrustedForwardedRequestIsNotPrivate(t *testing.T) {
	a := newAccess(t, true) // a token exists, so home mode admits by address
	if err := a.save(signinState{Mode: ModeHome}); err != nil {
		t.Fatal(err)
	}
	srv := peerServer(t, a, "192.168.1.20:5555")
	c := &http.Client{}
	if open := authState(t, srv, c)["open"]; open != true {
		t.Fatalf("LAN peer without a header: open=%v", open)
	}
	code, body, _ := do(t, c, srv, call{method: "GET", path: "/api/auth", forwardedFor: "192.168.1.7"})
	var out map[string]any
	if code != 200 || json.Unmarshal([]byte(body), &out) != nil || out["open"] != false {
		t.Errorf("forwarded from an untrusted peer: open want false, got %d %s", code, body)
	}
	for _, h := range []string{"X-Real-IP", "Forwarded"} {
		req := httptest.NewRequest("GET", "/api/auth", nil)
		req.RemoteAddr = "192.168.1.20:5555"
		req.Header.Set(h, "203.0.113.9")
		if (Transport{}).PrivateClient(req) {
			t.Errorf("%s from an untrusted peer counts as private", h)
		}
	}
}
