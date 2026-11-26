// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The CLI doctor's data line says a removed folder is missing, with the same advice as the web check.
func TestHealthDataFolderMissingSaysSoAndAdvises(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gone")
	c := dataHealthCheck(dir)
	if c.Status != "bad" || !strings.Contains(c.Detail, "does not exist") || !strings.HasSuffix(c.Detail, missingDataAdvice) {
		t.Fatalf("missing folder: status %q detail %q; want bad, says it does not exist, then the missing-folder advice", c.Status, c.Detail)
	}
	if strings.Contains(c.Detail, "Cannot write") || strings.Contains(c.Detail, "read-only") {
		t.Errorf("missing folder is described as unwritable: %q", c.Detail)
	}
}

// A folder that exists and cannot be written keeps its access advice.
func TestHealthDataFolderNotWritableSaysSo(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) }) // so the temp folder can be removed
	if CanWrite(dir) {
		t.Skip("this user can write to a read-only folder (root, or Windows)")
	}
	c := dataHealthCheck(dir)
	if c.Status != "bad" || !strings.Contains(c.Detail, "Cannot write to "+dir) || !strings.Contains(c.Detail, "read-only") {
		t.Fatalf("read-only folder: status %q detail %q; want bad, cannot write, read-only advice", c.Status, c.Detail)
	}
	if strings.Contains(c.Detail, "does not exist") {
		t.Errorf("read-only folder is described as missing: %q", c.Detail)
	}
}

// A writable folder is ok, and the line names the folder.
func TestHealthDataFolderWritableIsOK(t *testing.T) {
	dir := t.TempDir()
	c := dataHealthCheck(dir)
	if c.Status != "ok" || c.Detail != dir {
		t.Fatalf("writable folder: status %q detail %q; want ok, detail %q", c.Status, c.Detail, dir)
	}
}
