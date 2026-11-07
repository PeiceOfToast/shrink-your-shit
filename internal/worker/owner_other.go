// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !unix

package worker

import "os"

// keepOwner: Windows gives a new file the folder's inherited permissions and owner; there is nothing to copy.
func keepOwner(string, os.FileInfo) error { return nil }
