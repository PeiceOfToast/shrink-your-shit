// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build windows

package fsx

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// openForIdentity opens path for its file information only. The share modes let a file that is
// still being written, or opened by something else, be inspected without getting in its way.
func openForIdentity(path string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
}

// fileInfo is the handle query both FileID and LinkCount use.
func fileInfo(path string) (*windows.ByHandleFileInformation, error) {
	h, err := openForIdentity(path)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return nil, fmt.Errorf("read the file information of %s: %w", path, err)
	}
	return &info, nil
}

// FileID identifies the file by its volume and file index, so a renamed file keeps its id.
func FileID(path string) (string, error) {
	info, err := fileInfo(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%08x:%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}

// LinkCount is how many names point at the file; more than one means a hard link we leave alone.
func LinkCount(path string) (int, error) {
	info, err := fileInfo(path)
	if err != nil {
		return 0, err
	}
	return int(info.NumberOfLinks), nil
}
