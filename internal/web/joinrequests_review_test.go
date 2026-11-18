// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A reverse proxy on this host forwards the internet's address in X-Forwarded-For. The peer is loopback, which
// is local, but it is not a trusted proxy, so the join is refused rather than listed as this computer.
func TestJoinThroughAProxyOnThisHostIsRefused(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	r := httptest.NewRequest("POST", "https://server.test/api/worker/v1/join", strings.NewReader(`{"name":"Stranger"}`))
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set(workerHeader, "test; api=1")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	r.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	srv.Config.Handler.ServeHTTP(rec, r)
	if rec.Code != 403 || errCode(rec.Body.String()) != "not_local" {
		t.Fatalf("a proxied join from the internet: %d %s", rec.Code, rec.Body.String())
	}
	if rw, _ := st.RemoteWorkers(); len(rw) != 0 {
		t.Errorf("a refused proxied join paired a worker: %+v", rw)
	}
}

// The cap counts only unanswered requests, and a full list of waiting ones refuses the next. Once one of them
// has expired, it is dropped to make room.
func TestJoinCapRefusesWhenFullOfWaitingAndMakesRoomForAnExpiredOne(t *testing.T) {
	_, st, _, _ := libServer(t, false)
	b := newJoinBook(st)
	t0 := time.Now()
	for i := 0; i < maxPendingJoins; i++ {
		if _, _, err := b.add(&joinReq{name: "Office PC"}, t0); err != nil {
			t.Fatalf("join %d: %v", i, err)
		}
	}
	if _, _, err := b.add(&joinReq{name: "Late PC"}, t0.Add(time.Minute)); err != errJoinFull {
		t.Fatalf("a full list of waiting requests: %v", err)
	}
	if _, _, err := b.add(&joinReq{name: "Later PC"}, t0.Add(JoinFor+time.Second)); err != nil {
		t.Fatalf("an expired request makes room: %v", err)
	}
	if n := len(b.pending(t0.Add(JoinFor + time.Second))); n != maxPendingJoins {
		t.Errorf("after dropping one expired request the list has %d, want %d", n, maxPendingJoins)
	}
}

// An expired request shows as expired in the list, so the person can dismiss it.
func TestJoinExpiredShowsInTheList(t *testing.T) {
	old := JoinFor
	JoinFor = time.Millisecond
	defer func() { JoinFor = old }()
	srv, _, _, _ := libServer(t, false)
	id, _ := askJoin(t, srv, "Office PC")
	time.Sleep(5 * time.Millisecond)
	code, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workers"})
	if code != 200 || !strings.Contains(b, id) || !strings.Contains(b, `"state":"expired"`) {
		t.Fatalf("an expired request in the list: %d %s", code, b)
	}
}

// Dismiss takes the request off the list, and the worker's poll then finds nothing.
func TestJoinDismissRemovesIt(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	id, secret := askJoin(t, srv, "Office PC")
	if code, b, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/join-requests/" + id + "/dismiss", ctype: "application/json", body: "{}"}); code != 204 {
		t.Fatalf("dismiss: %d %s", code, b)
	}
	if code, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workers"}); strings.Contains(b, id) {
		t.Errorf("a dismissed request is still listed: %d %s", code, b)
	}
	if code, b := joinFrom(srv, "GET", "/api/worker/v1/join/"+id, "", secret, "192.168.1.20", false); code != 404 || errCode(b) != "join_request_not_found" {
		t.Errorf("poll after dismiss: %d %s", code, b)
	}
}

// The list says whether allowing the request replaces a worker of the same name, and only then.
func TestJoinListSaysItReplacesAWorker(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	code, _, err := st.NewPairing(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Pair(code, "Office PC", "test", "", "", 1, "{}", time.Now()); err != nil {
		t.Fatal(err)
	}
	askJoin(t, srv, "Office PC")
	askJoin(t, srv, "Garage PC")
	_, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workers"})
	if !strings.Contains(b, `"name":"Office PC"`) || !strings.Contains(b, `"replacesWorker":true`) || !strings.Contains(b, `"replacesWorker":false`) {
		t.Errorf("replacesWorker in the list: %s", b)
	}
}

// The summary badge counts open requests, and drops when the person answers one.
func TestJoinBadgeDropsAfterAnswer(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	id, _ := askJoin(t, srv, "Office PC")
	if _, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/summary"}); !strings.Contains(b, `"joinRequestsWaiting":1`) {
		t.Fatalf("badge before the answer: %s", b)
	}
	if code, b, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/join-requests/" + id + "/allow", ctype: "application/json", body: "{}"}); code != 200 {
		t.Fatalf("allow: %d %s", code, b)
	}
	if _, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/summary"}); !strings.Contains(b, `"joinRequestsWaiting":0`) {
		t.Errorf("badge after the answer: %s", b)
	}
}
