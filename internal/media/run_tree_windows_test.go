// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build windows

package media

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

// TestJobObjectKillsTheProcessTree is the Windows unit test of the kill mechanism L-Q-7 asks for:
// the process tree is ended by terminating the job object it was started in. It needs no ffmpeg
// (the system's cmd.exe and ping stand in) and runs in CI's Windows job.
func TestJobObjectKillsTheProcessTree(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "ping -n 60 127.0.0.1 > NUL")
	tree, err := startTree(cmd)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if tree.job == 0 {
		// A runner that already holds this process in a job without breakaway rights refuses the
		// assignment; startTree then falls back to the process handle, and Run still stops ffmpeg.
		t.Logf("job object unavailable; testing the process-handle fallback")
	}
	pid := cmd.Process.Pid
	tree.kill()
	if err := cmd.Wait(); err == nil {
		t.Fatal("cmd.exe ended by itself; the test proved nothing")
	}
	tree.release()

	if isProcessRunning(pid) {
		t.Fatalf("process %d is still alive after the job was terminated", pid)
	}
}

// isProcessRunning asks for a handle to pid; success means it still exists.
func isProcessRunning(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	windows.CloseHandle(handle)
	return true
}
