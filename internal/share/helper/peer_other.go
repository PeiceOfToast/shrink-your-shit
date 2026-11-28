// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !linux

package helper

import (
	"errors"
	"net"
)

// peerUID has no trustworthy answer away from Linux, so the helper refuses every peer instead of
// guessing. The helper service only exists on Linux.
func peerUID(*net.UnixConn) (uint32, error) {
	return 0, errors.New("peer credentials are only available on Linux")
}
