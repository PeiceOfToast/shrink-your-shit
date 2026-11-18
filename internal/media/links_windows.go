// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
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

// FileState is the file itself as Windows keeps it, read through the file's handle, not from the
// directory listing (whose copy NTFS updates lazily, so an untouched file could look changed between two
// scans): its identity (as FileKey), size, last write time, creation time and change time (FILE_BASIC_INFO;
// every write, rename or change of its attributes or times moves it). A file made again under the same
// name can get the old one's file index, and, through NTFS tunneling, its creation time, but writing it
// moves its change time (review 3ea035c). An error when the
// file cannot be opened or asked (a download holding it locked, for one).
func FileState(path string, _ os.FileInfo) (State, error) {
	f, err := os.Open(path)
	if err != nil {
		return State{}, err
	}
	defer f.Close()
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &info); err != nil {
		return State{}, err
	}
	var basic fileBasicInfo
	if err := windows.GetFileInformationByHandleEx(windows.Handle(f.Fd()), windows.FileBasicInfo, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))); err != nil {
		return State{}, err
	}
	return State{
		Key:     fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow),
		Size:    int64(info.FileSizeHigh)<<32 | int64(info.FileSizeLow),
		MTime:   time.Unix(0, info.LastWriteTime.Nanoseconds()),
		Born:    time.Unix(0, info.CreationTime.Nanoseconds()),
		Changed: fileTime(basic.ChangeTime),
	}, nil
}

// fileBasicInfo is FILE_BASIC_INFO: its times are FILETIMEs (100 ns since 1601) as LARGE_INTEGERs.
type fileBasicInfo struct {
	CreationTime, LastAccessTime, LastWriteTime, ChangeTime int64
	FileAttributes                                          uint32
	_                                                       uint32
}

// fileTime is a FILETIME count (100 ns since 1601) as a time.
func fileTime(ft int64) time.Time {
	t := windows.Filetime{LowDateTime: uint32(ft), HighDateTime: uint32(ft >> 32)}
	return time.Unix(0, t.Nanoseconds())
}
