// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build windows

package jobs

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// On Windows the share key file holds the key wrapped by DPAPI for the logged-in person, so only that
// person's session can read it back. The app runs as that person (installer Run key at logon).
var (
	crypt32         = syscall.NewLazyDLL("crypt32.dll")
	procProtectData = crypt32.NewProc("CryptProtectData")
	procUnprotect   = crypt32.NewProc("CryptUnprotectData")
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procLocalFree   = kernel32.NewProc("LocalFree")
)

// dataBlob is DATA_BLOB (wincrypt.h).
type dataBlob struct {
	cbData uint32
	pbData *byte
}

func wrapShareKey(key []byte) ([]byte, error) { return dpapi(procProtectData, key) }
func unwrapShareKey(b []byte) ([]byte, error) { return dpapi(procUnprotect, b) }

// dpapi runs CryptProtectData or CryptUnprotectData for this user (no entropy, no prompt).
func dpapi(proc *syscall.LazyProc, in []byte) ([]byte, error) {
	var inBlob dataBlob
	if len(in) > 0 {
		inBlob = dataBlob{cbData: uint32(len(in)), pbData: &in[0]}
	}
	var out dataBlob
	r, _, e := proc.Call(uintptr(unsafe.Pointer(&inBlob)), 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&out)))
	runtime.KeepAlive(in)
	if r == 0 {
		return nil, fmt.Errorf("share key: DPAPI: %w", e)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	res := make([]byte, out.cbData)
	copy(res, unsafe.Slice(out.pbData, out.cbData))
	return res, nil
}
