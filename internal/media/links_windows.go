// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// LinkCount is how many names the file has, from NumberOfLinks (os.FileInfo does not carry it on
// Windows, so the file is opened for its attributes; it is not read).
func LinkCount(path string, _ os.FileInfo) uint64 {
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

// FileKey identifies the file itself (volume serial number and file index, read from the file: an
// os.FileInfo from a directory listing does not carry them on Windows), so a file made again under the
// same name is told apart; ok is false when it cannot be read.
func FileKey(path string, _ os.FileInfo) (key string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &info); err != nil {
		return "", false
	}
	return fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), true
}

// FileState is the file itself as Windows keeps it: its identity (as FileKey), and its size and last
// write time read through the file's handle, not from the directory listing, whose copy NTFS updates
// lazily and which can lag behind (a file looking changed between two scans of it untouched).
func FileState(path string, _ os.FileInfo) (key string, size int64, mtime time.Time, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, time.Time{}, false
	}
	defer f.Close()
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &info); err != nil {
		return "", 0, time.Time{}, false
	}
	key = fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)
	size = int64(info.FileSizeHigh)<<32 | int64(info.FileSizeLow)
	return key, size, time.Unix(0, info.LastWriteTime.Nanoseconds()), true
}
