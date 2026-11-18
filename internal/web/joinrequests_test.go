// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// joinFrom sends a worker's request from a peer address. tls makes it an https request, as a proxy or TLS
// client would show it to the server.
func joinFrom(srv *httptest.Server, method, path, body, bearerTok, peer string, tls bool) (int, string) {
	url := "http://server.test" + path
	if tls {
		url = "https://server.test" + path
	}
	r := httptest.NewRequest(method, url, strings.NewReader(body))
	r.RemoteAddr = peer + ":5000"
	r.Header.Set(workerHeader, "test; api=1")
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if bearerTok != "" {
		r.Header.Set("Authorization", "Bearer "+bearerTok)
	}
	rec := httptest.NewRecorder()
	srv.Config.Handler.ServeHTTP(rec, r)
	return rec.Code, rec.Body.String()
}

// askJoin asks to join from the server's own network and returns the id and the secret.
func askJoin(t *testing.T, srv *httptest.Server, name string) (id, secret string) {
	t.Helper()
	code, b := joinFrom(srv, "POST", "/api/worker/v1/join", `{"name":"`+name+`","os":"linux","capabilities":{}}`, "", "192.168.1.20", false)
	if code != 202 {
		t.Fatalf("join: %d %s", code, b)
	}
	var got struct{ ID, Secret string }
	if err := json.Unmarshal([]byte(b), &got); err != nil {
		t.Fatal(err)
	}
	return got.ID, got.Secret
}

func TestJoinFromTheLocalNetworkIsAllowed(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	if err := st.SetWorkerEnabled(jobs.LocalWorker, false); err != nil { // every worker off: a new worker would arrive off
		t.Fatal(err)
	}
	id, secret := askJoin(t, srv, "Office PC")

	code, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workers"})
	if code != 200 || !strings.Contains(b, `"joinRequests"`) || !strings.Contains(b, id) || !strings.Contains(b, `"state":"waiting"`) {
		t.Fatalf("the person sees the request: %d %s", code, b)
	}
	if !strings.Contains(b, `"url":"http://192.168.1.20:8484"`) {
		t.Errorf("the request names where the worker answers: %s", b)
	}
	if code, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/summary"}); !strings.Contains(b, `"joinRequestsWaiting":1`) {
		t.Errorf("summary badge: %d %s", code, b)
	}

	if code, b, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/join-requests/" + id + "/allow", ctype: "application/json", body: "{}"}); code != 200 || !strings.Contains(b, "Office PC") {
		t.Fatalf("allow: %d %s", code, b)
	}
	code, b = joinFrom(srv, "GET", "/api/worker/v1/join/"+id, "", secret, "192.168.1.20", false)
	if code != 200 || !strings.Contains(b, `"state":"allowed"`) || !strings.Contains(b, `"token"`) {
		t.Fatalf("poll after allow: %d %s", code, b)
	}
	var got struct{ Token string }
	json.Unmarshal([]byte(b), &got)
	wk, err := st.WorkerByToken(got.Token)
	if err != nil {
		t.Fatalf("the credential pairs the worker: %v", err)
	}
	// Allowing is the go-ahead: the worker starts on, with no second step to press start.
	if !st.WorkerEnabled(wk.ID) {
		t.Errorf("an allowed join's worker starts disabled")
	}
}

func TestJoinFromOutsideIsRefused(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	code, b := joinFrom(srv, "POST", "/api/worker/v1/join", `{"name":"Stranger"}`, "", "203.0.113.9", true)
	if code != 403 || errCode(b) != "not_local" {
		t.Fatalf("an outside TLS peer: %d %s", code, b)
	}
	if rw, _ := st.RemoteWorkers(); len(rw) != 0 {
		t.Errorf("a refused join paired a worker: %+v", rw)
	}
	if code, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workers"}); strings.Contains(b, "Stranger") {
		t.Errorf("a refused join shows on the list: %d %s", code, b)
	}
}

func TestJoinWrongSecretLearnsNothing(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	id, _ := askJoin(t, srv, "Office PC")
	code, b := joinFrom(srv, "GET", "/api/worker/v1/join/"+id, "", "guess", "192.168.1.20", false)
	if code != 404 || errCode(b) != "join_request_not_found" {
		t.Fatalf("a wrong secret: %d %s", code, b)
	}
}

func TestJoinDenyTellsTheWorker(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	id, secret := askJoin(t, srv, "Office PC")
	if code, b, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/join-requests/" + id + "/deny", ctype: "application/json", body: "{}"}); code != 204 {
		t.Fatalf("deny: %d %s", code, b)
	}
	code, b := joinFrom(srv, "GET", "/api/worker/v1/join/"+id, "", secret, "192.168.1.20", false)
	if code != 200 || !strings.Contains(b, `"state":"denied"`) || strings.Contains(b, `"token"`) {
		t.Fatalf("poll after deny: %d %s", code, b)
	}
	if code, b, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/join-requests/" + id + "/allow", ctype: "application/json", body: "{}"}); code != 409 || errCode(b) != "join_request_answered" {
		t.Errorf("allow after deny: %d %s", code, b)
	}
}

func TestJoinAnsweredTwiceIsRefused(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	id, _ := askJoin(t, srv, "Office PC")
	if code, b, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/join-requests/" + id + "/allow", ctype: "application/json", body: "{}"}); code != 200 {
		t.Fatalf("first allow: %d %s", code, b)
	}
	if code, b, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/join-requests/" + id + "/allow", ctype: "application/json", body: "{}"}); code != 409 || errCode(b) != "join_request_answered" {
		t.Errorf("second allow: %d %s", code, b)
	}
	if code, b, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/join-requests/j-nope/allow", ctype: "application/json", body: "{}"}); code != 404 || errCode(b) != "join_request_not_found" {
		t.Errorf("unknown id: %d %s", code, b)
	}
}

func TestJoinAllowAfterTheWindowIsRefused(t *testing.T) {
	old := JoinFor
	JoinFor = time.Millisecond
	defer func() { JoinFor = old }()
	srv, _, _, _ := libServer(t, false)
	id, _ := askJoin(t, srv, "Office PC")
	time.Sleep(5 * time.Millisecond)
	if code, b, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/join-requests/" + id + "/allow", ctype: "application/json", body: "{}"}); code != 409 || errCode(b) != "join_request_expired" {
		t.Errorf("allow after the window: %d %s", code, b)
	}
}

func TestJoinReplacesAWorkerOfTheSameName(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	code, _, err := st.NewPairing(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := st.Pair(code, "Office PC", "test", "", "", 1, "{}", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := askJoin(t, srv, "Office PC")
	if code, b, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/join-requests/" + id + "/allow", ctype: "application/json", body: "{}"}); code != 200 {
		t.Fatalf("allow: %d %s", code, b)
	}
	rw, err := st.RemoteWorkers()
	if err != nil {
		t.Fatal(err)
	}
	if len(rw) != 1 || rw[0].ID == old.ID {
		t.Errorf("the same-name worker is replaced, not kept beside: %+v (old %s)", rw, old.ID)
	}
}

func TestJoinNeedsAName(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	code, b := joinFrom(srv, "POST", "/api/worker/v1/join", `{"name":"  "}`, "", "192.168.1.20", false)
	if code != 400 || errCode(b) != "invalid_join" {
		t.Errorf("empty name: %d %s", code, b)
	}
}
