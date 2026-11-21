// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// encodeInput is the -i of the scenario's last encode.
func (s *scenario) encodeInput() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	in := ""
	for _, c := range s.calls {
		if strings.HasPrefix(c, "ffmpeg ") && strings.Contains(c, "libx265") {
			f := strings.Fields(c)
			for i := 0; i+1 < len(f); i++ {
				if f[i] == "-i" {
					in = f[i+1]
					break
				}
			}
		}
	}
	return in
}

// Convert again (design 11:45Z) of a file that replaced its original: it converts from the original kept
// in .syp-trash, never from our output; the new file replaces our earlier one; the original stays kept,
// its days starting again.
func TestConvertAgainFromTheKeptOriginal(t *testing.T) {
	s := newScenario(t)
	w := s.keeping(7)
	if _, err := w.RunOne(context.Background()); err != nil || s.job().Status != jobs.Done {
		t.Fatalf("first: %v %s", err, s.job().Status)
	}
	first, err := s.store.OriginalOfJob(s.id)
	if err != nil || read(s.src) != "new" || read(s.trash()) != "original-bytes" {
		t.Fatalf("first swap %v %q %q", err, read(s.src), read(s.trash()))
	}
	later := keepNow.Add(5 * 24 * time.Hour)
	w.Now = func() time.Time { return later }
	if err := s.store.ConvertAgain(s.id); err != nil {
		t.Fatal(err)
	}
	s.moreSrcs = map[string]bool{s.trash(): true}
	s.content = "newer"
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := s.job(); j.Status != jobs.Done || j.OutputPath != s.src {
		t.Fatalf("again: %s %q %q", j.Status, j.Error, j.OutputPath)
	}
	if in := s.encodeInput(); in != s.trash() {
		t.Errorf("converted from %q, want the kept original %q", in, s.trash())
	}
	if read(s.src) != "newer" || read(s.trash()) != "original-bytes" || tempLeft(s.src) {
		t.Errorf("at the name %q, kept %q", read(s.src), read(s.trash()))
	}
	o, _ := s.store.OriginalByID(first.ID)
	if o.State != jobs.OriginalKept || o.DeleteOn != later.AddDate(0, 0, 7).UTC().Format(time.RFC3339) || !isConvertedFile(s.src, o) {
		t.Errorf("record %+v", o)
	}
	if s.store.AgainFrom(s.id) != 0 {
		t.Error("again_from not cleared once done")
	}
	// its kept original deleted: refused, with the date
	s.store.SetSwapState(first.ID, jobs.OriginalPurged)
	os.Remove(s.trash())
	var ae jobs.AgainError
	at, _ := time.Parse(time.RFC3339, ae.DeletedAt)
	if err := s.store.ConvertAgain(s.id); !errors.As(err, &ae) || ae.Code != jobs.AgainOriginalGone ||
		!strings.Contains(ae.Message, "the original was deleted on ") {
		t.Errorf("purged: %v", err)
	}
	if at, _ = time.Parse(time.RFC3339, ae.DeletedAt); time.Since(at) > time.Minute {
		t.Errorf("deletedAt %q: when it was purged", ae.DeletedAt)
	}
}

// A file at the name that isn't the one we made from this original is never replaced by a Convert again.
func TestConvertAgainNeverReplacesAnotherFile(t *testing.T) {
	s := newScenario(t)
	w := s.keeping(7)
	w.RunOne(context.Background())
	s.store.ConvertAgain(s.id)
	os.WriteFile(s.src, []byte("the person's own file"), 0o644)
	s.moreSrcs = map[string]bool{s.trash(): true}
	s.content = "newer"
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, "is not the one Shrink Your Shit made") {
		t.Errorf("%s %q", j.Status, j.Error)
	}
	if read(s.src) != "the person's own file" || read(s.trash()) != "original-bytes" || tempLeft(s.src) {
		t.Errorf("at the name %q, kept %q", read(s.src), read(s.trash()))
	}
}

// Output-first (beside here): Convert again runs from the original in place and replaces only our own
// earlier output once the new file passes its checks.
func TestConvertAgainBesideReplacesOurOutput(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Beside)
	if _, err := w.RunOne(context.Background()); err != nil || s.job().Status != jobs.Done {
		t.Fatalf("first: %v %s", err, s.job().Status)
	}
	out := BesidePath(s.src)
	if err := s.store.ConvertAgain(s.id); err != nil {
		t.Fatal(err)
	}
	s.content = "newer"
	if _, err := w.RunOne(context.Background()); err != nil || s.job().Status != jobs.Done {
		t.Fatalf("again: %v %s %q", err, s.job().Status, s.job().Error)
	}
	if in := s.encodeInput(); in != s.src {
		t.Errorf("converted from %q", in)
	}
	if read(out) != "newer" || read(s.src) != "original-bytes" {
		t.Errorf("beside %q, original %q", read(out), read(s.src))
	}
}

