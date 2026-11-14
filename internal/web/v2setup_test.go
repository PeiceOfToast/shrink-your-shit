// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/discover"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/remote"
)

// setupServer is a web server with its own data folder and web port 8484, as a worker's join reports it.
// TestMain sets the join poll once for the whole package: a join wait can outlive its test, so the knob is never
// changed per test.
func TestMain(m *testing.M) {
	remote.JoinEvery = time.Millisecond
	os.Exit(m.Run())
}

func setupServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a, _, _ := OpenAccess(t.TempDir(), false)
	dir := t.TempDir()
	srv := httptest.NewServer(Handler(st, a, Engine{DataDir: dir, Version: "1.2.3", Port: 8484, Joiner: &remote.Joiner{Version: "1.2.3"}}))
	t.Cleanup(srv.Close)
	return srv, dir
}

// fakeRemote is a server that a worker can ask to join: each join poll gets the next of states (the last repeats;
// "allowed" carries a token). It answers pairing code "good" and refuses any other. joinBody hears the join body.
func fakeRemote(t *testing.T, states ...string) (*httptest.Server, chan string) {
	t.Helper()
	joinBody := make(chan string, 1)
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/worker/v1/join":
			b, _ := io.ReadAll(r.Body)
			select {
			case joinBody <- string(b):
			default:
			}
			if len(states) == 1 && states[0] == "not_local" {
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"error":{"code":"not_local","message":"Only this computer can ask."}}`))
				return
			}
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"id":"j-1","expiresAt":"2099-01-01T00:00:00Z","secret":"s3cret"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/worker/v1/join/j-1":
			i := min(int(polls.Add(1))-1, len(states)-1)
			if states[i] == "allowed" {
				w.Write([]byte(`{"state":"allowed","workerId":"w-9","name":"Office PC","token":"tok"}`))
				return
			}
			w.Write([]byte(`{"state":"` + states[i] + `"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/worker/v1/pair":
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), `"code":"good"`) {
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"error":{"code":"pairing_invalid","message":"That code isn't valid."}}`))
				return
			}
			w.Write([]byte(`{"workerId":"w-1","name":"Office PC","token":"tok"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, joinBody
}

