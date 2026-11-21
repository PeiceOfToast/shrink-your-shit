// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"testing"
	"time"
)

// A remote encode whose position is frozen (out_time stops in a subtitle gap) is alive while its frames or bytes
// grow within twice the source, as a local one is; a worker that sends no frames or bytes is judged by position alone.
func TestRemoteFrozenPositionAliveByFramesAndBytes(t *testing.T) {
	t0 := time.Now()
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	type report struct {
		sec                   int
		pos                   float64
		frame, wrote, sizeSrc int64
		old                   bool // position only, as an older worker sends it
	}
	cases := []struct {
		name      string
		reports   []report
		wantStill time.Duration // how long since the last movement, at the last report
	}{
		{"frames grow, bytes none", []report{
			{0, 1e6, 0, 0, 14, false}, {5, 1e6, 50, 0, 14, false}}, 0},
		{"bytes grow within twice the source", []report{
			{0, 1e6, 0, 0, 14, false}, {5, 1e6, 0, 28, 14, false}}, 0},
		{"bytes past twice the source are not progress", []report{
			{0, 1e6, 0, 0, 14, false}, {5, 1e6, 0, 29, 14, false}}, 5 * time.Second},
		{"frames grow but the source size is unknown", []report{
			{0, 1e6, 0, 0, 0, false}, {5, 1e6, 50, 0, 0, false}}, 5 * time.Second},
		{"an older worker, position frozen", []report{
			{0, 1e6, 0, 0, 14, true}, {5, 1e6, 0, 0, 14, true}}, 5 * time.Second},
		{"an older worker, position moving", []report{
			{0, 1e6, 0, 0, 14, true}, {5, 2e6, 0, 0, 14, true}}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rj := NewRemoteJob("lease")
			var last report
			for _, r := range c.reports {
				if r.old {
					rj.Converting(r.pos, at(r.sec), 100)
				} else {
					rj.ConvertingFrames(r.pos, r.frame, r.wrote, r.sizeSrc, at(r.sec), 100)
				}
				last = r
			}
			still, judging := rj.stillFor(at(last.sec))
			if !judging {
				t.Fatal("the encode is not judged")
			}
			if still != c.wantStill {
				t.Errorf("still for %v, want %v", still, c.wantStill)
			}
		})
	}
}

// EncodeEnded (the worker's checks and upload) stops the judging whatever the frames say, as before.
func TestRemoteFramesDoNotJudgeAfterTheEncodeEnds(t *testing.T) {
	t0 := time.Now()
	rj := NewRemoteJob("lease")
	rj.ConvertingFrames(1e6, 0, 0, 14, t0, 100)
	rj.EncodeEnded()
	rj.ConvertingFrames(1e6, 50, 0, 14, t0.Add(time.Second), 100)
	if _, judging := rj.stillFor(t0.Add(time.Second)); !judging {
		t.Fatal("a new converting report starts judging again")
	}
	rj.EncodeEnded()
	if _, judging := rj.stillFor(t0.Add(time.Second)); judging {
		t.Error("after the encode ends it is not judged")
	}
}
