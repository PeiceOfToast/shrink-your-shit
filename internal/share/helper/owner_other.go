// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !linux

package helper

import "errors"

// checkOwnedPath has no owner information to check away from Linux, so it refuses.
func checkOwnedPath(path string, owner uint32, top string) error {
	return errors.New("the helper runs on Linux only")
}
