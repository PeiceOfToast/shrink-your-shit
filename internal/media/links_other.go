// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !unix && !windows

package media

import (
	"os"
	"time"
)

// LinkCount is 1 where the system does not say.
func LinkCount(string, os.FileInfo) uint64 { return 1 }

// FileKey: this system does not say which file a name is; ok is false.
func FileKey(string, os.FileInfo) (string, bool) { return "", false }

// FileState: no identity on this system; size and time from fi.
func FileState(_ string, fi os.FileInfo) (string, int64, time.Time, bool) {
	return "", fi.Size(), fi.ModTime(), false
}
