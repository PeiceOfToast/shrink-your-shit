// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !windows

package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// When the link works but the source cannot be unlinked, the file is at the new name and the source is still there:
// the caller gets ErrNotRemoved, so it can report the leftover instead of pretending the move was clean.
func TestLinkRenameReportsASourceLeftBehind(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so the unlink cannot be refused")
	}
	dir := t.TempDir()
	stage := filepath.Join(dir, "stage")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(stage, "out.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "final.mkv")

	// Read and search only: the link can still add a name in dir, but the unlink of stage/out.mkv is refused.
	if err := os.Chmod(stage, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(stage, 0o700) })

	err := linkRename(src, dst)
	if !errors.Is(err, ErrNotRemoved) {
		t.Fatalf("err = %v; want ErrNotRemoved", err)
	}
	if _, statErr := os.Stat(dst); statErr != nil {
		t.Fatalf("the file should be at the final name: %v", statErr)
	}
	if _, statErr := os.Stat(src); statErr != nil {
		t.Fatalf("the source should still be there: %v", statErr)
	}
}
