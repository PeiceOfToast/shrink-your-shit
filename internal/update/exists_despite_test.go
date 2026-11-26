// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"fmt"
	"io/fs"
	"os"
	"testing"
)

// Review 747: a process we may not open still exists; any other FindProcess error means it is gone.
func TestExistsDespiteOnlyAccessDenied(t *testing.T) {
	if !existsDespite(&os.SyscallError{Syscall: "OpenProcess", Err: fmt.Errorf("denied: %w", fs.ErrPermission)}) {
		t.Error("access denied: want exists")
	}
	if existsDespite(&os.SyscallError{Syscall: "OpenProcess", Err: fs.ErrNotExist}) || existsDespite(fmt.Errorf("invalid parameter")) {
		t.Error("other errors: want gone")
	}
}
