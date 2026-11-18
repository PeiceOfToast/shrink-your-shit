// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/discover"
	"github.com/shrink-your-shit/shrink-your-shit/internal/remote"
)

// namedRemote is a server that names itself name in its join and pair replies, and keeps a join waiting.
func namedRemote(t *testing.T, name string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/worker/v1/join":
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"id":"j-1","expiresAt":"2099-01-01T00:00:00Z","secret":"s3cret","serverName":"` + name + `"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/worker/v1/join/j-1":
			w.Write([]byte(`{"state":"waiting"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/worker/v1/pair":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"code":"good"`) {
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"error":{"code":"pairing_invalid","message":"That code isn't valid."}}`))
				return
			}
			w.Write([]byte(`{"workerId":"w-1","name":"Office PC","token":"tok","serverName":"` + name + `"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The server's own replies to a join and to a pair carry the name it announces on the network.
func TestServerRepliesCarryItsAnnouncedName(t *testing.T) {
	srv, st, _, _ := libServer(t, true)

	code, b := joinFrom(srv, "POST", "/api/worker/v1/join", `{"name":"Office PC","os":"linux","capabilities":{}}`, "", "192.168.1.20", false)
	var join struct{ ServerName string }
	json.Unmarshal([]byte(b), &join)
	if code != http.StatusAccepted || join.ServerName != discover.ServerName() || join.ServerName == "" {
		t.Errorf("join reply: %d %s, want serverName %q", code, b, discover.ServerName())
	}

	pairCode, _, err := st.NewPairing(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c, pb := workerCall(t, srv, "pair", "", `{"code":"`+pairCode+`","name":"Desktop"}`, WorkerAPILevel)
	var pair struct{ ServerName string }
	json.Unmarshal([]byte(pb), &pair)
	if c != http.StatusOK || pair.ServerName != discover.ServerName() {
		t.Errorf("pair reply: %d %s, want serverName %q", c, pb, discover.ServerName())
	}
}

// A join to a typed address shows the server's own name, not the address, from the join reply.
func TestSetupJoinToATypedAddressShowsTheServersName(t *testing.T) {
	remoteSrv := namedRemote(t, "Den PC")
	srv, _ := setupServer(t)
	c := jar(t)
	if code, body, _ := do(t, c, srv, call{method: "POST", path: "/api/v2/setup/role", ctype: "application/json",
		body: `{"role":"worker","serverUrl":"` + remoteSrv.URL + `"}`}); code != http.StatusAccepted {
		t.Fatalf("ask: %d %s", code, body)
	}
	j, _ := setupState(t, c, srv)["join"].(map[string]any)
	if j["serverName"] != "Den PC" {
		t.Errorf("join %v, want the server's name Den PC, not its address", j)
	}
}

// A pair to a typed address shows the server's name from the pair reply, and saves it.
func TestSetupPairToATypedAddressShowsTheServersName(t *testing.T) {
	remoteSrv := namedRemote(t, "Den PC")
	srv, dir := setupServer(t)
	c := jar(t)
	if code, body, _ := do(t, c, srv, call{method: "POST", path: "/api/v2/setup/role", ctype: "application/json",
		body: `{"role":"worker","serverUrl":"` + remoteSrv.URL + `","pairCode":"good"}`}); code != http.StatusOK {
		t.Fatalf("pair: %d %s", code, body)
	}
	j, _ := setupState(t, c, srv)["join"].(map[string]any)
	if j["serverName"] != "Den PC" {
		t.Errorf("join %v, want the server's name Den PC, not its address", j)
	}
	if st, err := remote.Load(filepath.Join(dir, "worker")); err != nil || st.ServerName != "Den PC" {
		t.Errorf("saved server name: %+v, %v; want Den PC", st, err)
	}
}

// An older server whose join reply says no name: the join shows the address, as before.
func TestSetupJoinToAnOlderServerShowsItsAddress(t *testing.T) {
	remoteSrv, _ := fakeRemote(t, "waiting")
	srv, _ := setupServer(t)
	c := jar(t)
	if code, body, _ := do(t, c, srv, call{method: "POST", path: "/api/v2/setup/role", ctype: "application/json",
		body: `{"role":"worker","serverUrl":"` + remoteSrv.URL + `"}`}); code != http.StatusAccepted {
		t.Fatalf("ask: %d %s", code, body)
	}
	j, _ := setupState(t, c, srv)["join"].(map[string]any)
	if j["serverName"] != mustHost(t, remoteSrv.URL) {
		t.Errorf("join %v, want the address %q", j, mustHost(t, remoteSrv.URL))
	}
}
