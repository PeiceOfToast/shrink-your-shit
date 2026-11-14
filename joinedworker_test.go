// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/remote"
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
