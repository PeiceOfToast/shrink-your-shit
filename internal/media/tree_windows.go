// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build windows

package media

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// tree is a started process and the job object that holds it and its children.
type tree struct {
	job  windows.Handle
	proc windows.Handle
	pid  int
}

// startTree starts cmd in a job object with kill-on-close, so killing the job also kills every
// child ffmpeg starts (L-Q-7). A runner that refuses job objects (a nested job without breakaway
// rights) falls back to killing the process itself; Run still works, only the tree guarantee is
// weaker there.
func startTree(cmd *exec.Cmd) (*tree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
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
	t := &tree{job: job, pid: cmd.Process.Pid}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(t.pid))
	if err != nil {
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

// kill ends the job (process and children), or just the process when no job is in force.
func (t *tree) kill() {
	if t.job != 0 {
		_ = windows.TerminateJobObject(t.job, 1)
		return
	}
	if t.proc != 0 {
		_ = windows.TerminateProcess(t.proc, 1)
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
