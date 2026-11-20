// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The new file's size comes from its stat; when that stat fails, the size measured before the move stands in
// and the job log says so. Only a failed stat with no measured size is reported as unknown.
func TestNewFileSizeFallsBack(t *testing.T) {
	p := filepath.Join(t.TempDir(), "new.mkv")
	if err := os.WriteFile(p, make([]byte, 77), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if size, note := newFileSize(st, 1234); size != 77 || note != "" {
		t.Fatalf("stat ok: size %d note %q, want 77 and no note", size, note)
	}
	size, note := newFileSize(nil, 1234)
	if size != 1234 || !strings.Contains(note, "using the size measured before the move (1234 bytes)") {
		t.Fatalf("stat failed, measured 1234: size %d note %q", size, note)
	}
	size, note = newFileSize(nil, 0)
	if size != 0 || !strings.Contains(note, "size unknown") {
		t.Fatalf("stat failed, nothing measured: size %d note %q, want 0 and size unknown", size, note)
	}
}
