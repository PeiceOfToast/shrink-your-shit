// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build unix

package library

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
)

// TestScanSkipsAHardLink covers the L-SCAN-1 hard-link guard: a file with more than one name is
// left alone, because converting through one name would change what the other name points at.
func TestScanSkipsAHardLink(t *testing.T) {
	st := newScanTest(t)
	original := st.write("original.mp4", "0123456789")
	if err := os.Link(original, filepath.Join(st.dir, "second-name.mp4")); err != nil {
		t.Skipf("this host will not make hard links: %v", err)
	}

	res := st.run()
	if res.SkippedBy[skipSharedFile] != 2 || res.Queued != 0 {
		t.Errorf("result = %+v, want both names skipped as shared", res)
	}
	wantQueued(t, st.queue.names())
	if f := st.row("original.mp4"); f.State != Skipped || f.SkipReason != skipSharedFile {
		t.Errorf("row = %+v, want skipped/shared_file", f)
	}
}

// TestScanMarksNothingGoneWhenAFolderIsUnreadable covers L-SCAN-3: a scan that could not read part
// of the library is incomplete, so a file inside the unreadable folder keeps its state instead of
// being marked gone. It also shows the person a warning naming the folder.
func TestScanMarksNothingGoneWhenAFolderIsUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a folder with no permissions")
	}
	st := newScanTest(t)
	sub := filepath.Join(st.dir, "sub")
	st.write("sub/clip.mp4", "0123456789")
	st.run()
	wantQueued(t, st.queue.names(), "sub/clip.mp4")

	if err := os.Remove(filepath.Join(sub, "clip.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })

	res := st.run()
	if res.Gone != 0 {
		t.Errorf("result = %+v, want nothing gone from an incomplete scan", res)
	}
	if f := st.row("sub/clip.mp4"); f.State != Queued {
		t.Errorf("row state = %q, want the old state kept", f.State)
	}
	warnings := st.events.ofKind(events.KindWarning)
	if len(warnings) == 0 {
		t.Error("no warning event for the unreadable folder")
	}
}
