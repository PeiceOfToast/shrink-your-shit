// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build linux

package helper

import (
	"os"
	"path/filepath"
	"testing"
)

// ownerOnlyDir is a fresh folder only this test's user can write, whatever the umask.
func ownerOnlyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A path is refused when anything on it up to top can be written by another user, sticky or not:
// that user could swap a folder for a link and steer the helper's mkdir, chown and mount elsewhere.
func TestCheckOwnedPath(t *testing.T) {
	me := uint32(os.Getuid())
	top := ownerOnlyDir(t)
	base := filepath.Join(top, "lib")
	shares := filepath.Join(base, "shares")
	if err := os.MkdirAll(shares, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkOwnedPath(shares, me, top); err != nil {
		t.Fatalf("an owner-only path refused: %v", err)
	}
	if me != 0 {
		if err := checkOwnedPath(shares, me+1, top); err == nil {
			t.Error("a path owned by another uid passed")
		}
	}
	for _, mode := range []os.FileMode{0o775, 0o757, os.ModeSticky | 0o777} {
		if err := os.Chmod(base, mode); err != nil {
			t.Fatal(err)
		}
		if err := checkOwnedPath(shares, me, top); err == nil {
			t.Errorf("a parent with mode %v passed", mode)
		}
	}
	link := filepath.Join(top, "link")
	if err := os.Symlink(shares, link); err != nil {
		t.Fatal(err)
	}
	if err := checkOwnedPath(link, me, top); err == nil {
		t.Error("a symbolic link passed")
	}
}

// prepareDir checks before it acts: under a parent others can write it refuses without making,
// chowning or chmodding anything, and an existing folder it doesn't trust is left as it was.
func TestPrepareDirChecksFirst(t *testing.T) {
	uid, gid := os.Getuid(), os.Getgid()
	top := ownerOnlyDir(t)
	open := filepath.Join(top, "open")
	if err := os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareDir(filepath.Join(open, "shares"), 0o750, uid, gid, top); err == nil {
		t.Error("a folder under a writable parent was prepared")
	}
	if _, err := os.Lstat(filepath.Join(open, "shares")); !os.IsNotExist(err) {
		t.Errorf("the refused folder was made anyway: %v", err)
	}

	loose := filepath.Join(top, "loose")
	if err := os.Mkdir(loose, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o770); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareDir(loose, 0o750, uid, gid, top); err == nil {
		t.Error("an existing group-writable folder was adopted")
	}
	if fi, _ := os.Stat(loose); fi.Mode().Perm() != 0o770 {
		t.Errorf("the refused folder's mode changed to %v", fi.Mode().Perm())
	}

	link := filepath.Join(top, "link")
	if err := os.Symlink(loose, link); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareDir(link, 0o750, uid, gid, top); err == nil {
		t.Error("a link was prepared")
	}

	good := filepath.Join(top, "shares")
	got, err := prepareDir(good, 0o750, uid, gid, top)
	if err != nil || got != good {
		t.Fatalf("prepareDir = %q, %v", got, err)
	}
	if fi, _ := os.Stat(good); fi.Mode().Perm() != 0o750 {
		t.Errorf("mode = %v, want 0750", fi.Mode().Perm())
	}
	if err := os.Chmod(good, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareDir(good, 0o750, uid, gid, top); err != nil {
		t.Fatalf("a trusted existing folder refused: %v", err)
	}
	if fi, _ := os.Stat(good); fi.Mode().Perm() != 0o750 {
		t.Errorf("an existing folder kept mode %v, want 0750", fi.Mode().Perm())
	}
}
