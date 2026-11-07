// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !unix && !windows

package media

import "os"

// LinkCount is 1 where the system does not say.
func LinkCount(string, os.FileInfo) uint64 { return 1 }

// FileKey: this system does not say which file a name is; ok is false.
func FileKey(string, os.FileInfo) (string, bool) { return "", false }
