// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !unix && !windows

package doctor

import "os"

// linkCount: not read on this system; 1.
func linkCount(string, os.FileInfo) uint64 { return 1 }
