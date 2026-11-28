// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !linux

package safety

import "os"

// ctimeOf reports no change time here: the identity check uses the file identity, size and mtime alone on this OS.
func ctimeOf(os.FileInfo) int64 { return 0 }
