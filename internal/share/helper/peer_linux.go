// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build linux

package helper

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerUID reads the uid the kernel vouches for on the other end of a unix socket. It is
// SO_PEERCRED, not anything the client says about itself.
func peerUID(conn *net.UnixConn) (uid uint32, err error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var ucred *unix.Ucred
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		ucred, sockErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if sockErr != nil {
		return 0, sockErr
	}
	uid = ucred.Uid
	return uid, nil
}
