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

// An allowed worker that picked up its token and never called in leaves a not-connected notice when its window ends:
// the worker is gone, the badge does not count the notice, and the person's list shows it until they dismiss it.
func TestJoinNeverCalledLeavesNotConnectedNotice(t *testing.T) {
	_, st, _, _ := libServer(t, false)
	b := newJoinBook(st)
	v := &workerView{store: st, joins: b}
	t0 := time.Now()
	id, _, err := b.add(&joinReq{name: "Office PC", version: "test", os: "linux", capabilities: "{}"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.allow(id, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.PickUpJoin(id); err != nil {
		t.Fatal(err)
	}
	now := t0.Add(JoinFor + time.Minute)
	views := v.joinViews(now)
	if rw, _ := st.RemoteWorkers(); len(rw) != 0 {
		t.Errorf("a picked-up worker that never called was left: %+v", rw)
	}
	if len(views) != 1 {
		t.Fatalf("joinRequests has %d entries, want the one not-connected notice: %v", len(views), views)
	}
	if got := views[0]["state"]; got != "not_connected" {
		t.Errorf("state %v, want not_connected", got)
	}
	if got := views[0]["name"]; got != "Office PC" {
		t.Errorf("name %v, want Office PC", got)
	}
	if got, _ := views[0]["expiresAt"].(string); got == "" {
		t.Errorf("the notice has no expiresAt")
	}
	if n := b.waiting(now); n != 0 {
		t.Errorf("the summary badge counts %d, want 0 for a not-connected notice", n)
	}
	// The badge must not count the notice by its decision alone, even at a clock still inside the notice's old window.
	if n := b.waiting(t0.Add(time.Minute)); n != 0 {
		t.Errorf("the summary badge counts %d inside the old window, want 0", n)
	}
	if err := b.dismiss(id); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if left := v.joinViews(now); len(left) != 0 {
		t.Errorf("a dismissed notice is still listed: %v", left)
	}
}

// A picked-up worker that called in has no notice when its window passes.
func TestJoinHeardFromHasNoNotice(t *testing.T) {
	_, st, _, _ := libServer(t, false)
	b := newJoinBook(st)
	v := &workerView{store: st, joins: b}
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
	now := t0.Add(JoinFor + time.Minute)
	if views := v.joinViews(now); len(views) != 0 {
		t.Errorf("a worker that called in has a join entry: %v", views)
	}
	if rw, _ := st.RemoteWorkers(); len(rw) != 1 {
		t.Errorf("a worker that called the server was removed: %d left", len(rw))
	}
}
