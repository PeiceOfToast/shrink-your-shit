// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package library

import (
	"os"
	"path/filepath"
	"testing"
)

// CheckWritable is the one write probe: it takes a file and gives it back, and an error is the folder's answer.
func TestCheckWritable(t *testing.T) {
	dir := t.TempDir()
	if err := CheckWritable(dir); err != nil {
		t.Fatalf("CheckWritable of a writable folder = %v, want nil", err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("after the probe the folder holds %v (err %v), want it left empty", entries, err)
	}
	if err := CheckWritable(filepath.Join(dir, "gone")); err == nil {
		t.Error("CheckWritable of a missing folder = nil, want an error")
	}
}
