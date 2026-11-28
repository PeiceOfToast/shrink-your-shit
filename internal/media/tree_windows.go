// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build windows

package media

import (
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// openProcess is windows.OpenProcess, a variable so the "Windows will not hand us the process"
// fallback can be tested. Tests set it and restore it; none run in parallel.
var openProcess = windows.OpenProcess

// tree is a started process and the job object that holds it and its children. fallback ends the
// whole tree without a job object: taskkill walks the children, and cmd.Process.Kill is the last
// resort because after Start the command always keeps its own process handle.
type tree struct {
	job      windows.Handle
	proc     windows.Handle
	pid      int
	fallback func() error
}

// startTree starts cmd in a job object with kill-on-close, so killing the job also kills every
// child ffmpeg starts (L-Q-7). A runner that refuses job objects (a nested job without breakaway
// rights) falls back to walking the tree with taskkill; Run keeps the same promise there.
func startTree(cmd *exec.Cmd) (*tree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	if LowPriority() {
		// Windows' low priority is a process creation flag, so it is set before the tool starts.
		cmd.SysProcAttr.PriorityClass = syscall.BELOW_NORMAL_PRIORITY_CLASS
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		job = 0
	} else {
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			windows.CloseHandle(job)
			job = 0
		}
	}
	if err := cmd.Start(); err != nil {
		if job != 0 {
			windows.CloseHandle(job)
		}
		return nil, err
	}
	t := &tree{job: job, pid: cmd.Process.Pid, fallback: killTree(cmd.Process.Pid, cmd.Process.Kill)}
	proc, err := openProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(t.pid))
	if err != nil {
		// Without this handle the process cannot be put in the job either, so the job is empty:
		// close it and let kill walk the tree by pid.
		if job != 0 {
			windows.CloseHandle(job)
			t.job = 0
		}
		return t, nil
	}
	t.proc = proc
	if job != 0 {
		if err := windows.AssignProcessToJobObject(job, proc); err != nil {
			// The job stays empty: kill will fall back to the process handle.
			windows.CloseHandle(job)
			t.job = 0
		}
	}
	return t, nil
}

// kill ends the job (process and children), else walks the tree through taskkill, so a cancel always
// ends the program and what it started. A process handle alone would end only the root: on Windows
// `cmd /c ffmpeg ...` makes ffmpeg a child, and TerminateProcess on cmd.exe would leave it running.
func (t *tree) kill() {
	if t.job != 0 {
		_ = windows.TerminateJobObject(t.job, 1)
		return
	}
	if t.fallback != nil {
		_ = t.fallback()
	}
}

// killTree ends pid and its children through taskkill, which is the tree walk Windows offers
// without a job object. fallback ends the program itself when even that is unavailable.
func killTree(pid int, fallback func() error) func() error {
	return func() error {
		if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run(); err != nil {
			return fallback()
		}
		return nil
	}
}

// release closes the handles. Closing the job also kills anything still in it, which is the same
// "no orphan" promise by another route.
func (t *tree) release() {
	if t.proc != 0 {
		windows.CloseHandle(t.proc)
		t.proc = 0
	}
	if t.job != 0 {
		windows.CloseHandle(t.job)
		t.job = 0
	}
}
