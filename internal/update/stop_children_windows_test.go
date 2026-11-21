// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// startSleeper starts this test binary as a child that sleeps until it is killed (see TestSleepHelper).
func startSleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSleepHelper$")
	cmd.Env = append(os.Environ(), "SYP_SLEEP_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	return cmd
}

// TestSleepHelper is not a test of its own: it is the child that TestStopChildrenSparesTheKeptProcess starts.
func TestSleepHelper(t *testing.T) {
	if os.Getenv("SYP_SLEEP_HELPER") == "" {
		t.Skip("only runs as a child of the test above")
	}
	time.Sleep(10 * time.Minute)
}

// The app's children are ended; the process to keep (the watcher, the app's own child too) is not. Both
// are children of this test process, which stands in for the app.
func TestStopChildrenSparesTheKeptProcess(t *testing.T) {
	started, err := appStarted(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	child := startSleeper(t)
	kept := startSleeper(t)
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	if err := stopChildren(os.Getpid(), kept.Process.Pid, started); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the child was not ended")
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(kept.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if st, err := windows.WaitForSingleObject(h, 0); err != nil || st != uint32(windows.WAIT_TIMEOUT) {
		t.Errorf("the kept process ended: status %v, err %v", st, err)
	}
}

// A process that names the app as its parent but was created before the app (a reused PID) is not the app's
// child: it is not ended. The app here is "started" an hour from now, so this child is older than it.
func TestStopChildrenSparesAChildOlderThanTheApp(t *testing.T) {
	child := startSleeper(t)
	if err := stopChildren(os.Getpid(), 0, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(child.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if st, err := windows.WaitForSingleObject(h, 0); err != nil || st != uint32(windows.WAIT_TIMEOUT) {
		t.Errorf("a child older than the app was ended: status %v, err %v", st, err)
	}
}
