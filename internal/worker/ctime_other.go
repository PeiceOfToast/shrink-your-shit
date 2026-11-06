// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !linux && !darwin

package worker

import (
	"os"
	"time"
)

// changeTime: no status change time is read on this system (Windows does not report one through
// os.FileInfo), so the source check relies on file identity, size and modification time.
func changeTime(os.FileInfo) (time.Time, bool) { return time.Time{}, false }
