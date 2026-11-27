// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build unix

package media

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRunCancelKillsTheProcessTree proves L-Q-7 on Unix: a cancelled run kills ffmpeg's whole
// process group, not just the process Run started. The shell stands in for ffmpeg (no ffmpeg is
// needed) and records both its own pid and its child's.
func TestRunCancelKillsTheProcessTree(t *testing.T) {
	dir := t.TempDir()
	script := fmt.Sprintf(`sleep 300 & echo $! > %q; echo $$ > %q; sleep 300`,
		filepath.Join(dir, "child.pid"), filepath.Join(dir, "own.pid"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		result Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := Run(ctx, []string{"sh", "-c", script}, nil)
		done <- outcome{result, err}
	}()

	own := waitForPID(t, filepath.Join(dir, "own.pid"))
	child := waitForPID(t, filepath.Join(dir, "child.pid"))
	cancel()

	var got outcome
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return within 10s of the cancel")
	}
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", got.err)
	}
	if pid := got.result.PID; pid != own {
		t.Errorf("Run reported pid %d, the shell said %d", pid, own)
	}
	for name, pid := range map[string]int{"shell": own, "child": child} {
		waitUntilGone(t, name, pid)
	}
}

// TestRunReturnsWhenAChildKeepsTheProgressPipe: the program exits but leaves a child holding
// stdout. Waiting for the read to end would hang Run until something cancelled it.
func TestRunReturnsWhenAChildKeepsTheProgressPipe(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	script := fmt.Sprintf(`sleep 300 & echo $! > %q; exit 0`, pidFile)
	t.Cleanup(func() {
		if data, err := os.ReadFile(pidFile); err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})

	done := make(chan error, 1)
	go func() {
		_, err := Run(context.Background(), []string{"sh", "-c", script}, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v, want success after the program exited", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run waited for a child that kept its progress pipe open")
	}

	pid := waitForPID(t, pidFile)
	waitUntilGone(t, "the child holding the pipe", pid)
}

// TestRunReportsAFailingExit keeps the exit code and the end of stderr for the failure card.
func TestRunReportsAFailingExit(t *testing.T) {
	result, err := Run(context.Background(), []string{"sh", "-c", "echo 'boom: bad input' >&2; exit 3"}, nil)
	if err == nil {
		t.Fatal("a failing program returned no error")
	}
	if result.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", result.ExitCode)
	}
	if len(result.KeyLines) != 1 || !strings.Contains(result.KeyLines[0], "boom: bad input") {
		t.Errorf("key lines = %q, want the stderr line", result.KeyLines)
	}
	if result.Duration <= 0 {
		t.Errorf("duration = %v, want > 0", result.Duration)
	}
}

// TestRunKeepsTheLastKeyLines checks the 50-line cap that the stderr ring promises.
func TestRunKeepsTheLastKeyLines(t *testing.T) {
	result, err := Run(context.Background(), []string{"sh", "-c", `for i in $(seq 1 80); do echo "line $i" >&2; done`}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.KeyLines) != keyLineCount {
		t.Fatalf("key lines = %d, want %d", len(result.KeyLines), keyLineCount)
	}
	if first := result.KeyLines[0]; first != "line 31" {
		t.Errorf("first kept line = %q, want line 31 (the last 50 of 80)", first)
	}
	if last := result.KeyLines[len(result.KeyLines)-1]; last != "line 80" {
		t.Errorf("last kept line = %q, want line 80", last)
	}
}

// waitForPID waits until a script has written its pid.
func waitForPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if convErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no pid in %s", path)
	return 0
}

// isProcessRunning asks signal 0 whether pid still exists.
func isProcessRunning(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// waitUntilGone waits until signal 0 says no such process.
func waitUntilGone(t *testing.T, name string, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("%s process %d is still alive after the cancel", name, pid)
}
