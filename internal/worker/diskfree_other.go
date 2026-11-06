// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !unix && !windows

package worker

import "errors"

// diskFree is not implemented on this OS; the caller treats that as "cannot check" and keeps the original.
func diskFree(dir string) (uint64, error) {
	return 0, errors.New("free disk space cannot be checked on this operating system")
}
