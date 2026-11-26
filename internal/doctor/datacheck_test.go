// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A data folder removed while the program runs is missing, not unwritable: the advice is to restart, not to
// change access.
func TestDataFolderMissingSaysSoAndHintsRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gone")
	c := dataCheck(dir)
	if c.Status != "bad" || !strings.Contains(c.Detail, "does not exist") || c.Hint == nil || *c.Hint != missingDataAdvice {
		t.Fatalf("missing folder: status %q detail %q hint %v; want bad, says it does not exist, the missing-folder advice", c.Status, c.Detail, c.Hint)
	}
	if strings.Contains(c.Detail, "Cannot write") || strings.Contains(*c.Hint, "write access") {
		t.Errorf("missing folder is described as unwritable: detail %q hint %q", c.Detail, *c.Hint)
	}
	if !strings.Contains(*c.Hint, "reconnect") || !strings.Contains(*c.Hint, "new, empty") {
		t.Errorf("hint %q must say to reconnect a drive or share, and that a restart makes a new, empty folder", *c.Hint)
	}
}

// A folder that exists and cannot be written is described as such, with the access advice.
func TestDataFolderNotWritableSaysSo(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) }) // so the temp folder can be removed
	if CanWrite(dir) {
		t.Skip("this user can write to a read-only folder (root, or Windows)")
	}
	c := dataCheck(dir)
	if c.Status != "bad" || !strings.Contains(c.Detail, "Cannot write") || c.Hint == nil || !strings.Contains(*c.Hint, "write access") {
		t.Fatalf("read-only folder: status %q detail %q hint %v; want bad, cannot write, write-access hint", c.Status, c.Detail, c.Hint)
	}
	if strings.Contains(c.Detail, "does not exist") {
		t.Errorf("read-only folder is described as missing: %q", c.Detail)
	}
}
