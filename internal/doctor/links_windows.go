// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build windows

package doctor

import (
	"os"
	"syscall"
)

// linkCount is how many names the file has (NTFS hardlinks), read through an open handle.
func linkCount(path string, _ os.FileInfo) uint64 {
	f, err := os.Open(path)
	if err != nil {
		return 1
	}
	defer f.Close()
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &info); err != nil {
		return 1
	}
	return uint64(info.NumberOfLinks)
}
