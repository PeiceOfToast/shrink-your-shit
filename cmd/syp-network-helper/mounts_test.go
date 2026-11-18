//go:build linux

// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withMnt points /mnt, the mount table and the helper's record at temp places for one test. Nothing here mounts.
func withMnt(t *testing.T) (mnt, table string) {
	t.Helper()
	mnt = filepath.Join(t.TempDir(), "mnt")
	if err := os.Mkdir(mnt, 0o755); err != nil {
		t.Fatal(err)
	}
	table = filepath.Join(t.TempDir(), "mounts")
	if err := os.WriteFile(table, nil, 0o644); err != nil { // an empty mount table: nothing is mounted
		t.Fatal(err)
	}
	oldMnt, oldTable, oldRecord, oldOwner := mntDir, procMounts, helperMounts, rootOwner
	mntDir, procMounts, helperMounts, rootOwner = mnt, table, filepath.Join(t.TempDir(), "helper-mounts"), os.Getuid()
	t.Cleanup(func() { mntDir, procMounts, helperMounts, rootOwner = oldMnt, oldTable, oldRecord, oldOwner })
	return mnt, table
}

func writeTable(t *testing.T, table string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(table, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMountPointUnderMnt(t *testing.T) {
	for _, mp := range []string{"/mnt/media", "/mnt/nas-media_2", "/mnt/a.b"} {
		if err := validateMountPoint(mp); err != nil {
			t.Errorf("%s refused: %v", mp, err)
		}
	}
	for _, mp := range []string{"/mnt", "/mnt/", "/mnt/a/b", "/mnt/../etc", "/mnt/./x", "/mnt//x",
		"/mnt/-x", "/mnt/.hidden", "/mnt/Media", "/mntX/x", "/mnt/x/../../etc"} {
		if validateMountPoint(mp) == nil {
			t.Errorf("%s accepted", mp)
		}
	}
}

func TestMntFolderRules(t *testing.T) {
	mnt, _ := withMnt(t)
	me := rootOwner

	mp := filepath.Join(mnt, "media")
	if err := prepareMountPoint(mp, me); err != nil {
		t.Fatalf("fresh folder under /mnt: %v", err)
	}

	// a symlink planted in /mnt is refused, for mount and for unmount/status
	target := t.TempDir()
	evil := filepath.Join(mnt, "evil")
	if err := os.Symlink(target, evil); err != nil {
		t.Fatal(err)
	}
	if prepareMountPoint(evil, me) == nil {
		t.Error("symlink under /mnt accepted for mount")
	}
	if checkExistingMountPoint(evil, me) == nil {
		t.Error("symlink under /mnt accepted for unmount/status")
	}

	// a /mnt that anyone else can write is refused
	if err := os.Chmod(mnt, 0o777); err != nil {
		t.Fatal(err)
	}
	if checkSharesDir(mnt, me) == nil {
		t.Error("/mnt writable by others accepted")
	}
	if err := os.Chmod(mnt, 0o755); err != nil {
		t.Fatal(err)
	}
}

// A mount under /mnt that the helper did not make is the person's own: mount and unmount leave it alone.
func TestForeignMountUnderMntIsLeftAlone(t *testing.T) {
	mnt, table := withMnt(t)
	mp := filepath.Join(mnt, "backup")
	if err := os.Mkdir(mp, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTable(t, table, "//nas/backup "+mp+" cifs rw 0 0")
	req := request{Cmd: "mount", Kind: "cifs", Address: "//nas/backup", MountPoint: mp}

	// These return before any mount or umount runs, so nothing real is touched.
	if got := mount(req, service{}); got.OK || got.Error == nil || !strings.Contains(got.Error.Message, "already mounted") {
		t.Errorf("mount over a foreign mount: %+v", got)
	}
	if got := unmount(mp); got.OK || got.Error == nil || !strings.Contains(got.Error.Message, "left alone") {
		t.Errorf("unmount of a foreign mount: %+v", got)
	}
}

// A mount the helper recorded under /mnt is its own: it counts as connected and can be unmounted.
func TestRecordedMountUnderMntIsOurs(t *testing.T) {
	mnt, table := withMnt(t)
	mp := filepath.Join(mnt, "media")
	if err := os.Mkdir(mp, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTable(t, table, "//nas/media "+mp+" cifs rw 0 0")

	if ownedHere(mp, "cifs", "//nas/media") {
		t.Error("an unrecorded mount counted as the helper's")
	}
	if err := record(mp, "//nas/media"); err != nil {
		t.Fatal(err)
	}
	if !ownedHere(mp, "cifs", "//nas/media") {
		t.Error("a recorded network mount not counted as the helper's")
	}
	if ownedHere(mp, "ext4", "//nas/media") {
		t.Error("a recorded non-network mount counted as the helper's")
	}
	if got := mount(request{Cmd: "mount", Kind: "cifs", Address: "//nas/media", MountPoint: mp}, service{}); !got.OK {
		t.Errorf("mount of an already-connected recorded share: %+v", got)
	}
}

func TestRecordRoundTrip(t *testing.T) {
	_, _ = withMnt(t)
	a, b := "/mnt/a", "/mnt/b"
	if err := record(a, "//nas/a"); err != nil {
		t.Fatal(err)
	}
	if err := record(b, "//nas/b"); err != nil {
		t.Fatal(err)
	}
	if err := record(b, "//nas/b"); err != nil { // recording again keeps one line
		t.Fatal(err)
	}
	if err := forget(a); err != nil {
		t.Fatal(err)
	}
	if recorded(a, "//nas/a") || !recorded(b, "//nas/b") {
		t.Errorf("after forgetting a: a=%v b=%v", recorded(a, "//nas/a"), recorded(b, "//nas/b"))
	}
	if err := forget(b); err != nil {
		t.Fatal(err)
	}
	if err := forget(b); err != nil { // forgetting twice is fine
		t.Fatal(err)
	}
	if recorded(b, "//nas/b") {
		t.Error("b still recorded")
	}
}

// A recorded mount that went away outside the helper leaves its record behind. If the person then mounts their own
// share at the same folder, it is still theirs: not counted as connected, and never unmounted.
func TestStaleRecordDoesNotClaimThePersonsMount(t *testing.T) {
	mnt, table := withMnt(t)
	mp := filepath.Join(mnt, "media")
	if err := os.Mkdir(mp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := record(mp, "//nas/media"); err != nil {
		t.Fatal(err)
	}
	writeTable(t, table, "//person/own "+mp+" cifs rw 0 0")

	if ownedHere(mp, "cifs", "//person/own") {
		t.Error("the person's mount counted as the helper's")
	}
	if got := mount(request{Cmd: "mount", Kind: "cifs", Address: "//nas/media", MountPoint: mp}, service{}); got.OK {
		t.Errorf("mount reported connected over the person's share: %+v", got)
	}
	if got := unmount(mp); got.OK || got.Error == nil || !strings.Contains(got.Error.Message, "left alone") {
		t.Errorf("unmount of the person's mount: %+v", got)
	}
}

// A folder under /mnt that already has files is the person's: a share is not mounted over it.
func TestNonEmptyMntFolderRefused(t *testing.T) {
	mnt, _ := withMnt(t)
	mp := filepath.Join(mnt, "stuff")
	if err := os.Mkdir(mp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mp, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := mount(request{Cmd: "mount", Kind: "cifs", Address: "//nas/stuff", MountPoint: mp}, service{})
	if got.OK || got.Error == nil || !strings.Contains(got.Error.Message, "not empty") {
		t.Errorf("mount over a non-empty folder: %+v", got)
	}
}
