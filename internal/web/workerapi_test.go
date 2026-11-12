// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Q1 (a), orch-lead 13:28Z: TLS here, or plain HTTP only from a private address; X-Forwarded-* only
// from a configured trusted proxy (anyone else can write them).
func TestWorkerTransport(t *testing.T) {
	proxy := netip.MustParsePrefix("10.0.0.5/32")
	req := func(peer string, tlsOn bool, xfp, xff string) *http.Request {
		r := httptest.NewRequest("POST", "/api/worker/v1/heartbeat", nil)
		r.RemoteAddr = peer
		if tlsOn {
			r.TLS = &tls.ConnectionState{}
		}
		if xfp != "" {
			r.Header.Set("X-Forwarded-Proto", xfp)
		}
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}
	none, trusted := Transport{}, Transport{TrustedProxies: []netip.Prefix{proxy}}
	for _, c := range []struct {
		name string
		t    Transport
		r    *http.Request
		want bool
	}{
		{"plain HTTP from this network", none, req("192.168.1.20:5000", false, "", ""), true},
		{"plain HTTP from loopback", none, req("127.0.0.1:5000", false, "", ""), true},
		{"plain HTTP from IPv6 ULA", none, req("[fd00::7]:5000", false, "", ""), true},
		{"plain HTTP from outside", none, req("203.0.113.9:5000", false, "", ""), false},
		{"TLS here from outside", none, req("203.0.113.9:5000", true, "", ""), true},
		{"forged https from an untrusted outside peer", none, req("203.0.113.9:5000", false, "https", ""), false},
		{"forged private address from an untrusted outside peer", none, req("203.0.113.9:5000", false, "", "192.168.1.2"), false},
		{"a trusted proxy that terminated https", trusted, req("10.0.0.5:5000", false, "https", "203.0.113.9"), true},
		{"a trusted proxy, plain http from outside", trusted, req("10.0.0.5:5000", false, "http", "203.0.113.9"), false},
		{"a trusted proxy, plain http from this network", trusted, req("10.0.0.5:5000", false, "http", "192.168.1.2"), true},
		{"a private peer that isn't the proxy is judged directly", trusted, req("10.0.0.6:5000", false, "http", "203.0.113.9"), true},
	} {
		if got := c.t.Allowed(c.r); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if p, err := ParseTrustedProxies(" 10.0.0.5, 172.16.0.0/12 "); err != nil || len(p) != 2 || !p[1].Contains(netip.MustParseAddr("172.20.1.1")) {
		t.Errorf("%v %v", p, err)
	}
	if _, err := ParseTrustedProxies("proxy.lan"); err == nil {
		t.Error("a name is not an address")
	}
}

// workerCall calls the worker API as a worker of this version and API level.
func workerCall(t *testing.T, srv *httptest.Server, path, token, body string, level int) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", srv.URL+"/api/worker/v1/"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if level > 0 {
		req.Header.Set(workerHeader, "test; api="+strconv.Itoa(level))
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// V7b1: pairing once, scope both ways, versions, the Workers page, leases with fencing, removal.
func TestRemoteWorkerPairingAndLeases(t *testing.T) {
	srv, st, a, _ := libServer(t, true) // exposed: the person's token is required
	person := a.Token()
	code, body, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/pairings", body: `{}`, ctype: "application/json", bearer: person})
	var p struct{ Code, Command string }
	json.Unmarshal([]byte(body), &p)
	if code != 200 || len(p.Code) != 9 || !strings.Contains(p.Command, "worker --server "+srv.URL+" --pair "+p.Code) {
		t.Fatalf("pairing %d %s", code, body)
	}
	pair := `{"code":"` + strings.ToLower(p.Code) + `","name":"Desktop"}` // typed in lower case: still the code
	if c, b := workerCall(t, srv, "pair", "", pair, 0); c != 426 || errCode(b) != "update_worker" {
		t.Errorf("no version: %d %s", c, b)
	}
	c, b := workerCall(t, srv, "pair", "", pair, WorkerAPILevel)
	var w struct{ WorkerID, Token string }
	json.Unmarshal([]byte(b), &w)
	if c != 200 || w.Token == "" || w.WorkerID == "" {
		t.Fatalf("pair %d %s", c, b)
	}
	if c, b := workerCall(t, srv, "pair", "", pair, WorkerAPILevel); c != 403 || errCode(b) != "pairing_invalid" {
		t.Errorf("a code works once: %d %s", c, b)
	}
	old, _, _ := st.NewPairing(time.Now().Add(-jobs.PairingFor - time.Minute))
	if c, b := workerCall(t, srv, "pair", "", `{"code":"`+old+`"}`, WorkerAPILevel); c != 403 || errCode(b) != "pairing_invalid" {
		t.Errorf("an expired code: %d %s", c, b)
	}
	// scope: the person's token opens nothing here, the worker's nothing else
	if c, b := workerCall(t, srv, "heartbeat", person, `{}`, WorkerAPILevel); c != 403 || errCode(b) != "worker_token" {
		t.Errorf("the person's token on the worker API: %d %s", c, b)
	}
	if c, _, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workers", bearer: w.Token}); c != 401 {
		t.Errorf("a worker token on /api/v2: %d", c)
	}
	if c, b := workerCall(t, srv, "heartbeat", w.Token, `{"freeSlots":1}`, WorkerAPILevel); c != 200 {
		t.Errorf("heartbeat %d %s", c, b)
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/workers", bearer: person})
	if !strings.Contains(body, `"id":"`+w.WorkerID+`"`) || !strings.Contains(body, `"kind":"remote"`) || !strings.Contains(body, `"total":2`) {
		t.Errorf("the Workers page: %s", body)
	}
	// a lease, its progress, giving it back: then the lease is gone (409)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv"), SizeBefore: 1000})
	c, b = workerCall(t, srv, "lease", w.Token, `{"slots":1}`, WorkerAPILevel)
	var l struct {
		Lease struct {
			ID  string
			Job struct{ ID, Name string }
		}
	}
	json.Unmarshal([]byte(b), &l)
	if c != 200 || l.Lease.ID == "" || l.Lease.Job.ID != strconv.FormatInt(id, 10) || l.Lease.Job.Name != "a.mkv" {
		t.Fatalf("lease %d %s", c, b)
	}
	if j, _ := st.Get(id); j.Status != jobs.Running || j.Worker != w.WorkerID {
		t.Errorf("leased job %+v", j)
	}
	if c, b := workerCall(t, srv, "leases/"+l.Lease.ID+"/progress", w.Token, `{"percent":12}`, WorkerAPILevel); c != 200 {
		t.Errorf("progress %d %s", c, b)
	}
	if c, b := workerCall(t, srv, "leases/"+l.Lease.ID+"/release", w.Token, `{}`, WorkerAPILevel); c != 200 {
		t.Errorf("release %d %s", c, b)
	}
	if c, b := workerCall(t, srv, "leases/"+l.Lease.ID+"/progress", w.Token, `{"percent":50}`, WorkerAPILevel); c != 409 || errCode(b) != "lease_gone" {
		t.Errorf("a stale lease: %d %s", c, b)
	}
	if j, _ := st.Get(id); j.Status != jobs.Queued {
		t.Errorf("given back: %s", j.Status)
	}
	// removed while it runs a job: its token stops working, the job goes back to the queue
	workerCall(t, srv, "lease", w.Token, `{"slots":1}`, WorkerAPILevel)
	if c, b, _ := do(t, nil, srv, call{method: "DELETE", path: "/api/v2/workers/" + w.WorkerID, ctype: "application/json", bearer: person}); c != 200 {
		t.Fatalf("remove %d %s", c, b)
	}
	if c, b := workerCall(t, srv, "heartbeat", w.Token, `{}`, WorkerAPILevel); c != 403 || errCode(b) != "worker_token" {
		t.Errorf("a removed worker: %d %s", c, b)
	}
	if j, _ := st.Get(id); j.Status != jobs.Queued {
		t.Errorf("its job: %s", j.Status)
	}
}

// The worker API itself refuses a public peer on plain HTTP, also one that forges X-Forwarded-Proto.
func TestWorkerAPIRefusesPlainHTTPFromOutside(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := workerAPI(st, Transport{}, "test", Engine{DataDir: t.TempDir()})
	for _, xfp := range []string{"", "https"} {
		r := httptest.NewRequest("POST", "/api/worker/v1/pair", strings.NewReader(`{"code":"x"}`))
		r.RemoteAddr = "203.0.113.9:4000"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(workerHeader, "test; api=1")
		if xfp != "" {
			r.Header.Set("X-Forwarded-Proto", xfp)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != 403 || errCode(rec.Body.String()) != "https_required" {
			t.Errorf("X-Forwarded-Proto %q: %d %s", xfp, rec.Code, rec.Body.String())
		}
	}
}

// orch-lead 13:41Z: wrong pairing codes wait, and an address that sends PairMaxFails of them within the
// window gets 429; another address still pairs, and the wait holds up no one else.
func TestPairingTriesAreLimited(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	delay := PairFailDelay
	defer func() { PairFailDelay = delay }()
	h := workerAPI(st, Transport{}, "test", Engine{DataDir: t.TempDir()})
	pairFrom := func(peer, code string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/worker/v1/pair", strings.NewReader(`{"code":"`+code+`"}`))
		r.RemoteAddr = peer + ":4000"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(workerHeader, "test; api=1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	// the wait holds no lock: a good code from another address answers at once meanwhile
	PairFailDelay = time.Second // well above the bound below, also on a busy machine
	good, _, _ := st.NewPairing(time.Now())
	slow := make(chan int)
	go func() { slow <- pairFrom("192.168.1.66", "WRONG-CODE").Code }()
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	if rec := pairFrom("192.168.1.20", good); rec.Code != 200 || time.Since(start) > 600*time.Millisecond {
		t.Errorf("good code while another waits: %d after %s", rec.Code, time.Since(start))
	}
	if c := <-slow; c != 403 {
		t.Errorf("wrong code: %d", c)
	}
	// PairMaxFails wrong codes from one address: then 429, even with a good code
	PairFailDelay = 0
	for i := 1; i < PairMaxFails; i++ { // one failure was above
		if rec := pairFrom("192.168.1.66", "WRONG-CODE"); rec.Code != 403 {
			t.Fatalf("try %d: %d", i, rec.Code)
		}
	}
	good2, _, _ := st.NewPairing(time.Now())
	if rec := pairFrom("192.168.1.66", good2); rec.Code != 429 || errCode(rec.Body.String()) != "too_many_tries" || rec.Header().Get("Retry-After") == "" {
		t.Errorf("after %d failures: %d %s", PairMaxFails, rec.Code, rec.Body.String())
	}
	if rec := pairFrom("192.168.1.21", good2); rec.Code != 200 {
		t.Errorf("another address: %d %s", rec.Code, rec.Body.String())
	}
}

// User review 11-12 item 2 (orch-lead 14:22Z): each worker on and off by itself; the switch for all
// sets them all; the Dashboard's processingPaused is "every worker off"; stopping a remote worker ends
// its lease.
func TestEachWorkerOnAndOff(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	code, _, _ := st.NewPairing(time.Now())
	rw, _, err := st.Pair(code, "Desktop", "1", 1, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	post := func(path string) (int, string) {
		c, b, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/" + path, body: `{}`, ctype: "application/json"})
		return c, b
	}
	summaryPaused := func() bool {
		_, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/summary"})
		return strings.Contains(b, `"processingPaused":true`)
	}
	if c, b := post("local/stop"); c != 200 || !strings.Contains(b, `"enabled":false`) || st.WorkerEnabled(jobs.LocalWorker) || !st.WorkerEnabled(rw.ID) {
		t.Errorf("local stop: %d %s", c, b)
	}
	if summaryPaused() {
		t.Error("a worker is on: the switch for all reads on")
	}
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv")})
	if j, err := st.Claim(rw.ID); err != nil || j.ID != id {
		t.Fatalf("claim %v", err)
	}
	if c, b := post(rw.ID + "/drain"); c != 200 || !strings.Contains(b, `"status":"draining"`) || st.WorkerEnabled(rw.ID) {
		t.Errorf("remote drain: %d %s", c, b)
	}
	if !summaryPaused() {
		t.Error("every worker off: paused")
	}
	if c, b := post(rw.ID + "/stop"); c != 200 || !strings.Contains(b, `"currentJobIds":[]`) {
		t.Errorf("remote stop: %d %s", c, b)
	}
	if j, _ := st.Get(id); j.Status != jobs.Queued {
		t.Errorf("its job: %s", j.Status)
	}
	if c, b := post(rw.ID + "/start"); c != 200 || !st.WorkerEnabled(rw.ID) || st.WorkerEnabled(jobs.LocalWorker) {
		t.Errorf("remote start starts only it: %d %s", c, b)
	}
	if c, b := post("all/start"); c != 200 || !st.WorkerEnabled(jobs.LocalWorker) || !st.WorkerEnabled(rw.ID) || strings.Count(b, `"enabled":true`) != 2 {
		t.Errorf("all on: %d %s", c, b)
	}
	if c, _ := post("w-nobody/stop"); c != 404 {
		t.Errorf("no such worker: %d", c)
	}
}
