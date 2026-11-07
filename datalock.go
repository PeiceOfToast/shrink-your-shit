// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"errors"
	"os"
	"path/filepath"
)

// errDataLocked: another program holds the data folder's lock (the service, or a restore --force).
var errDataLocked = errors.New("the data folder is in use by another shrink-your-shit")

// lockDataDir takes the exclusive lock on <data>/syp.lock (review 34cb07f). The service holds it for
// its whole life; restore --force takes the same lock for the whole restore, so the two never move the
// same files and rows at once, whatever address the service listens on. The lock goes with the
// process (flock on Linux and macOS, LockFileEx on Windows), so a crash never leaves it stuck.
func lockDataDir(dir string) (release func(), err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "syp.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return func() { unlockFile(f); f.Close() }, nil
}
