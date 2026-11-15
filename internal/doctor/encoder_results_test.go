// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package doctor

import (
	"context"
	"testing"
	"time"
)

// Q1: a hardware test run's results reach OnTested, once it is stored.
func TestCheckerReportsItsTestRun(t *testing.T) {
	c := checker(1 << 40)
	c.Exec = fakeFFmpeg(true, true, []string{"hevc_nvenc", "hevc_vaapi"}, map[string]bool{"hevc_nvenc": true})
	var got []EncoderResult
	calls := 0
	c.OnTested = func(at time.Time, rs []EncoderResult) {
		calls++
		got = rs
	}
	c.Tools(context.Background())
	c.Tools(context.Background()) // fresh: shared, not tested again
	if calls != 1 {
		t.Fatalf("one stored run, got %d", calls)
	}
	want := map[string]EncoderResult{"hevc_nvenc": {Encoder: "hevc_nvenc", Passed: true, Detail: "10-bit"}, "hevc_vaapi": {Encoder: "hevc_vaapi"}}
	for _, r := range got {
		if w, ok := want[r.Encoder]; ok && w != r {
			t.Errorf("%s: want %+v, got %+v", r.Encoder, w, r)
		}
		delete(want, r.Encoder)
	}
	if len(want) != 0 {
		t.Errorf("missing results %v in %+v", want, got)
	}
}

func TestResultsOfMarksTenBitAndFailures(t *testing.T) {
	hw := [5][]string{{"hevc_nvenc"}, {"hevc_vaapi"}, {"hevc_nvenc"}, {"libx265"}, {"libsvtav1"}}
	got := resultsOf(hw)
	want := []EncoderResult{
		{Encoder: "hevc_nvenc", Passed: true, Detail: "10-bit"},
		{Encoder: "libx265", Passed: true},
		{Encoder: "hevc_vaapi"},
		{Encoder: "libsvtav1"},
	}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: want %+v, got %+v", i, want[i], got[i])
		}
	}
}
