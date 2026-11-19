//go:build !windows

// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import "errors"

var errNotWindows = errors.New("the Windows update runs on Windows only")

func startDetached(exe string, args []string) error { return errNotWindows }

func stopProcess(pid int) error { return errNotWindows }
