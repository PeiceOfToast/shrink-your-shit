// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
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
	if err := s.store.ConvertAgain(s.id); !errors.As(err, &ae) || ae.Code != jobs.AgainOriginalGone || ae.DeletedAt != o.DeleteOn ||
		!strings.Contains(ae.Message, "the original was deleted on ") {
		t.Errorf("purged: %v", err)
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
