// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"os"
	"syscall"
	"testing"
)

// The real errors FindProcess wraps on Windows: ERROR_ACCESS_DENIED (5) exists, ERROR_INVALID_PARAMETER (87) is gone.
func TestExistsDespiteWindowsErrnos(t *testing.T) {
	if !existsDespite(os.NewSyscallError("OpenProcess", syscall.ERROR_ACCESS_DENIED)) {
		t.Error("ERROR_ACCESS_DENIED: want exists")
	}
	if existsDespite(os.NewSyscallError("OpenProcess", syscall.Errno(87))) {
		t.Error("ERROR_INVALID_PARAMETER: want gone")
	}
}
