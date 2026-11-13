// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"fmt"
	"os/exec"
	"testing"
)

// User review (#214 P2): a failed encode says whether ffmpeg ran and its exit status. A program that exited has a
// status, one a signal ended has none, and one that could not be started never ran. The errors are real ones from
// exec, wrapped as media.OSExec wraps them.
func TestFfmpegExitStatus(t *testing.T) {
	exited := exec.Command("sh", "-c", "exit 3").Run()
	signalled := exec.Command("sh", "-c", "kill -9 $$").Run()
	missing := exec.Command("/nonexistent/ffmpeg-for-this-test").Run()
	for _, c := range []struct {
		name string
		err  error
		ran  bool
		exit int
	}{
		{"exited with 3", fmt.Errorf("ffmpeg: %w: last line", exited), true, 3},
		{"killed by a signal", fmt.Errorf("ffmpeg: %w", signalled), true, -1},
		{"could not start", fmt.Errorf("ffmpeg: %w", missing), false, -1},
	} {
		if c.err == nil {
			t.Fatalf("%s: no error to test", c.name)
		}
		if ran, exit := ffmpegExit(c.err); ran != c.ran || exit != c.exit {
			t.Errorf("%s: ran %v exit %d, want %v %d", c.name, ran, exit, c.ran, c.exit)
		}
	}
}
