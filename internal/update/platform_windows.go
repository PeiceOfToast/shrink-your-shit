//go:build windows

// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// detachedProcess is the DETACHED_PROCESS creation flag: the started program has no console of its own.
const detachedProcess = 0x00000008

// startDetached starts exe with args in its own process group, not waited for. The watcher outlives the app.
func startDetached(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess, HideWindow: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// stopChildren ends the processes whose parent is pid and that pid started (started is pid's own start time, read
// while it was alive), except keep. They are the app's children (its ffmpeg and ffprobe runs); a child keeps the
// app's PID as its parent after the app ends, and a reused PID can name the app too (see ownChildren). keep is the
// watcher, which is also the app's child, so it is never ended. A child that is already gone is not an error.
func stopChildren(pid, keep int, started time.Time) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snap)
	e := windows.ProcessEntry32{}
	e.Size = uint32(unsafe.Sizeof(e))
	var cands []childCandidate
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if int(e.ParentProcessID) == pid {
			created, cerr := processCreated(int(e.ProcessID))
			cands = append(cands, childCandidate{pid: int(e.ProcessID), created: created, err: cerr})
		}
	}
	// A walk that failed partway still ends the children it found; its error is reported after them.
	var walkErr error
	if err != windows.ERROR_NO_MORE_FILES {
		walkErr = err
	}
	return endChildren(ownChildren(cands, started, keep), walkErr, stopProcess)
}

// appStarted is when process pid was created. It must be called while pid is alive: the start time of a process
// that has ended cannot be read, and its PID may be reused by then.
func appStarted(pid int) (time.Time, error) {
	return processCreated(pid)
}

// processCreated is when process pid was created; an error when it cannot be read (a process this one may not open).
func processCreated(pid int) (time.Time, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return time.Time{}, err
	}
	defer windows.CloseHandle(h)
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, created.Nanoseconds()), nil
}

// stopProcess ends one process and waits for it to exit. It is not a tree kill: the watcher is the app's child,
// and the installer's own tree kill would take the watcher with the app. A process already gone is not an error.
func stopProcess(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(pid))
	if err == windows.ERROR_INVALID_PARAMETER { // no such process
		return nil
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		return err
	}
	event, err := windows.WaitForSingleObject(h, uint32(stopWait.Milliseconds()))
	return exitWaitError(pid, event, err)
}