// Review #491 P2: the moves are recorded and logged in the order they happened. A Convert again whose new name
// differs moves the new file first and removes our earlier converted file after it.
func TestConvertAgainRecordsTheMoveBeforeTheRemove(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(old, []byte("earlier"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, id := realJob(t, old, 1)
	w := realWorker(st, media.OSExec{})
	ost, err := os.Stat(old)
	if err != nil {
		t.Fatal(err)
	}
	h, err := SampleHash(old)
	if err != nil {
		t.Fatal(err)
	}
	o := jobs.Original{JobID: id, Path: old, NewBytes: ost.Size(), NewMTime: ost.ModTime().UnixNano(), NewHash: h}
	if o.ID, err = st.BeginSwap(o); err != nil {
		t.Fatal(err)
	}
	stage := &staged{dir: dir, path: filepath.Join(dir, "movie.tmp")}
	if err := os.WriteFile(stage.path, []byte("newer"), 0o644); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(dir, "movie.mp4")
	if err := w.replaceOwnOutput(context.Background(), o, stage, final, 7, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	moves, err := st.JobMoves(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(moves) != 2 || moves[0].Action != "move" || moves[0].From != stage.path || moves[0].To != final || !moves[0].OK ||
		moves[1].Action != "remove" || moves[1].From != old || !moves[1].OK {
		t.Fatalf("moves %+v, want the move then the remove", moves)
	}
	if read(final) != "newer" {
		t.Errorf("at the new name %q, want the new file", read(final))
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("our earlier converted file is still there")
	}
}

// #400: a file the person put at our output's name, the same size as our output, is not ours: a size-only match
// must never delete it. It is not an exact match, and it sits at our recorded output name, so (decision A) it is
// moved into .syp-trash beside it, never deleted, and the new file takes the name.
func TestBesideKeepsSameSizeUserFile(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Beside)
	w.RunOne(context.Background())
	out := BesidePath(s.src)
	own := strings.Repeat("x", len(read(out)))
	os.WriteFile(out, []byte(own), 0o644)
	s.id, _ = s.store.Enqueue(jobs.Job{Library: s.dir, Path: s.src, DurationSec: 100})
	s.content = "newer"
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Done {
		t.Errorf("%s %q", j.Status, j.Error)
	}
	if read(out) != "newer" {
		t.Errorf("at the name %q, want the new file", read(out))
	}
	if kept := trashedBeside(t, out); len(kept) != 1 || read(kept[0]) != own {
		t.Errorf("trash %v, want the person's file kept once", kept)
	}
}

// A Convert again whose workflow now ends in "Stop as failed" fails with that ending: its own code and the
// workflow's words, as the beside run does (dev-review2 P3-1 of #615). The replanning "leaves this file
// alone" refusal used to come first, so the job stored convert_failed with words that hid the ending.
func TestConvertAgainWithAStopAsFailedEndingGetsItsCode(t *testing.T) {
	s := newScenarioNamed(t, "Harrow.Lane.2019.WEB.mkv")
	w := s.keeping(7)
	if _, err := w.RunOne(context.Background()); err != nil || s.job().Status != jobs.Done {
		t.Fatalf("first: %v %s", err, s.job().Status)
	}
	encodes := s.count("-c:v")
	o, err := s.store.OriginalOfJob(s.id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.ConvertAgain(s.id); err != nil {
		t.Fatal(err)
	}
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return cardsTemplate(), 0, nil } // its No path ends in Stop as failed
	s.moreSrcs = map[string]bool{o.Trash: true}
	w.RunOne(context.Background())
	j := s.job()
	if j.Status != jobs.Failed || j.ErrorCode != jobs.ReasonWorkflowStopped {
		t.Errorf("a Stop as failed on Convert again: %s code %q, want failed code %q", j.Status, j.ErrorCode, jobs.ReasonWorkflowStopped)
	}
	if !strings.Contains(j.Error, "Not a remux: nothing to gain") || strings.Contains(j.Error, "leaves this file alone") {
		t.Errorf("the words are %q, want the workflow's ending, not the leaves-it-alone refusal", j.Error)
	}
	if got := s.count("-c:v"); got != encodes {
		t.Error("nothing should have been encoded")
	}
	if read(s.src) != "new" || read(o.Trash) != "original-bytes" { // our earlier output, and the kept original
		t.Errorf("the file %q and the kept original %q, want both untouched", read(s.src), read(o.Trash))
	}
}
