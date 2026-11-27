// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build linux || darwin

package fsx

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// Lock takes an exclusive flock on path and fails at once if another program holds it (LOCK_EX |
// LOCK_NB, L-SAFE-17). The file is created mode 0600 when it is not there yet. The returned func
// releases the lock and closes the file; it is safe to call more than once.
func Lock(path string) (unlock func() error, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the lock file %s: %w", path, err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("%s: %w", path, ErrLocked)
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	var once sync.Once
	var unlockErr error
	return func() error {
		once.Do(func() {
			if err := unix.Flock(int(f.Fd()), unix.LOCK_UN); err != nil {
				unlockErr = fmt.Errorf("unlock %s: %w", path, err)
			}
			if err := f.Close(); err != nil && unlockErr == nil {
				unlockErr = fmt.Errorf("close the lock file %s: %w", path, err)
			}
		})
		return unlockErr
	}, nil
}
