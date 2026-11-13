// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build windows

package web

import (
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

const (
	resourceTypeDisk  = 1    // RESOURCETYPE_DISK
	errorNotConnected = 2250 // ERROR_NOT_CONNECTED: nothing to disconnect
)

var (
	mpr               = syscall.NewLazyDLL("mpr.dll")
	procAddConnection = mpr.NewProc("WNetAddConnection2W")
	procCancelConnect = mpr.NewProc("WNetCancelConnection2W")
)

// netResourceW is NETRESOURCEW (winnetwk.h), for a disk share with no drive letter.
type netResourceW struct {
	Scope, Type, DisplayType, Usage          uint32
	LocalName, RemoteName, Comment, Provider *uint16
}

// winConnectSys connects unc as user (deviceless, so no drive letter is taken). An empty user and
// password is a guest connection. The password is never logged.
func winConnectSys(unc, user, password string) error {
	remote, err := syscall.UTF16PtrFromString(unc)
	if err != nil {
		return err
	}
	nr := netResourceW{Type: resourceTypeDisk, RemoteName: remote}
	var userP, passP *uint16
	if user != "" {
		if userP, err = syscall.UTF16PtrFromString(user); err != nil {
			return err
		}
	}
	if password != "" {
		if passP, err = syscall.UTF16PtrFromString(password); err != nil {
			return err
		}
	}
	r, _, _ := procAddConnection.Call(uintptr(unsafe.Pointer(&nr)),
		uintptr(unsafe.Pointer(passP)), uintptr(unsafe.Pointer(userP)), 0)
	runtime.KeepAlive(remote)
	runtime.KeepAlive(userP)
	runtime.KeepAlive(passP)
	if r != 0 {
		return winErr(uint32(r))
	}
	return nil
}

// winDisconnectSys drops the connection to unc, forcing open files closed.
func winDisconnectSys(unc string) error {
	name, err := syscall.UTF16PtrFromString(unc)
	if err != nil {
		return err
	}
	r, _, _ := procCancelConnect.Call(uintptr(unsafe.Pointer(name)), 0, 1)
	runtime.KeepAlive(name)
	if r != 0 && uint32(r) != errorNotConnected {
		return winErr(uint32(r))
	}
	return nil
}

func winErr(code uint32) *winError {
	return &winError{Code: code, Words: formatWinError(code)}
}

// formatWinError is the system's text for a Windows error code, or "" when it has none.
func formatWinError(code uint32) string {
	buf := make([]uint16, 512)
	n, _ := syscall.FormatMessage(syscall.FORMAT_MESSAGE_FROM_SYSTEM|syscall.FORMAT_MESSAGE_IGNORE_INSERTS,
		0, code, 0, buf, nil)
	if n == 0 {
		return ""
	}
	return strings.TrimSpace(syscall.UTF16ToString(buf[:n]))
}
