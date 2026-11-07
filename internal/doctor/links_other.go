// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !unix

package doctor

import "os"

// linkCount: not read on this system (Windows hardlinks are rare in media libraries); 1.
func linkCount(os.FileInfo) uint64 { return 1 }
