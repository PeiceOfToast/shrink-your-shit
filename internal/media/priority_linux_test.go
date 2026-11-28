// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build linux

package media

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// procStatFields returns the fields of /proc/<pid>/stat after the process name.
func procStatFields(t *testing.T, pid int) []string {
	t.Helper()
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		t.Fatalf("reading /proc/%d/stat: %v", pid, err)
	}
	s := string(b)
	i := strings.LastIndex(s, ")")
	if i < 0 {
		t.Fatalf("/proc/%d/stat has no process name: %q", pid, s)
	}
	return strings.Fields(s[i+1:])
}

// niceOf is the process's nice value (field 19 of /proc/<pid>/stat).
func niceOf(t *testing.T, pid int) int {
	t.Helper()
	f := procStatFields(t, pid)
	// The fields after ")" start at field 3 (the state), so field 19 is f[16].
	n, err := strconv.Atoi(f[16])
	if err != nil {
		t.Fatalf("nice of %d: %v (%v)", pid, err, f)
	}
	return n
}

// ioprioOf is the process's I/O priority through the same syscall ionice uses. ioprio_get returns the
// priority itself, the way getpriority does.
func ioprioOf(t *testing.T, pid int) int {
	t.Helper()
	prio, _, errno := unix.RawSyscall(unix.SYS_IOPRIO_GET, ioprioWhoProcess, uintptr(pid), 0)
	if errno != 0 {
		t.Fatalf("reading the I/O priority of %d: %v", pid, errno)
	}
	return int(prio)
}

// startSleep starts a process through the same door Run and Capture use, so the priority the tool gets
// is the priority the test reads.
func startSleep(t *testing.T) *tree {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	got, err := startTree(cmd)
	if err != nil {
		t.Fatalf("starting the tool: %v", err)
	}
	t.Cleanup(func() {
		got.kill()
		_ = cmd.Wait()
		got.release()
	})
	return got
}

// With low priority on, a started tool runs at nice 10 and the lowest best-effort I/O priority (page 11's
// "Low priority: on by default").
func TestLowPriorityLowersTheToolProcess(t *testing.T) {
	was := LowPriority()
	t.Cleanup(func() { SetLowPriority(was) })
	SetLowPriority(true)

	tr := startSleep(t)
	if n := niceOf(t, tr.pid); n != 10 {
		t.Errorf("nice of a tool started with low priority on = %d, want 10", n)
	}
	if p := ioprioOf(t, tr.pid); p != ioprioLowest {
		t.Errorf("I/O priority of a tool started with low priority on = %#x, want %#x", p, ioprioLowest)
	}
}

// With low priority off, a started tool keeps the priority this process itself runs at.
func TestLowPriorityOffLeavesTheToolAlone(t *testing.T) {
	was := LowPriority()
	t.Cleanup(func() { SetLowPriority(was) })
	SetLowPriority(false)

	wantNice := niceOf(t, os.Getpid())
	wantPrio := ioprioOf(t, os.Getpid())
	tr := startSleep(t)
	if n := niceOf(t, tr.pid); n != wantNice {
		t.Errorf("nice of a tool started with low priority off = %d, want %d", n, wantNice)
	}
	if p := ioprioOf(t, tr.pid); p != wantPrio {
		t.Errorf("I/O priority of a tool started with low priority off = %#x, want %#x", p, wantPrio)
	}
}
