// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"testing"
	"time"
)

// A remote finish (position at the file's length) whose worker reports its output is judged by that output's growth:
// the stall allowance runs from the last growth, and FinishAfter bounds the frozen position. These match the local
// finish in growing_temp_test.go.
func TestRemoteFinishingOutputJudgedByGrowth(t *testing.T) {
	t0 := time.Now()
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	const length = 100e6 // the position at the file's end, µs
	// finish returns a remote job at the file's end at t0, judged with the given limits; it reports its output
	// when reports is true (an older worker does not).
	finish := func(stallAfter, finishAfter time.Duration, reports bool) (*Worker, *RemoteJob) {
		w := &Worker{StallAfter: stallAfter, FinishAfter: finishAfter}
		rj := NewRemoteJob("lease")
		if reports {
			rj.SendsOutput()
		}
		rj.ConvertingFrames(length, -1, 0, 14, at(0), 100)
		return w, rj
	}
	// grow reports the output at second s, n bytes into the new file.
	grow := func(rj *RemoteJob, s int, n int64) { rj.ConvertingFrames(length, -1, n, 14, at(s), 100) }

	t.Run("a finish whose output stops growing stops at StallAfter", func(t *testing.T) {
		// red before the change: the finish was judged by FinishAfter (a minute here), so it ran on
		w, rj := finish(2*time.Second, time.Minute, true)
		grow(rj, 1, 1)
		grow(rj, 2, 2) // the last growth
		if _, k := w.remoteJudge(rj, at(3)); k != stallNone {
			t.Errorf("at 3 s, 1 s after the last growth: %v, want none", k)
		}
		if limit, k := w.remoteJudge(rj, at(4)); k != stallNoProgress || limit != 2*time.Second {
			t.Errorf("at 4 s, 2 s after the last growth: %v %v, want stallNoProgress 2s", k, limit)
		}
	})

	t.Run("a growing finish is not stopped before FinishAfter", func(t *testing.T) {
		w, rj := finish(2*time.Second, 30*time.Second, true)
		for s := 1; s <= 12; s++ {
			grow(rj, s, int64(s)) // 12 bytes, within twice the 14-byte source
			if _, k := w.remoteJudge(rj, at(s)); k != stallNone {
				t.Fatalf("at %d s, the output growing: %v, want none", s, k)
			}
		}
	})

	t.Run("a growing finish is stopped at FinishAfter", func(t *testing.T) {
		w, rj := finish(2*time.Second, 5*time.Second, true)
		for s := 1; s <= 4; s++ {
			grow(rj, s, int64(s))
		}
		if _, k := w.remoteJudge(rj, at(4)); k != stallNone {
			t.Errorf("at 4 s: %v, want none", k)
		}
		grow(rj, 5, 5)
		if limit, k := w.remoteJudge(rj, at(5)); k != stallFinish || limit != 5*time.Second {
			t.Errorf("at 5 s, the position frozen for FinishAfter: %v %v, want stallFinish 5s", k, limit)
		}
	})

	t.Run("an older worker that does not report output keeps the FinishAfter rule", func(t *testing.T) {
		w, rj := finish(2*time.Second, 5*time.Second, false)
		if _, k := w.remoteJudge(rj, at(4)); k != stallNone {
			t.Errorf("at 4 s: %v, want none", k)
		}
		if limit, k := w.remoteJudge(rj, at(5)); k != stallNoProgress || limit != 5*time.Second {
			t.Errorf("at 5 s, silent for FinishAfter: %v %v, want stallNoProgress 5s", k, limit)
		}
	})
}
