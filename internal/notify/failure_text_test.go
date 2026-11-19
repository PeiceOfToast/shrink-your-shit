// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package notify

import (
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// With Options.FailureText set, a failure's notification carries the Jobs sentence (its first line), not fixed words.
func TestFailureSentenceIsTheJobsOneWhenSet(t *testing.T) {
	st, svc, _, _ := fixture(t)
	svc.opt.FailureText = func(int64) string {
		return "Couldn't write to /mnt/out: permission denied.\nA second line is not sent."
	}
	start(t, svc)
	r := newRx(t, 200)
	mustCreate(t, svc, NewTarget{Name: "hook", Kind: "webhook", URL: r.srv.URL + "/hook/tok"})
	id := enqueue(t, st, "/media/a.mkv")
	claim(t, st)
	if err := st.Fail(id, jobs.ReasonCannotWrite, "The file at /home/someone/private/a.mkv is not the one that was queued."); err != nil {
		t.Fatal(err)
	}
	svc.Tick()
	svc.Idle()
	r.mu.Lock()
	defer r.mu.Unlock()
	if want := "Failed: a.mkv. Couldn't write to /mnt/out: permission denied."; len(r.bodies) != 1 || r.bodies[0]["text"] != want {
		t.Fatalf("failure message: %v, want %q", r.bodies, want)
	}
}
