// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build linux

package media

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// QA finding B: a stopped program whose child still holds its output (a wrapper around ffmpeg) ended
// only when that child did, so its job stayed Running. Its whole group is killed now, and a leftover
// outside the group (setsid) holds the run at most StopWaitDelay.
func TestStopEndsAWrapperWhoseChildHoldsItsOutput(t *testing.T) {
	old := StopWaitDelay
	StopWaitDelay = 500 * time.Millisecond
	defer func() { StopWaitDelay = old }()
	for _, c := range []struct{ name, child string }{{"in its group", "sleep 30"}, {"outside it", "setsid sleep 30"}} {
		t.Run(c.name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "pid")
			script := c.child + " & echo $! > " + pidFile + "; sleep 0.3; echo out_time_ms=1000000; sleep 30" // the child is out of the group first
			ctx, cancel := context.WithCancel(context.Background())
			lines := make(chan string, 10)
			done := make(chan error, 1)
			start := time.Now()
			go func() {
				_, err := OSExec{}.Stream(ctx, func(l string) { lines <- l }, "sh", "-c", script)
				done <- err
			}()
			if l := <-lines; l != "out_time_ms=1000000" { // the wrapper is running, its child started
				t.Fatalf("line %q", l)
			}
			cancel()
			select {
			case err := <-done:
				took := time.Since(start)
				if err == nil || took > 3*time.Second || c.child != "sleep 30" && took < 300*time.Millisecond+StopWaitDelay {
					t.Errorf("ended after %s: %v", took, err) // outside the group: the leftover holds it StopWaitDelay
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the stopped run did not end")
			}
			b, _ := os.ReadFile(pidFile)
			pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			if pid > 0 && c.child == "sleep 30" {
				time.Sleep(100 * time.Millisecond)
				if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil && !zombie(b) {
					t.Errorf("the wrapper's child %d is still running", pid)
				}
			}
			if pid > 0 {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		})
	}
}
