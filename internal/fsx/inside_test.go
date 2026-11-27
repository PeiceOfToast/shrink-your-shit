// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package fsx

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInsideOnRealFolders(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "shows", "season 1")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		child string
		want  bool
	}{
		{"under", child, true},
		{"the parent itself", root, true},
		{"a sibling of the child, still under it", filepath.Join(root, "music"), true},
		{"up and over", filepath.Join(root, "..", filepath.Base(root)+"-other"), false},
		{"a not-yet-made folder under it", filepath.Join(child, "new"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Inside(c.child, root); got != c.want {
				t.Errorf("Inside(%s, %s) = %v, want %v", c.child, root, got, c.want)
			}
		})
	}
}

// Case is ignored on every OS (L-SAFE-15: two libraries may not differ only in letter case).
func TestInsideIgnoresCase(t *testing.T) {
	if got := Inside("/Movies/Shows", "/movies"); !got {
		t.Error("Inside ignored case for nothing: /Movies/Shows is not inside /movies")
	}
}

// A parent reached through a symlink compares as its real folder.
func TestInsideResolvesSymlinks(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("this machine cannot make a symlink: %v", err)
	}
	if got := Inside(filepath.Join(alias, "child"), real); !got {
		t.Error("a child of the symlinked folder is not inside its real folder")
	}
}

// A Windows path on another volume is not inside; on Unix the test is the same folder rule.
func TestInsideAcrossVolumes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("volumes only exist on Windows")
	}
	if Inside(`D:\media`, `C:\data`) {
		t.Error(`D:\media reported inside C:\data`)
	}
}
