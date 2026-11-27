// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build windows

package fsx

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"golang.org/x/sys/windows"
)

// Lock takes an exclusive LockFileEx lock on path and fails at once if another program holds it
// (FAIL_IMMEDIATELY, L-SAFE-17). The returned func releases the lock and closes the file; it is
// safe to call more than once.
func Lock(path string) (unlock func() error, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the lock file %s: %w", path, err)
	}
	handle := windows.Handle(f.Fd())
	var ol windows.Overlapped
	err = windows.LockFileEx(handle,
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
	if err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, fmt.Errorf("%s: %w", path, ErrLocked)
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	var once sync.Once
	var unlockErr error
	return func() error {
		once.Do(func() {
			if err := windows.UnlockFileEx(handle, 0, 1, 0, &ol); err != nil {
				unlockErr = fmt.Errorf("unlock %s: %w", path, err)
			}
			if err := f.Close(); err != nil && unlockErr == nil {
				unlockErr = fmt.Errorf("close the lock file %s: %w", path, err)
			}
		})
		return unlockErr
	}, nil
}