// setupState reads GET /api/v2/setup as a map.
func setupState(t *testing.T, c *http.Client, srv *httptest.Server) map[string]any {
	t.Helper()
	code, body, _ := do(t, c, srv, call{method: "GET", path: "/api/v2/setup"})
	if code != http.StatusOK {
		t.Fatalf("GET setup: %d %s", code, body)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// waitFor polls the setup page until ok says so, or fails the test.
func waitFor(t *testing.T, c *http.Client, srv *httptest.Server, what string, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		v := setupState(t, c, srv)
		if ok(v) {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s: setup is %v", what, v)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// joinState is the page's join state ("" when there is no join).
func joinState(v map[string]any) string {
	j, _ := v["join"].(map[string]any)
	s, _ := j["state"].(string)
	return s
}

func TestSetupAskWaitsThenAllowedMakesTheWorkerRole(t *testing.T) {
	srv, dir := setupServer(t)
	remoteSrv, joinBody := fakeRemote(t, "waiting", "waiting", "allowed")
	c := jar(t)

	code, body, _ := do(t, c, srv, call{method: "POST", path: "/api/v2/setup/role", ctype: "application/json",
		body: `{"role":"worker","serverUrl":"` + remoteSrv.URL + `"}`})
	if code != http.StatusAccepted {
		t.Fatalf("ask: %d %s", code, body)
	}
	var v map[string]any
	json.Unmarshal([]byte(body), &v)
	if v["role"] != "unset" || joinState(v) != "waiting" {
		t.Errorf("answer while waiting: %s", body)
	}
	if !strings.Contains(<-joinBody, `"port":8484`) {
		t.Error("the join request does not name the web port")
	}

	waitFor(t, c, srv, "the worker role", func(v map[string]any) bool { return v["role"] == "worker" })
	if got := setupState(t, c, srv); joinState(got) != "allowed" {
		t.Errorf("after allowed: %v", got)
	}
	st, err := remote.Load(filepath.Join(dir, "worker"))
	if err != nil || st.Token != "tok" || st.Server != remoteSrv.URL {
		t.Errorf("worker state saved: %+v, %v", st, err)
	}
}

func TestSetupDeniedKeepsTheRoleUnset(t *testing.T) {
	srv, _ := setupServer(t)
	remoteSrv, _ := fakeRemote(t, "waiting", "denied")
	c := jar(t)
	do(t, c, srv, call{method: "POST", path: "/api/v2/setup/role", ctype: "application/json", body: `{"role":"worker","serverUrl":"` + remoteSrv.URL + `"}`})
	v := waitFor(t, c, srv, "denied", func(v map[string]any) bool { return joinState(v) == "denied" })
	if v["role"] != "unset" {
		t.Errorf("a denied join changed the role: %v", v)
	}
}

func TestSetupUnsetCancelsAWaitingJoin(t *testing.T) {
	srv, _ := setupServer(t)
	remoteSrv, _ := fakeRemote(t, "waiting")
	c := jar(t)
	do(t, c, srv, call{method: "POST", path: "/api/v2/setup/role", ctype: "application/json", body: `{"role":"worker","serverUrl":"` + remoteSrv.URL + `"}`})
	code, body, _ := do(t, c, srv, call{method: "POST", path: "/api/v2/setup/role", ctype: "application/json", body: `{"role":"unset"}`})
	if code != http.StatusOK || !strings.Contains(body, `"join":null`) {
		t.Errorf("cancel: %d %s", code, body)
	}
}

func TestSetupRefusesAnUnreachableServer(t *testing.T) {
	srv, _ := setupServer(t)
	c := jar(t)
	code, body, _ := do(t, c, srv, call{method: "POST", path: "/api/v2/setup/role", ctype: "application/json", body: `{"role":"worker","serverUrl":"http://127.0.0.1:1"}`})
	if code != http.StatusBadGateway || errCode(body) != "server_unreachable" {
		t.Errorf("unreachable: %d %s", code, body)
	}
	if v := setupState(t, c, srv); v["role"] != "unset" || v["join"] != nil {
		t.Errorf("nothing should change: %v", v)
	}
}

func TestSetupRefusesAddressThatIsNotAServer(t *testing.T) {
	srv, _ := setupServer(t)
	plain := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(plain.Close)
	c := jar(t)
	code, body, _ := do(t, c, srv, call{method: "POST", path: "/api/v2/setup/role", ctype: "application/json", body: `{"role":"worker","serverUrl":"` + plain.URL + `"}`})
	if code != http.StatusBadGateway || errCode(body) != "not_a_server" {
		t.Errorf("not a server: %d %s", code, body)
	}
}

func TestSetupRejectsABadServerAddress(t *testing.T) {
	srv, _ := setupServer(t)
	c := jar(t)
	code, body, _ := do(t, c, srv, call{method: "POST", path: "/api/v2/setup/role", ctype: "application/json", body: `{"role":"worker","serverUrl":"nas:8484"}`})
	if code != http.StatusBadRequest || errCode(body) != "invalid_server_url" {
		t.Errorf("bad address: %d %s", code, body)
	}
}

func TestSetupPairWithAGoodCodeIsTheWorkerRole(t *testing.T) {
	srv, dir := setupServer(t)
	remoteSrv, _ := fakeRemote(t)
	c := jar(t)
	code, body, _ := do(t, c, srv, call{method: "POST", path: "/api/v2/setup/role", ctype: "application/json",
		body: `{"role":"worker","serverUrl":"` + remoteSrv.URL + `","pairCode":"good"}`})
	if code != http.StatusOK || !strings.Contains(body, `"role":"worker"`) || !strings.Contains(body, `"state":"allowed"`) {
		t.Fatalf("pair: %d %s", code, body)
	}
	if st, err := remote.Load(filepath.Join(dir, "worker")); err != nil || st.Token != "tok" {
		t.Errorf("paired state saved: %+v, %v", st, err)
	}
}

func TestSetupPairWithABadCodeSaysSo(t *testing.T) {
	srv, _ := setupServer(t)
	remoteSrv, _ := fakeRemote(t)
	c := jar(t)
	code, body, _ := do(t, c, srv, call{method: "POST", path: "/api/v2/setup/role", ctype: "application/json",
		body: `{"role":"worker","serverUrl":"` + remoteSrv.URL + `","pairCode":"nope"}`})
	if code != http.StatusBadRequest || errCode(body) != "pair_code_invalid" {
		t.Errorf("bad code: %d %s", code, body)
	}
	if v := setupState(t, c, srv); v["role"] != "unset" {
		t.Errorf("a bad code changed the role: %v", v)
	}
}

func TestSetupNotLocalLeavesTheRoleAlone(t *testing.T) {
	srv, _ := setupServer(t)
	remoteSrv, _ := fakeRemote(t, "not_local")
	c := jar(t)
	code, body, _ := do(t, c, srv, call{method: "POST", path: "/api/v2/setup/role", ctype: "application/json", body: `{"role":"worker","serverUrl":"` + remoteSrv.URL + `"}`})
	if code != http.StatusOK || !strings.Contains(body, `"state":"not_local"`) || !strings.Contains(body, `"role":"unset"`) {
		t.Errorf("not local: %d %s", code, body)
	}
}

// An install that is already a server can't ask to join another server: 409, and no join request leaves it, so a
// request from elsewhere on the network can't change the role.
func TestSetupEnrolledInstallRefusesTheWorkerRole(t *testing.T) {
	srv, _ := setupServer(t)
	remoteSrv, joinBody := fakeRemote(t, "waiting")
	c := jar(t)
	if code, body, _ := do(t, c, srv, call{method: "POST", path: "/api/v2/setup/role", ctype: "application/json", body: `{"role":"server"}`}); code != http.StatusOK {
		t.Fatalf("set server: %d %s", code, body)
	}
	code, body, _ := do(t, c, srv, call{method: "POST", path: "/api/v2/setup/role", ctype: "application/json", body: `{"role":"worker","serverUrl":"` + remoteSrv.URL + `"}`})
	if code != http.StatusConflict || errCode(body) != "role_already_set" {
		t.Fatalf("worker on a server: %d %s", code, body)
	}
	select {
	case b := <-joinBody:
		t.Errorf("a join request was sent: %s", b)
	default:
	}
	if v := setupState(t, c, srv); v["role"] != "server" || v["join"] != nil {
		t.Errorf("role or join changed: %v", v)
	}
}

// The server allowed the join, but the token can't be saved: the state says so, not "expired", and the role stays unset.
func TestSetupAllowedButTokenNotSavedSaysSo(t *testing.T) {
	srv, dir := setupServer(t)
	// A file where the worker's folder goes makes the token save fail.
	if err := os.WriteFile(filepath.Join(dir, "worker"), []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	remoteSrv, _ := fakeRemote(t, "waiting", "allowed")
	c := jar(t)
	do(t, c, srv, call{method: "POST", path: "/api/v2/setup/role", ctype: "application/json", body: `{"role":"worker","serverUrl":"` + remoteSrv.URL + `"}`})
	v := waitFor(t, c, srv, "not_saved", func(v map[string]any) bool { return joinState(v) == "not_saved" })
	if v["role"] != "unset" {
		t.Errorf("a failed token save changed the role: %v", v)
	}
}

// An allowed join tells the app the role is now worker, so its worker loop starts without a restart.
func TestSetupAllowedTellsTheAppTheRoleChanged(t *testing.T) {
	dir := t.TempDir()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a, _, _ := OpenAccess(t.TempDir(), false)
	var mu sync.Mutex
	var roles []string
	srv := httptest.NewServer(Handler(st, a, Engine{DataDir: dir, Version: "1.2.3", Port: 8484, Joiner: &remote.Joiner{Version: "1.2.3"},
		RoleChanged: func(role string) { mu.Lock(); roles = append(roles, role); mu.Unlock() }}))
	t.Cleanup(srv.Close)

	remoteSrv, _ := fakeRemote(t, "waiting", "allowed")
	c := jar(t)
	do(t, c, srv, call{method: "POST", path: "/api/v2/setup/role", ctype: "application/json", body: `{"role":"worker","serverUrl":"` + remoteSrv.URL + `"}`})
	waitFor(t, c, srv, "the worker role", func(v map[string]any) bool { return v["role"] == "worker" })
	mu.Lock()
	defer mu.Unlock()
	if len(roles) != 1 || roles[0] != "worker" {
		t.Errorf("role changes told to the app: %v, want [worker]", roles)
	}
}

// fakeFinder answers the server list a browse would find.
type fakeFinder []discover.Found

func (f fakeFinder) Servers() []discover.Found { return f }

// The servers route answers the browse's list. It sits on the same mux as the other setup routes, so it has their sign-in.
func TestSetupServersListsWhatAnswered(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a, _, _ := OpenAccess(t.TempDir(), false)
	found := fakeFinder{
		{Name: "Office", Address: "192.168.1.20", Port: 8484, Version: "1.4.0"},
		{Name: "Den", Address: "fe80::1", Port: 8484, Version: "1.2.3"}, // this install runs 1.2.3
	}
	srv := httptest.NewServer(Handler(st, a, Engine{DataDir: t.TempDir(), Version: "1.2.3", Port: 8484, Finder: found}))
	t.Cleanup(srv.Close)

	c := jar(t)
	code, body, _ := do(t, c, srv, call{method: "GET", path: "/api/v2/setup/servers"})
	if code != http.StatusOK {
		t.Fatalf("GET servers: %d %s", code, body)
	}
	want := `{"items":[{"name":"Office","address":"192.168.1.20","url":"http://192.168.1.20:8484","version":"1.4.0","matchesVersion":false},{"name":"Den","address":"fe80::1","url":"http://[fe80::1]:8484","version":"1.2.3","matchesVersion":true}]}`
	if strings.TrimSpace(body) != want {
		t.Errorf("body %s, want %s", strings.TrimSpace(body), want)
	}
}

func TestSetupServersIsEmptyWhenNothingAnswers(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a, _, _ := OpenAccess(t.TempDir(), false)
	srv := httptest.NewServer(Handler(st, a, Engine{DataDir: t.TempDir(), Version: "1.2.3", Port: 8484, Finder: fakeFinder(nil)}))
	t.Cleanup(srv.Close)

	c := jar(t)
	code, body, _ := do(t, c, srv, call{method: "GET", path: "/api/v2/setup/servers"})
	if code != http.StatusOK || strings.TrimSpace(body) != `{"items":[]}` {
		t.Fatalf("GET servers: %d %s, want an empty list", code, body)
	}
}
