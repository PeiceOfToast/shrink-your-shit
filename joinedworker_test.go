// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/remote"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// A saved worker credential starts the remote loop (its heartbeats reach the server) and holds this install's own
// converter off; stopping the role ends the loop and gives the converter back.
func TestJoinedWorkerRunsTheRemoteLoopNotTheLocalPool(t *testing.T) {
	var heartbeats atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/worker/v1/heartbeat" {
			heartbeats.Add(1)
			if r.Header.Get("Authorization") != "Bearer tok" {
				t.Errorf("heartbeat auth %q, want the saved token", r.Header.Get("Authorization"))
			}
		}
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	dir := filepath.Join(t.TempDir(), "worker")
	if err := (remote.State{Server: server.URL, WorkerID: "w-1", Name: "Office PC", Token: "tok"}).Save(dir); err != nil {
		t.Fatal(err)
	}
	j := newJoinedWorker(dir, "/usr/bin/ffmpeg", "/usr/bin/ffprobe")
	if !localPoolTakes(j, true) {
		t.Fatal("before joining, the local converter takes jobs when its switch is on")
	}

	j.SetRole("worker")
	t.Cleanup(func() { j.SetRole("unset") })
	if !j.Working() {
		t.Fatal("a saved worker role didn't start the worker loop")
	}
	deadline := time.Now().Add(5 * time.Second)
	for heartbeats.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if heartbeats.Load() == 0 {
		t.Fatal("the worker loop never reached the server")
	}
	if localPoolTakes(j, true) {
		t.Error("the local converter still takes jobs while this install is a joined worker")
	}

	j.SetRole("unset")
	if j.Working() {
		t.Error("the loop still runs after the role went back to unset")
	}
	if !localPoolTakes(j, true) {
		t.Error("the local converter isn't back after the role went back to unset")
	}
}

// Without a saved credential, the worker role starts nothing.
func TestJoinedWorkerWithoutACredentialStartsNothing(t *testing.T) {
	j := newJoinedWorker(filepath.Join(t.TempDir(), "worker"), "/usr/bin/ffmpeg", "/usr/bin/ffprobe")
	j.SetRole("worker")
	if j.Working() {
		t.Error("the worker role with no saved credential started a loop")
	}
}

// At startup a saved worker role holds the local pool back before it can claim a queued job: the job stays queued
// for the joined worker (main applies the saved role before the pool starts).
func TestSavedWorkerRoleKeepsTheLocalPoolFromClaimingAtStartup(t *testing.T) {
	var heartbeats atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/worker/v1/heartbeat" {
			heartbeats.Add(1)
		}
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	dir := t.TempDir()
	if err := (remote.State{Server: server.URL, WorkerID: "w-1", Name: "Office PC", Token: "tok"}).Save(filepath.Join(dir, "worker")); err != nil {
		t.Fatal(err)
	}
	st, err := jobs.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	id, err := st.Enqueue(jobs.Job{Library: dir, Path: filepath.Join(dir, "Movie.mkv"), DurationSec: 100})
	if err != nil {
		t.Fatal(err)
	}

	j := newJoinedWorker(filepath.Join(dir, "worker"), "/usr/bin/ffmpeg", "/usr/bin/ffprobe")
	j.SetRole("worker") // main does this before the local pool starts
	t.Cleanup(func() { j.SetRole("unset") })
	local := &worker.Worker{ID: "local", Queue: st, Paused: localGate(j, st)} // the gate main gives the local pool
	ran, err := local.RunOne(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("the local pool ran a job while this install is a joined worker")
	}
	if job, _ := st.Get(id); job.Status != jobs.Queued {
		t.Errorf("job status %s, want it still queued for the joined worker", job.Status)
	}
}

// main applies the saved role before the local pool starts. This reads main.go and fails if the role call moves
// after pool.Run, so the startup test cannot pass while the order is wrong.
func TestMainAppliesTheSavedRoleBeforeThePoolRuns(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var setRole, poolRun token.Pos
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if recv.Name == "joined" && sel.Sel.Name == "SetRole" && setRole == token.NoPos {
			setRole = call.Pos()
		}
		if recv.Name == "pool" && sel.Sel.Name == "Run" && poolRun == token.NoPos {
			poolRun = call.Pos()
		}
		return true
	})
	if setRole == token.NoPos {
		t.Fatal("main.go no longer applies the saved role (joined.SetRole)")
	}
	if poolRun == token.NoPos {
		t.Fatal("main.go no longer runs the pool (pool.Run) in the form this test reads")
	}
	if setRole > poolRun {
		t.Error("main.go applies the saved role after the local pool starts")
	}
}
