// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"errors"
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
