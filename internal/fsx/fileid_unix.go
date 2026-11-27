// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build unix

package fsx

import (
	"fmt"
	"os"
	"syscall"
)

// fileStat is a file's stat with the two parts the catalog needs: the identity that survives a
// rename and how many names point at it.
func fileStat(path string) (*syscall.Stat_t, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, fmt.Errorf("read %s: the system does not describe a Unix file", path)
	}
	return st, nil
}

// FileID identifies the file by its device and inode, so a renamed file keeps its id.
func FileID(path string) (string, error) {
	st, err := fileStat(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", uint64(st.Dev), uint64(st.Ino)), nil
}

// LinkCount is how many names point at the file; more than one means a hard link we leave alone.
func LinkCount(path string) (int, error) {
	st, err := fileStat(path)
	if err != nil {
		return 0, err
	}
	return int(st.Nlink), nil
}
