// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

// A file converted in place is our output: the scan must not queue it again. Its record takes the file's time and loses
// its "queued" decision, but keeps the before-size (the totals read it: BytesBefore and bytesNow do not change). An
// unchanged scan after that leaves the record as it is.
func TestReplacedInPlaceUpdatesItsRecord(t *testing.T) {
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
	before, err := st.GetFile(a)
	if err != nil || before == nil || before.Size != 5 {
		t.Fatalf("queued record %+v, %v: want size 5", before, err)
	}
	id, err := st.Claim("w")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(a, []byte("ab"), 0o644)
	if err := st.Complete(id.ID, a, 2); err != nil {
		t.Fatal(err)
	}
	totalsOf := func() (bytesBefore, bytesNow int64) {
		lt, err := st.LibraryTotalsOf(l)
		if err != nil {
			t.Fatal(err)
		}
		tot, _ := st.TotalsOf()
		c := tot.Counts[l.ID]
		return lt.BytesBefore, lt.BytesBefore - c.FreedBytes - c.WaitingBytes
	}
	b0, now0 := totalsOf()

	if n := settle(s); n != 0 {
		t.Fatalf("queued %d after the replace, want 0: our own output", n)
	}
	fi, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.GetFile(a)
	if err != nil || got == nil {
		t.Fatalf("record %+v, %v", got, err)
	}
	if got.ModTime != fi.ModTime().UnixNano() {
		t.Errorf("record time %d, want the file's %d", got.ModTime, fi.ModTime().UnixNano())
	}
	if got.Size != 5 {
		t.Errorf("record size %d, want 5: the before-size is kept on purpose", got.Size)
	}
	if got.Decision != "" || got.Reason != "" {
		t.Errorf("record decision %q reason %q, want none", got.Decision, got.Reason)
	}
	if b1, now1 := totalsOf(); b1 != b0 || now1 != now0 {
		t.Errorf("totals changed: BytesBefore %d -> %d, bytesNow %d -> %d", b0, b1, now0, now1)
	}

	// an unchanged scan: nothing written, the record is the same
	if n := settle(s); n != 0 {
		t.Fatalf("queued %d on the unchanged scan, want 0", n)
	}
	again, _ := st.GetFile(a)
	if again == nil || *again != *got {
		t.Errorf("record changed on the unchanged scan: %+v, want %+v", again, got)
	}
}
