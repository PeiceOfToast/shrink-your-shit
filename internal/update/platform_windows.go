//go:build windows

// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"

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
	if _, err := windows.WaitForSingleObject(h, uint32((30 * time.Second).Milliseconds())); err != nil {
		return fmt.Errorf("process %d did not exit: %w", pid, err)
	}
	return nil
}
