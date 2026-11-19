// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package scanner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// A converted file whose original is kept in the trash is touched (a copy back, a sync tool, a backup restore):
// same bytes, new mtime. It is still our output, so the scan must not record it at its size and queue it again:
// the library's before-size stays the original's 5, the saving 3 stays waiting, and 5 - 3 = 2 is what is there.
func TestTouchedConvertedFileIsStillOurOutput(t *testing.T) {
	s, st, lib, _ := setup(t, h264Big)
	if n := settle(s); n != 1 {
		t.Fatalf("queued %d, want 1", n)
	}
	libs, err := st.Libraries()
	if err != nil || len(libs) != 1 {
		t.Fatalf("libraries %v, %v", libs, err)
	}
	l := libs[0]
	a := filepath.Join(lib, "a.mkv")
	id, err := st.Claim("w")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(a, []byte("ab"), 0o644)
	if err := st.Complete(id.ID, a, 2); err != nil {
		t.Fatal(err)
	}
	oid, err := st.BeginSwap(jobs.Original{JobID: id.ID, LibraryID: l.ID, Path: a, Trash: filepath.Join(t.TempDir(), "a.mkv"), Bytes: 5, NewBytes: 2, NewPath: ""})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishSwap(oid, time.Now().Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	settle(s)

	// The touch: the same two bytes, a new mtime a minute ago (a current copy, not a future one the walk skips).
	os.WriteFile(a, []byte("ab"), 0o644)
	touched := time.Now().Add(-time.Minute)
	os.Chtimes(a, touched, touched)
	if n := settle(s); n != 0 {
		t.Errorf("queued %d after the touch, want 0: the converted file is still our output", n)
	}

	lt, err := st.LibraryTotalsOf(l)
	if err != nil {
		t.Fatal(err)
	}
	tot, _ := st.TotalsOf()
	c := tot.Counts[l.ID]
	if c.WaitingBytes != 3 {
		t.Errorf("waiting after the touch %d, want 3", c.WaitingBytes)
	}
	if lt.BytesBefore != 5 {
		t.Errorf("BytesBefore after the touch %d, want 5 (the record keeps the original's size)", lt.BytesBefore)
	}
	if now := lt.BytesBefore - c.FreedBytes - c.WaitingBytes; now != 2 {
		t.Errorf("bytesNow after the touch %d, want 2 (BytesBefore 5 less the waiting 3); got a double subtraction", now)
	}
}
