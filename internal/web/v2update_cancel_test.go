// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"net/http"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/update"
)

// An install waiting for the running job: cancel puts the state back, turns the workers on again, writes no
// marker and never calls the helper.
func TestUpdateCancelWhileWaiting(t *testing.T) {
	srv, u, st, fake := installServer(t, "script")
	runningJob(t, st)
	if code, body := postJSON(t, srv.URL+"/api/v2/update/install", `{"when":"after-job"}`); code != http.StatusAccepted {
		t.Fatalf("after-job: %d %v", code, body)
	}
	if !st.Paused() {
		t.Fatal("the waiting install should pause the workers")
	}
	code, body := postJSON(t, srv.URL+"/api/v2/update/cancel", `{}`)
	if code != http.StatusOK {
		t.Fatalf("cancel: %d %v", code, body)
	}
	if inst := body["install"].(map[string]any); inst["state"] == "waiting" {
		t.Errorf("install after cancel %v, still waiting", inst)
	}
	if st.Paused() {
		t.Error("the workers stayed paused after the cancel")
	}
	time.Sleep(50 * time.Millisecond) // several polls: the cancelled wait must not start the install
	if got := fake.installs(); len(got) != 0 {
		t.Errorf("helper was called after cancel: %v", got)
	}
	if update.ReadMarker(u.dataDir) != nil {
		t.Error("a marker was written after cancel")
	}
	_, get := getJSON(t, srv.URL+"/api/v2/update")
	if inst := get["install"].(map[string]any); inst["state"] == "waiting" {
		t.Errorf("GET after cancel: install %v, still waiting", inst)
	}
}

// The workers were already paused before the install (by someone else): cancel leaves them paused.
func TestUpdateCancelKeepsAPauseThatWasNotOurs(t *testing.T) {
	srv, _, st, _ := installServer(t, "script")
	runningJob(t, st)
	if err := st.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	if code, body := postJSON(t, srv.URL+"/api/v2/update/install", `{"when":"after-job"}`); code != http.StatusAccepted {
		t.Fatalf("after-job: %d %v", code, body)
	}
	if code, body := postJSON(t, srv.URL+"/api/v2/update/cancel", `{}`); code != http.StatusOK {
		t.Fatalf("cancel: %d %v", code, body)
	}
	if !st.Paused() {
		t.Error("cancel turned on workers that were paused before the install")
	}
}

func TestUpdateCancelWhenIdle(t *testing.T) {
	srv, _, _, fake := installServer(t, "script")
	code, body := postJSON(t, srv.URL+"/api/v2/update/cancel", `{}`)
	if code != http.StatusConflict || updErrCode(body) != "nothing_to_cancel" {
		t.Errorf("cancel when idle: %d %v", code, body)
	}
	if len(fake.installs()) != 0 {
		t.Errorf("helper called: %v", fake.installs())
	}
}

// Once the helper has the update, cancel says so and changes nothing.
func TestUpdateCancelRefusedWhileTheHelperInstalls(t *testing.T) {
	srv, u, _, fake := installServer(t, "script")
	if code, body := postJSON(t, srv.URL+"/api/v2/update/install", `{"when":"now"}`); code != http.StatusAccepted {
		t.Fatalf("install: %d %v", code, body)
	}
	code, body := postJSON(t, srv.URL+"/api/v2/update/cancel", `{}`)
	if code != http.StatusConflict || updErrCode(body) != "update_in_progress" {
		t.Errorf("cancel during the helper's install: %d %v", code, body)
	}
	if m := update.ReadMarker(u.dataDir); m == nil {
		t.Error("the marker went after a refused cancel")
	}
	if got := fake.installs(); len(got) != 1 {
		t.Errorf("helper got %v", got)
	}
}

// A cancel and the start race for the same wait: either the cancel wins (no install, workers on) or the
// start wins (installed, marker written). Never the marker with the workers on.
func TestUpdateCancelRacesTheStart(t *testing.T) {
	for i := 0; i < 10; i++ {
		srv, u, st, fake := installServer(t, "script")
		id := runningJob(t, st)
		if code, body := postJSON(t, srv.URL+"/api/v2/update/install", `{"when":"after-job"}`); code != http.StatusAccepted {
			t.Fatalf("after-job: %d %v", code, body)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := st.Complete(id, "", 5); err != nil {
				t.Error(err)
			}
		}()
		code, body := postJSON(t, srv.URL+"/api/v2/update/cancel", `{}`)
		<-done
		switch code {
		case http.StatusOK:
			time.Sleep(20 * time.Millisecond)
			if got := fake.installs(); len(got) != 0 || update.ReadMarker(u.dataDir) != nil || st.Paused() {
				t.Fatalf("round %d: cancelled but helper %v, marker %v, paused %v", i, got, update.ReadMarker(u.dataDir), st.Paused())
			}
		case http.StatusConflict:
			code := updErrCode(body)
			if code != "update_in_progress" && code != "nothing_to_cancel" {
				t.Fatalf("round %d: cancel refused with %v", i, code)
			}
			updWaitFor(t, "the install after the race", func() bool { return len(fake.installs()) == 1 })
			if m := update.ReadMarker(u.dataDir); m == nil {
				t.Fatalf("round %d: installed without a marker", i)
			}
		default:
			t.Fatalf("round %d: cancel %d %v", i, code, body)
		}
	}
}
