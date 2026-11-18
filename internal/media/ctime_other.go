// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !linux && !darwin

package media

import (
	"os"
	"time"
)

// ChangeTime: no status change time is read on this system (Windows does not report one through
// os.FileInfo), so the source check relies on file identity, size and modification time.
func ChangeTime(os.FileInfo) (time.Time, bool) { return time.Time{}, false }
