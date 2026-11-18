//go:build linux

// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

// What the helper knows about mounts: what is mounted where, and which mounts under /mnt it made itself.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// procMounts is the kernel's mount table (tests point it at a fake table).
var procMounts = "/proc/self/mounts"

// helperMounts lists the /mnt mount points this helper mounted, one per line. It lives in /run, so it lasts exactly as
// long as the mounts do: a reboot clears both. Tests point it at a temp file.
var helperMounts = "/run/shrink-your-shit/helper-mounts"

// rootOwner is the uid that must own the folders the helper uses (root; tests use their own uid).
var rootOwner = 0

// mountType returns the filesystem type mounted at mp, or "" if nothing is mounted there. The last entry for mp is
// the top mount, the one that counts.
func mountType(mp string) (string, error) {
	data, err := os.ReadFile(procMounts)
	if err != nil {
		return "", err
	}
	typ := ""
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) >= 3 && f[1] == mp {
			typ = f[2]
		}
	}
	return typ, nil
}

// ownedHere says whether the helper may unmount the mount at mp (of type typ), or count it as connected. Its own
// folder, sharesDir, is only ever mounted by the helper. /mnt is the person's too: there only a network mount the
// helper recorded is the helper's.
func ownedHere(mp, typ string) bool {
	if filepath.Dir(mp) == sharesDir {
		return true
	}
	return (typ == "cifs" || typ == "nfs") && recorded(mp)
}

func recorded(mp string) bool {
	data, err := os.ReadFile(helperMounts)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == mp {
			return true
		}
	}
	return false
}

// record notes that the helper mounted mp.
func record(mp string) error {
	if err := os.MkdirAll(filepath.Dir(helperMounts), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(helperMounts, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(f, mp)
	return errors.Join(err, f.Close())
}

// forget drops mp from the record (it is unmounted, or was never there).
func forget(mp string) error {
	data, err := os.ReadFile(helperMounts)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var keep []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" && line != mp {
			keep = append(keep, line)
		}
	}
	out := ""
	if len(keep) > 0 {
		out = strings.Join(keep, "\n") + "\n"
	}
	return os.WriteFile(helperMounts, []byte(out), 0o600)
}
