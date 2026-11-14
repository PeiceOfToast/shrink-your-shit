// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"errors"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// An allowed worker that picks up its token and never calls the server is gone after the join window, as one
// that was never picked up is. The pick-up hands the token once: a second pick-up finds nothing.
func TestJoinPickedUpNeverHeardFromIsRemoved(t *testing.T) {
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
	if _, token, err := st.PickUpJoin(id); err != nil || token == "" {
		t.Fatalf("pick-up: %q %v", token, err)
	}
	if _, _, err := st.PickUpJoin(id); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("a second pick-up: %v, want not found", err)
	}
	b.pending(t0.Add(JoinFor + time.Minute))
	if rw, _ := st.RemoteWorkers(); len(rw) != 0 {
		t.Errorf("a picked-up worker that never called was left: %+v", rw)
	}
}

// A picked-up worker that has called the server (its heartbeat) stays when its window passes.
func TestJoinPickedUpAndHeardFromIsKept(t *testing.T) {
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
	wk, _, err := st.PickUpJoin(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SeenWorker(wk, "test", 1, t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	b.pending(t0.Add(JoinFor + time.Minute))
	if rw, _ := st.RemoteWorkers(); len(rw) != 1 {
		t.Errorf("a worker that called the server was removed: %d left", len(rw))
	}
}

// The card of an allowed worker is not online before the worker's first call: the allow is not a call.
func TestJoinAllowedCardNotSeenBeforeItsFirstCall(t *testing.T) {
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
	if rw[0].LastSeen != "" {
		t.Errorf("an allowed worker reads as heard from (%s) before its first call", rw[0].LastSeen)
	}
}
