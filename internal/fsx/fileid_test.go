// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package fsx

import (
	"os"
	"path/filepath"
	"testing"
)

// FileID keeps a file's identity across a rename, so a settled file that moved is not a new file.
func TestFileIDStaysTheSameAfterARename(t *testing.T) {
	dir := t.TempDir()
	before := filepath.Join(dir, "before.mp4")
	after := filepath.Join(dir, "after.mp4")
	if err := os.WriteFile(before, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := FileID(before)
	if err != nil {
		t.Fatalf("FileID: %v", err)
	}
	if err := os.Rename(before, after); err != nil {
		t.Fatal(err)
	}
	moved, err := FileID(after)
	if err != nil {
		t.Fatalf("FileID after the rename: %v", err)
	}
	if id == "" || id != moved {
		t.Errorf("id %q became %q, want the same non-empty id", id, moved)
	}
}

func TestDifferentFilesHaveDifferentIDs(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	idA, err := FileID(a)
	if err != nil {
		t.Fatal(err)
	}
	idB, err := FileID(b)
	if err != nil {
		t.Fatal(err)
	}
	if idA == idB {
		t.Errorf("two files share the id %q", idA)
	}
}

// LinkCount is how the scanner tells a hard link from a normal file.
func TestLinkCountCountsNames(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.mp4")
	second := filepath.Join(dir, "second.mp4")
	if err := os.WriteFile(first, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err := LinkCount(first); err != nil || n != 1 {
		t.Fatalf("LinkCount = %d, %v; want 1", n, err)
	}
	if err := os.Link(first, second); err != nil {
		t.Skipf("this file system cannot make a hard link: %v", err)
	}
	for _, p := range []string{first, second} {
		if n, err := LinkCount(p); err != nil || n != 2 {
			t.Errorf("LinkCount(%s) = %d, %v; want 2", filepath.Base(p), n, err)
		}
	}
}

func TestFileIDOfAMissingFileErrors(t *testing.T) {
	if _, err := FileID(filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Error("FileID of a missing file returned no error")
	}
}
