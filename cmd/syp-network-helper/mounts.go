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

// helperMounts lists the /mnt mounts this helper made, one "<source> <mount point>" per line, with the source as the
// mount table shows it. It lives in /run, so it lasts exactly as
// long as the mounts do: a reboot clears both. Tests point it at a temp file.
var helperMounts = "/run/shrink-your-shit/helper-mounts"

// rootOwner is the uid that must own the folders the helper uses (root; tests use their own uid).
var rootOwner = 0

// mountEntry returns the filesystem type and source mounted at mp, or "" if nothing is mounted there. The last entry
// for mp is the top mount, the one that counts.
func mountEntry(mp string) (typ, src string, err error) {
	data, err := os.ReadFile(procMounts)
	if err != nil {
		return "", "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) >= 3 && f[1] == mp {
			typ, src = f[2], f[0]
		}
	}
	return typ, src, nil
}

// ownedHere says whether the helper may unmount the mount at mp (of type typ, from src), or count it as connected.
// Its own folder, sharesDir, is only ever mounted by the helper. /mnt is the person's too: there only a network mount
// the helper recorded, from the same source, is the helper's. The source check matters when a recorded mount went
// away outside the helper and the person mounted their own share at the same folder.
func ownedHere(mp, typ, src string) bool {
	if filepath.Dir(mp) == sharesDir {
		return true
	}
	return (typ == "cifs" || typ == "nfs") && recorded(mp, src)
}

func recorded(mp, src string) bool {
	data, err := os.ReadFile(helperMounts)
	if err != nil || src == "" {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == src+" "+mp {
			return true
		}
	}
	return false
}

// record notes that the helper mounted src at mp.
func record(mp, src string) error {
	if err := forget(mp); err != nil { // one line per mount point
		return err
	}
	if err := os.MkdirAll(filepath.Dir(helperMounts), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(helperMounts, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(f, src, mp)
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
		if f := strings.Fields(line); len(f) == 2 && f[1] != mp {
			keep = append(keep, line)
		}
	}
	out := ""
	if len(keep) > 0 {
		out = strings.Join(keep, "\n") + "\n"
	}
	return os.WriteFile(helperMounts, []byte(out), 0o600)
}
