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
)

// keptOne converts the scenario's file keeping its original, and returns the kept row.
func keptOne(t *testing.T) (*scenario, jobs.Original) {
	t.Helper()
	s := newScenario(t)
	if _, err := s.keeping(7).RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	o, err := s.store.OriginalOfJob(s.id)
	if err != nil {
		t.Fatal(err)
	}
	return s, o
}

func TestRestorePutsTheOriginalBack(t *testing.T) {
	s, o := keptOne(t)
	got, err := Restore(s.store, o.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if read(s.src) != "original-bytes" {
		t.Errorf("at the name: %q", read(s.src))
	}
	if _, err := os.Stat(filepath.Join(s.dir, TrashDir)); err == nil {
		t.Error("the emptied .syp-trash folder is removed")
	}
	if r, _ := s.store.OriginalByID(o.ID); r.State != jobs.OriginalRestored {
		t.Errorf("row %s", r.State)
	}
	if f, _ := s.store.GetFile(s.src); f == nil || f.Decision != jobs.DecisionRestored {
		t.Errorf("file record %+v", f)
	}
	if !IdentityConfirmed(got) {
		t.Error("size, time and samples are those recorded")
	}
	if _, err := Restore(s.store, o.ID, false); !isCode(err, "not_kept") {
		t.Errorf("a second restore: %v", err)
	}
}

func isCode(err error, code string) bool {
	var re *RestoreError
	return errors.As(err, &re) && re.Code == code
}

// A converted file changed since it was made is not replaced, unless forced.
func TestRestoreRefusesAChangedConvertedFile(t *testing.T) {
	s, o := keptOne(t)
	os.WriteFile(s.src, []byte("edited"), 0o644)
	if _, err := Restore(s.store, o.ID, false); !isCode(err, "converted_changed") || !strings.Contains(err.Error(), "Nothing was changed") {
		t.Fatalf("%v", err)
	}
	if read(s.src) != "edited" || read(s.trash()) != "original-bytes" {
		t.Error("nothing may move")
	}
	if r, _ := s.store.OriginalByID(o.ID); r.State != jobs.OriginalKept {
		t.Errorf("row %s", r.State)
	}
	if _, err := Restore(s.store, o.ID, true); err != nil || read(s.src) != "original-bytes" {
		t.Errorf("forced: %v %q", err, read(s.src))
	}
}

// Same size and time, other content: the sampled hash sees it.
func TestRestoreSeesAnEditKeepingSizeAndTime(t *testing.T) {
	s, o := keptOne(t)
	st, _ := os.Stat(s.src)
	os.WriteFile(s.src, []byte("NEW"), 0o644) // "new" has 3 bytes too
	os.Chtimes(s.src, st.ModTime(), st.ModTime())
	if _, err := Restore(s.store, o.ID, false); !isCode(err, "converted_changed") {
		t.Errorf("%v", err)
	}
}

func TestRestoreRefusesAMissingOrDifferentOriginal(t *testing.T) {
	s, o := keptOne(t)
	os.WriteFile(s.trash(), []byte("someone-else!!"), 0o644) // same size, other content
	if _, err := Restore(s.store, o.ID, false); !isCode(err, "original_missing") || read(s.src) != "new" {
		t.Errorf("%v %q", err, read(s.src))
	}
	os.Remove(s.trash())
	if _, err := Restore(s.store, o.ID, false); !isCode(err, "original_missing") {
		t.Errorf("%v", err)
	}
	if _, err := Restore(s.store, 999, false); !isCode(err, "not_found") {
		t.Errorf("%v", err)
	}
}

// A converted file already gone (deleted by the person): the original just goes back.
func TestRestoreWhenTheConvertedFileIsGone(t *testing.T) {
	s, o := keptOne(t)
	os.Remove(s.src)
	if _, err := Restore(s.store, o.ID, false); err != nil || read(s.src) != "original-bytes" {
		t.Errorf("%v %q", err, read(s.src))
	}
}

// A crash at every step of a restore, then a restart: exactly one of the files is at the name, the
// journal has nothing left "restoring", and a finished restore is marked restored.
func TestCrashDuringRestoreRecovers(t *testing.T) {
	for _, step := range []string{"restore journal written", "converted moved aside", "original back"} {
		t.Run(step, func(t *testing.T) {
			s, o := keptOne(t)
			swapHook = func(at string) error {
				if at == step {
					return errCrash
				}
				return nil
			}
			_, err := Restore(s.store, o.ID, false)
			swapHook = nil
			if !errors.Is(err, errCrash) {
				t.Fatalf("must stop at %q: %v", step, err)
			}
			RecoverSwaps(s.store, s.store, 7, keepNow)
			r, _ := s.store.OriginalByID(o.ID)
			switch at := read(s.src); at {
			case "new": // nothing moved: still kept
				if r.State != jobs.OriginalKept || read(s.trash()) != "original-bytes" {
					t.Errorf("%s, trash %q", r.State, read(s.trash()))
				}
			case "original-bytes":
				if r.State != jobs.OriginalRestored || !strings.HasPrefix(read(s.trash()), "<") || !strings.HasPrefix(read(asidePath(o)), "<") {
					t.Errorf("%s, trash %q, aside %q", r.State, read(s.trash()), read(asidePath(o)))
				}
				if f, _ := s.store.GetFile(s.src); f == nil || f.Decision != jobs.DecisionRestored {
					t.Errorf("file record %+v", f)
				}
			default:
				t.Fatalf("at the name: %q", at)
			}
			if m, _ := s.store.Originals(jobs.Restoring); len(m) != 0 {
				t.Errorf("left restoring: %+v", m)
			}
		})
	}
}

// Purge: kept for days or more (the setting now) is deleted, a second less is kept. Only the recorded
// file goes; anything else in .syp-trash stays, and the folder with it.
func TestPurgeBoundaryAndOnlyKnownFiles(t *testing.T) {
	s, o := keptOne(t)
	kept, _ := time.Parse(time.RFC3339, o.KeptAt)
	if said := Purge(s.store, 7, kept.Add(7*24*time.Hour-time.Second)); len(said) != 0 || read(s.trash()) != "original-bytes" {
		t.Fatalf("a second early: %q, %q", said, read(s.trash()))
	}
	stranger := filepath.Join(s.dir, TrashDir, "not-ours.mkv")
	os.WriteFile(stranger, []byte("x"), 0o644)
	said := Purge(s.store, 7, kept.Add(7*24*time.Hour+time.Second))
	if !strings.HasPrefix(read(s.trash()), "<") || len(said) != 1 || !strings.Contains(said[0], "deleted 1") {
		t.Errorf("a second late: %q, %q", said, read(s.trash()))
	}
	if read(stranger) != "x" {
		t.Error("an unknown file in .syp-trash must never be touched")
	}
	if r, _ := s.store.OriginalByID(o.ID); r.State != jobs.OriginalPurged {
		t.Errorf("row %s", r.State)
	}
	if read(s.src) != "new" {
		t.Error("the converted file stays")
	}
}

// Lowering keepOriginalsDays applies at the next purge (the age is counted from when it was kept, with
// the setting as it is now).
func TestPurgeUsesTheSettingNow(t *testing.T) {
	s, o := keptOne(t)
	kept, _ := time.Parse(time.RFC3339, o.KeptAt)
	three := kept.Add(3 * 24 * time.Hour)
	if Purge(s.store, 7, three); read(s.trash()) != "original-bytes" {
		t.Fatal("7 days: kept after 3")
	}
	Purge(s.store, 2, three) // the setting lowered to 2 days
	if !strings.HasPrefix(read(s.trash()), "<") {
		t.Error("2 days: deleted after 3")
	}
	if _, err := os.Stat(filepath.Join(s.dir, TrashDir)); err == nil {
		t.Error("the empty .syp-trash folder is removed")
	}
}

// A file at the trash name that is not the kept original (another size) is left alone.
func TestPurgeLeavesAReplacedTrashFile(t *testing.T) {
	s, o := keptOne(t)
	os.WriteFile(s.trash(), []byte("not the original at all"), 0o644)
	kept, _ := time.Parse(time.RFC3339, o.KeptAt)
	said := Purge(s.store, 1, kept.Add(48*time.Hour))
	if read(s.trash()) != "not the original at all" || len(said) != 1 || !strings.Contains(said[0], "left alone") {
		t.Errorf("%q %q", said, read(s.trash()))
	}
	if r, _ := s.store.OriginalByID(o.ID); r.State != jobs.SwapAttention {
		t.Errorf("row %s", r.State)
	}
}
