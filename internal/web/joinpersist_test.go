// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/preview"
)

// serverAt is a server on the database at path, made as libServer makes one. A second call on the same path is the
// server after a restart.
func serverAt(t *testing.T, path string) (*httptest.Server, *jobs.Store) {
	t.Helper()
	st, err := jobs.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.SetPaused(false)
	t.Cleanup(func() { st.Close() })
	a, _, _ := OpenAccess(t.TempDir(), false)
	a.FailDelay = 0
	fe := &fakeEngine{}
	next := time.Date(2026, 11, 7, 3, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(Handler(st, a, Engine{
		ScanNow: func() { fe.scans++ }, NextScan: func() time.Time { return next }, RequeueJob: func(id int64) { fe.stopped = append(fe.stopped, id); st.Release(id) },
		Preview: &preview.Previewer{Store: st, Exec: h264Probe, FFprobe: "ffprobe", Probes: 1}}))
	t.Cleanup(srv.Close)
	return srv, st
}

// An allowed join survives a restart: the person allows it, the server restarts before the worker polls, and the
// poll still gets a token that works. The token is given out once.
func TestJoinAllowedSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	srv, st := serverAt(t, path)
	id, secret := askJoin(t, srv, "Office PC")
	if c, b, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/join-requests/" + id + "/allow", ctype: "application/json", body: "{}"}); c != 200 {
		t.Fatalf("allow: %d %s", c, b)
	}
	srv.Close()
	st.Close()
	srv, st = serverAt(t, path)

	code, b := joinFrom(srv, "GET", "/api/worker/v1/join/"+id, "", secret, "192.168.1.20", false)
	if code != 200 || !strings.Contains(b, `"state":"allowed"`) {
		t.Fatalf("poll after the restart: %d %s", code, b)
	}
	var got struct {
		WorkerID string `json:"workerId"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal([]byte(b), &got); err != nil || got.Token == "" {
		t.Fatalf("no token in %s: %v", b, err)
	}
	wk, err := st.WorkerByToken(got.Token)
	if err != nil || wk.ID != got.WorkerID {
		t.Fatalf("the token does not pair the worker %s: %v", got.WorkerID, err)
	}
	if !st.WorkerEnabled(wk.ID) {
		t.Errorf("an allowed join's worker starts disabled")
	}
	if code, b := joinFrom(srv, "GET", "/api/worker/v1/join/"+id, "", secret, "192.168.1.20", false); code != 404 || strings.Contains(b, `"token"`) {
		t.Errorf("a second poll after the token was given out: %d %s", code, b)
	}
}

// An allow nobody picks up leaves no worker behind once its window has passed.
func TestJoinAllowedNeverPickedUpLeavesNoWorker(t *testing.T) {
	_, st, _, _ := libServer(t, false)
	b := newJoinBook(st)
	t0 := time.Now()
	id, _, err := b.add(&joinReq{name: "Office PC", version: "test", os: "linux", capabilities: "{}"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.allow(id, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if rw, _ := st.RemoteWorkers(); len(rw) != 1 {
		t.Fatalf("the allow paired %d workers, want 1", len(rw))
	}
	b.pending(t0.Add(JoinFor + time.Minute)) // reading the list after the window prunes the request
	if rw, _ := st.RemoteWorkers(); len(rw) != 0 {
		t.Errorf("an allow nobody picked up left %d workers: %+v", len(rw), rw)
	}
}

// A worker that has run a job stays when its allow expires, even if the request was never picked up.
func TestJoinExpiryKeepsAWorkerThatRanAJob(t *testing.T) {
	_, st, _, _ := libServer(t, false)
	b := newJoinBook(st)
	t0 := time.Now()
	id, _, err := b.add(&joinReq{name: "Office PC", version: "test", os: "linux", capabilities: "{}"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.allow(id, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	rw, _ := st.RemoteWorkers()
	if len(rw) != 1 {
		t.Fatalf("the allow paired %d workers, want 1", len(rw))
	}
	if _, err := st.Enqueue(jobs.Job{Library: "/m", Path: "/m/a.mkv", SizeBefore: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Claim(rw[0].ID); err != nil {
		t.Fatalf("claim for the worker: %v", err)
	}
	b.pending(t0.Add(JoinFor + time.Minute))
	if rw, _ := st.RemoteWorkers(); len(rw) != 1 {
		t.Errorf("a worker that ran a job was removed: %d left", len(rw))
	}
}
