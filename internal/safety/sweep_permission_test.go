// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A leftover staging folder we cannot empty is ErrNoPermission, which fails the job before encoding. It is not
// ErrBusy, which means retry later.
func TestSweepReportsNoPermissionForALockedLeftover(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows folder permissions are not Unix modes")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so the remove cannot be refused")
	}
	dir := t.TempDir()
	folder := filepath.Join(dir, ".movie.abc123.syp-tmp")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "out.mkv"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Read and search only: the folder can be listed, but the out.mkv inside it cannot be removed.
	if err := os.Chmod(folder, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(folder, 0o700) })

	_, err := Sweep(context.Background(), []string{dir})
	if !errors.Is(err, ErrNoPermission) {
		t.Fatalf("err = %v; want ErrNoPermission", err)
	}
	if errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v; a permission refusal must not read as busy", err)
	}
	if _, statErr := os.Stat(filepath.Join(folder, "out.mkv")); statErr != nil {
		t.Fatalf("the locked file should be untouched: %v", statErr)
	}
}
