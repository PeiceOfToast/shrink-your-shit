// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package scanner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// A converted file whose original was kept in the trash is deleted from the library: once a scan finds it
// gone, the overview must stop counting it in bytesNow and waitingToFree (the trash's original is not in the
// library either, and its saving is no longer waiting for anything that is still there).
func TestDeletedConvertedFileLeavesTheOverview(t *testing.T) {
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

	// The conversion: the original (5 bytes) goes to the trash, the 2-byte new file takes its place.
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
	if n := settle(s); n != 0 {
		t.Fatalf("queued %d after the conversion, want 0", n)
	}
	before, _ := st.TotalsOf()
	if got := before.Counts[l.ID].WaitingBytes; got != 3 {
		t.Fatalf("waiting before the delete %d, want 3 (5 - 2)", got)
	}

	// The person deletes the converted file; the next scan finds it gone.
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	settle(s)
	after, _ := st.TotalsOf()
	c := after.Counts[l.ID]
	if c.WaitingBytes != 0 {
		t.Errorf("waitingToFree after the delete %d, want 0", c.WaitingBytes)
	}
	bytesBefore, err := st.LibraryTotalsOf(l)
	if err != nil {
		t.Fatal(err)
	}
	if now := jobs.BytesNow(bytesBefore, c); now != 0 {
		t.Errorf("bytesNow after the delete %d, want 0 (BytesBefore %d, freed %d, waiting %d, gone %d)",
			now, bytesBefore.BytesBefore, c.FreedBytes, c.WaitingBytes, c.GoneBytes)
	}
}

// The freed case: a replaced file with nothing kept (no original in the trash), whose converted file is then
// deleted. Its saving is freed already; the bytes still in bytesNow must go too, so bytesNow is 0.
func TestDeletedReplacedFileWithNothingKeptLeavesTheOverview(t *testing.T) {
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
	settle(s)
	before, _ := st.TotalsOf()
	lt, _ := st.LibraryTotalsOf(l)
	if got := before.Counts[l.ID].FreedBytes; got != 3 {
		t.Fatalf("freed before the delete %d, want 3 (5 - 2)", got)
	}
	if now := jobs.BytesNow(lt, before.Counts[l.ID]); now != 2 {
		t.Fatalf("bytesNow before the delete %d, want 2", now)
	}

	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	settle(s)
	after, _ := st.TotalsOf()
	c := after.Counts[l.ID]
	if now := jobs.BytesNow(lt, c); now != 0 {
		t.Errorf("bytesNow after the delete %d, want 0 (BytesBefore %d, freed %d, waiting %d, gone %d)",
			now, lt.BytesBefore, c.FreedBytes, c.WaitingBytes, c.GoneBytes)
	}
	if c.FreedBytes != 3 {
		t.Errorf("freed after the delete %d, want 3: the saved history does not change", c.FreedBytes)
	}
}
