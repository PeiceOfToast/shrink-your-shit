// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"strings"
	"testing"
)

// moveAlong carries Convert again's Done -> Queued and nothing else: any other edge is refused, and the
// rows it was pointed at stay where they were (design-583 P3). It is not a way around the state machine.
func TestMoveAlongOnlyAllowsDoneToQueued(t *testing.T) {
	s := open(t)
	enqueue := func(name string) int64 {
		t.Helper()
		id, err := s.Enqueue(Job{Library: "/m", Path: "/m/" + name, Reason: "r"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	done := enqueue("done.mkv")
	if _, err := s.Claim("w"); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(done, "/m/done.mkv", 40); err != nil {
		t.Fatal(err)
	}
	failed := enqueue("failed.mkv")
	if _, err := s.Claim("w"); err != nil {
		t.Fatal(err)
	}
	if err := s.Fail(failed, "convert_failed", "x"); err != nil {
		t.Fatal(err)
	}
	queued := enqueue("queued.mkv")

	for _, c := range []struct {
		name string
		id   int64
		from Status
		to   Status
	}{
		{"done -> running", done, Done, Running},
		{"failed -> done", failed, Failed, Done},
		{"queued -> running", queued, Queued, Running}, // legal for the generic moves, still not moveAlong's
	} {
		n, err := s.moveAlong(s.db, c.from, c.to, "", nil, `id=?`, c.id)
		if err == nil || !strings.Contains(err.Error(), "cannot go from "+string(c.from)+" to "+string(c.to)) {
			t.Errorf("%s: %v (n %d), want cannot go from %s to %s", c.name, err, n, c.from, c.to)
		}
		if n != 0 {
			t.Errorf("%s: moved %d rows, want 0", c.name, n)
		}
		if j, err := s.Get(c.id); err != nil || j.Status != c.from {
			t.Fatalf("%s changed the job: status %s, %v; want %s", c.name, j.Status, err, c.from)
		}
	}
}
