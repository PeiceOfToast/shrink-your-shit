// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A playback test that keeps the original says why in the job log: what it checked, how far playback got, and
// the distinct ffmpeg errors. The reason the person reads stays plain.
func TestPlaybackKeptLogSaysWhy(t *testing.T) {
	s := newScenario(t)
	s.decodeErr = errors.New("exit status 1")
	s.decodeStdout = "out_time_us=3000000\nframe=72\nprogress=continue\n"
	s.decodeStderr = strings.Join([]string{
		"[hevc @ 0x1a2b] Invalid NAL unit size 5",
		"[hevc @ 0x3c4d] Invalid NAL unit size 9",
		"[hevc @ 0x1a2b] Invalid NAL unit size 7",
		"Error while decoding stream #0:0",
		"line a", "line b", "line c", "line d", "line e",
	}, "\n")
	s.run(Replace)
	s.assertKept("does not play cleanly")
	assertPlain(t, s.job().Error)
	if !strings.Contains(s.job().Error, "9 errors") {
		t.Errorf("reason %q does not give the error count", s.job().Error)
	}

	logLines, err := s.store.LogLines(s.id)
	if err != nil {
		t.Fatal(err)
	}
	var detail []string
	for _, l := range logLines {
		if l.Step == 1 && l.Stream == "check" {
			detail = append(detail, l.Text)
		}
	}
	find := func(prefix string) string {
		for _, l := range detail {
			if strings.HasPrefix(l, prefix) {
				return l
			}
		}
		return ""
	}
	for _, want := range []string{
		"Check: playback test of the whole new file (0:00:00 to ",
		"Where: playback reached frame 72 (about 0:00:03)",
		"Errors: 9 error lines from ffmpeg, 7 different",
		"Encoder: libx265, -crf 26, hardware: none",
		"Original: ",
		"New file: ",
	} {
		if find(want) == "" {
			t.Errorf("job log has no line starting %q; detail: %q", want, detail)
		}
	}
	t.Logf("step 1 log lines: %q", detail)
	labels := []string{"Check:", "Where:", "Errors:", "Encoder:", "Original:", "New file:"}
	shown := 0
	for _, l := range detail {
		labelled := false
		for _, p := range labels {
			labelled = labelled || strings.HasPrefix(l, p)
		}
		if !labelled {
			shown++ // one of ffmpeg's own lines
		}
	}
	if shown != 5 {
		t.Errorf("%d distinct ffmpeg lines in the log, want 5: %q", shown, detail)
	}

	steps, err := s.store.RunSteps(s.id)
	if err != nil {
		t.Fatal(err)
	}
	var checked bool
	for _, st := range steps {
		if st.Index == 1 && st.Name == "check" && st.Outcome == "failed" && st.Result != "" {
			checked = true
		}
	}
	if !checked {
		t.Errorf("no failed check step recorded: %+v", steps)
	}
	// A kept job is not a failed one: no failure summary, or the drawer would say "Why it failed".
	if f, err := s.store.FailureOf(s.id); err != nil || f != nil {
		t.Errorf("kept job has a failure summary %+v (err %v), want none", f, err)
	}
}

// libva's driver chatter is kept in the log as "info" lines: the drawer leaves them out, and they are never key lines.
func TestLibvaChatterIsNotOutput(t *testing.T) {
	st, id := realJob(t, filepath.Join(t.TempDir(), "x.mkv"), 10)
	w := &Worker{Queue: st}
	w.recordEncode(id, []string{"-c:v", "libx265"}, time.Now(),
		"libva info: Driver version: Intel iHD 24.1\nReal error line", errors.New("exit status 1"), "")
	logLines, err := st.LogLines(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range logLines {
		if strings.HasPrefix(l.Text, "libva info:") && l.Stream != "info" {
			t.Errorf("libva line stored as %q, want info: %q", l.Stream, l.Text)
		}
		if strings.HasPrefix(l.Text, "Real error") && l.Stream != "stderr" {
			t.Errorf("ffmpeg error line stored as %q, want stderr", l.Stream)
		}
	}
	f, err := st.FailureOf(id)
	if err != nil || f == nil {
		t.Fatalf("no failure summary: %v", err)
	}
	if len(f.KeyLines) != 1 || f.KeyLines[0] != "Real error line" {
		t.Errorf("key lines %q, want only the real error line", f.KeyLines)
	}
}
