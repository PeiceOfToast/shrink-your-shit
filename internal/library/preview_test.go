// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package library

import (
	"context"
	"reflect"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// The preview counts what QueueFiles would queue for the same scanned files, from the same stored probes, and queues
// nothing itself.
func TestPreviewMatchesWhatQueueFilesQueues(t *testing.T) {
	ctx := context.Background()
	st := newScanTest(t)
	decide := func(f File, _ media.Info) (bool, string) {
		switch f.RelPath {
		case "a.mp4":
			return true, ""
		case "b.mp4":
			return false, "nothing_to_do"
		}
		return false, "plan_failed"
	}
	st.decide = decide
	st.write("a.mp4", "aaaa")
	st.write("b.mp4", "bbbb")
	st.write("c.mp4", "cccc")
	st.run()

	files, err := st.c.Files(ctx, st.lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	queuedBefore := st.queue.names()
	p := PreviewOf(files, decide)
	if got := st.queue.names(); !reflect.DeepEqual(got, queuedBefore) {
		t.Errorf("the preview queued %v, want nothing", got)
	}
	if p.WouldConvert != 1 || p.Total != 3 {
		t.Fatalf("preview = %d of %d, want 1 of 3", p.WouldConvert, p.Total)
	}
	if len(p.Skipped) != 2 || p.Skipped[0] != (PreviewSkip{Reason: "nothing_to_do", Count: 1}) && p.Skipped[0] != (PreviewSkip{Reason: "plan_failed", Count: 1}) {
		t.Errorf("skipped = %+v, want one nothing_to_do and one plan_failed", p.Skipped)
	}

	n, err := st.scanner.QueueFiles(ctx, st.lib)
	if err != nil {
		t.Fatal(err)
	}
	if n != p.WouldConvert {
		t.Errorf("QueueFiles queued %d, the preview said %d", n, p.WouldConvert)
	}
}

// A file with no stored probe is unknown; a gone or still-changing row is not counted at all.
func TestPreviewCountsUnprobedAndLeavesOutGoneRows(t *testing.T) {
	files := []File{
		{RelPath: "unprobed.mp4"},
		{RelPath: "gone.mp4", State: Gone, Probe: &media.Tracks{Container: "mov"}},
		{RelPath: "moving.mp4", State: StillChanging, Probe: &media.Tracks{Container: "mov"}},
		{RelPath: "ok.mp4", Probe: &media.Tracks{Container: "mov"}},
	}
	p := PreviewOf(files, func(File, media.Info) (bool, string) { return true, "" })
	if p.Total != 2 || p.WouldConvert != 1 {
		t.Errorf("preview = %d of %d, want 1 of 2 (gone and still-changing left out)", p.WouldConvert, p.Total)
	}
	if len(p.Skipped) != 1 || p.Skipped[0] != (PreviewSkip{Reason: PreviewUnknown, Count: 1}) {
		t.Errorf("skipped = %+v, want one unknown", p.Skipped)
	}
}

// An empty library gives zeros and an empty list, not nil.
func TestPreviewOfNoFiles(t *testing.T) {
	p := PreviewOf(nil, func(File, media.Info) (bool, string) { return true, "" })
	if p.Total != 0 || p.WouldConvert != 0 || p.Skipped == nil || len(p.Skipped) != 0 {
		t.Errorf("empty preview = %+v", p)
	}
}
