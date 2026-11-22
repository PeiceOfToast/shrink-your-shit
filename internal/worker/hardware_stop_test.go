// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The Convert step's hardware choice stops a graphics failure only under only and fail; the default steps down, and
// a disk-full failure keeps its own retry whatever the choice.
func TestHardwareStopWordsFollowTheChoice(t *testing.T) {
	failed := errors.New("ffmpeg exited 1")
	cases := []struct {
		name string
		v    rules.VideoEncode
		err  error
		want string
	}{
		{"default steps down", rules.VideoEncode{}, failed, ""},
		{"only stops", rules.VideoEncode{Hardware: "only"}, failed, HardwareOnlyFailedWords},
		{"fail stops", rules.VideoEncode{HardwareFail: "fail"}, failed, HardwareFailFailedWords},
		{"only wins over fail", rules.VideoEncode{Hardware: "only", HardwareFail: "fail"}, failed, HardwareOnlyFailedWords},
		{"cpu does not stop a card", rules.VideoEncode{Hardware: "cpu"}, failed, ""},
		{"disk full keeps its retry", rules.VideoEncode{Hardware: "only"}, syscall.ENOSPC, ""},
	}
	for _, c := range cases {
		if got := HardwareStopWords(c.v, c.err); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// A paused graphics card is back only after the pause, so the paused wait must outlast it: the first retry comes
// after the whole pause, and the job's last try is still within reach.
func TestPausedWaitOutlastsThePause(t *testing.T) {
	wait, ok := retryIn(retryable(errors.New("paused"), retryPaused), 0)
	if !ok || wait < jobs.EncoderPauseFor {
		t.Errorf("first wait %v (ok %v): want at least the %v pause", wait, ok, jobs.EncoderPauseFor)
	}
	if wait2, ok2 := retryIn(retryable(errors.New("paused"), retryPaused), 1); !ok2 || wait2 < time.Hour {
		t.Errorf("second wait %v (ok %v): want it to stay past the pause", wait2, ok2)
	}
}

// Under only, a paused card makes the job wait for it: no encode runs, the log never says the processor takes the
// file, and the retry is after the whole pause (the wait is 1 h 5 min).
func TestPausedCardUnderOnlyWaitsForTheCard(t *testing.T) {
	s := newScenario(t)
	w := hardwareWorker(s, "only", "")
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_nvenc", "" }
	for i := 0; i < 3; i++ {
		fall(t, s, w)
	}
	w.RunOne(context.Background())
	if j := s.job(); j.Status == jobs.Done || j.Status == jobs.Failed {
		t.Fatalf("status %s: want the job to wait for the card", j.Status)
	}
	if enc := encodeCalls(s); len(enc) != 0 {
		t.Errorf("encodes %v: want none while the card is paused", enc)
	}
	if n := logCount(t, s, "using the processor"); n != 0 {
		t.Errorf("log says the processor takes the file %d times under only", n)
	}
	if n := logCount(t, s, "trying again in 1h5m0s"); n != 1 {
		t.Errorf("retry line: %d, want the wait to outlast the pause (1h5m0s)", n)
	}
}

// Under only, a local encoder that did not pass its test: the job log gives the reason and never names the processor,
// and the refusal follows the reason.
func TestOnlyLocalRefusalLogNeverNamesTheProcessor(t *testing.T) {
	s := newScenario(t)
	w := hardwareWorker(s, "only", "")
	w.Encoder = func(string, bool, *rules.HDR) (string, string) {
		return "", "encoder h264_qsv didn't pass its test here"
	}
	w.RunOne(context.Background()) // a failed job returns its error; the status below is the check
	// The refusal's own words say the processor is never used; no other line may name it.
	lines, err := s.store.LogLines(s.id)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if strings.Contains(l.Text, "processor") && !strings.Contains(l.Text, "never uses the processor") {
			t.Errorf("log line names the processor under only, before the refusal: %q", l.Text)
		}
	}
	if n := logCount(t, s, "encoder h264_qsv didn't pass its test here"); n != 1 {
		t.Errorf("reason line: %d, want 1", n)
	}
	if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, "Hardware only: no graphics encoder") {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
}
