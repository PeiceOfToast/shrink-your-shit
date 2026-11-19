// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"strings"
	"testing"
	"time"
)

// Three graphics-encoder failures in a row pause that worker's encoder for an hour; a success in between starts
// the run again, and another worker is not affected.
func TestEncoderPausesAfterThreeFallsInARow(t *testing.T) {
	s := open(t)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	id, _ := s.Enqueue(Job{Path: "/m/a.mkv", SizeBefore: 100})

	for i := 0; i < 2; i++ {
		if paused, err := s.EncoderFell(id, "w", "h264_qsv", "unknown encoder"); err != nil || paused {
			t.Fatalf("fall %d: paused %v, err %v; want not paused", i+1, paused, err)
		}
	}
	if err := s.EncoderWorked("w"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if paused, err := s.EncoderFell(id, "w", "h264_qsv", "unknown encoder"); err != nil || paused {
			t.Fatalf("after a success, fall %d: paused %v, err %v; want not paused", i+1, paused, err)
		}
	}
	if paused, err := s.EncoderFell(id, "w", "h264_qsv", "unknown encoder"); err != nil || !paused {
		t.Fatalf("third in a row: paused %v, err %v; want paused", paused, err)
	}
	until, on, err := s.EncoderPaused("w")
	if err != nil || !on || !until.Equal(now.Add(time.Hour)) {
		t.Fatalf("EncoderPaused = %v, %v, %v; want paused until one hour on", until, on, err)
	}
	if _, on, _ := s.EncoderPaused("other"); on {
		t.Error("another worker is paused too")
	}
	now = now.Add(61 * time.Minute)
	if _, on, _ := s.EncoderPaused("w"); on {
		t.Error("still paused after the hour")
	}
}

// Only an encoder or ffmpeg failure converts again on the processor; a full disk or a damaged source does not.
func TestFallsBackToProcessorOnlyForEncoderErrors(t *testing.T) {
	cases := []struct {
		stderr string
		want   bool
	}{
		{"Unknown encoder 'h264_qsv'", true},
		{"Error while opening encoder for output stream #0:0", true},
		{"Conversion failed!", true},
		{"No space left on device", false},
		{"Invalid data found when processing input", false},
	}
	for _, c := range cases {
		if got := FallsBackToProcessor(strings.Split(c.stderr, "\n")); got != c.want {
			t.Errorf("%q: %v, want %v", c.stderr, got, c.want)
		}
	}
}

// The overview counts today's fall-backs per worker, from the job's worker.
func TestEncoderFallbacksCountPerWorker(t *testing.T) {
	s := open(t)
	s.Enqueue(Job{Path: "/m/b.mkv", SizeBefore: 100})
	j, err := s.Claim("gpu-box")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EncoderFell(j.ID, "gpu-box", "h264_qsv", "unknown encoder"); err != nil {
		t.Fatal(err)
	}
	got, err := s.EncoderFallbacksSince(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Worker != "gpu-box" || got[0].Count != 1 {
		t.Fatalf("fall-backs since an hour ago = %+v; want one for gpu-box", got)
	}
	none, err := s.EncoderFallbacksSince(time.Now().Add(time.Hour))
	if err != nil || len(none) != 0 {
		t.Errorf("fall-backs after now = %+v, %v; want none", none, err)
	}
}

// The overview shows the latest fall-back's encoder and reason: a second fall-back replaces the first.
func TestEncoderLastFellIsTheLatest(t *testing.T) {
	s := open(t)
	s.Enqueue(Job{Path: "/m/c.mkv", SizeBefore: 100})
	j, err := s.Claim("gpu-box")
	if err != nil {
		t.Fatal(err)
	}
	if enc, reason, err := s.EncoderLastFell("gpu-box"); err != nil || enc != "" || reason != "" {
		t.Fatalf("before any fall-back: %q, %q, %v; want none", enc, reason, err)
	}
	if _, err := s.EncoderFell(j.ID, "gpu-box", "hevc_qsv", "Error while opening encoder"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EncoderFell(j.ID, "gpu-box", "h264_qsv", "Unknown encoder 'h264_qsv'"); err != nil {
		t.Fatal(err)
	}
	enc, reason, err := s.EncoderLastFell("gpu-box")
	if err != nil || enc != "h264_qsv" || reason != "Unknown encoder 'h264_qsv'" {
		t.Fatalf("EncoderLastFell = %q, %q, %v; want the second fall-back", enc, reason, err)
	}
}
