// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build windows

package media

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// startWithReportedChild starts a command that runs a long ping and reports the ping's pid, so a
// test can check that the whole tree died and not just the wrapper. It needs no ffmpeg (PowerShell,
// ping and taskkill are part of Windows) and runs in CI's Windows job.
func startWithReportedChild(t *testing.T) (*exec.Cmd, *tree, int) {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	script := fmt.Sprintf(
		"$p = Start-Process -FilePath ping -ArgumentList '-n','60','127.0.0.1' -PassThru -WindowStyle Hidden; "+
			"Set-Content -LiteralPath '%s' -Value $p.Id; Wait-Process -Id $p.Id",
		strings.ReplaceAll(pidFile, "'", "''"))
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	started, err := startTree(cmd)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	return cmd, started, waitForChildPID(t, pidFile)
}

// waitForChildPID waits for the wrapper to write the child's pid. PowerShell starts slowly on a
// busy runner, so the wait is generous.
func waitForChildPID(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the child never reported its pid")
	return 0
}

// TestJobObjectKillsTheProcessTree is the Windows unit test of the kill mechanism L-Q-7 asks for:
// the process tree is ended by terminating the job object it was started in. The ping child is
// checked too, so killing only the wrapper cannot pass.
func TestJobObjectKillsTheProcessTree(t *testing.T) {
	cmd, tree, child := startWithReportedChild(t)
	if tree.job == 0 {
		// A runner that already holds this process in a job without breakaway rights refuses the
		// assignment; startTree then falls back to walking the tree with taskkill, and Run still
		// stops ffmpeg and its children.
		t.Logf("job object unavailable; testing the taskkill tree fallback")
	}
	tree.kill()
	if err := cmd.Wait(); err == nil {
		t.Fatal("the wrapper ended by itself; the test proved nothing")
	}
	tree.release()

	waitUntilGone(t, child)
}

// TestCancelFallsBackWhenWindowsGivesNoProcessHandle is finding 1 of review 801: when OpenProcess
// fails, the job cannot hold the process either, so kill must still end the tree rather than leave
// ffmpeg or its children running.
func TestCancelFallsBackWhenWindowsGivesNoProcessHandle(t *testing.T) {
	old := openProcess
	openProcess = func(access uint32, inheritHandle bool, pid uint32) (windows.Handle, error) {
		return windows.InvalidHandle, windows.ERROR_ACCESS_DENIED
	}
	t.Cleanup(func() { openProcess = old })

	cmd, tree, child := startWithReportedChild(t)
	if tree.job != 0 || tree.proc != 0 {
		t.Fatalf("job %d, process handle %d; want no handles after a refused OpenProcess", tree.job, tree.proc)
	}
	if tree.fallback == nil {
		t.Fatal("no fallback kill: a cancel would leave the program running")
	}
	tree.kill()
	if err := cmd.Wait(); err == nil {
		t.Fatal("the wrapper ended by itself; the test proved nothing")
	}
	tree.release()

	waitUntilGone(t, child)
}

// waitUntilGone fails if pid still exists 10s after the tree was killed.
func waitUntilGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !isProcessRunning(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("process %d is still alive after the tree was killed", pid)
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
