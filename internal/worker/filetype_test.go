// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Changing the file type in replace mode (25a-2): movie.mp4 becomes movie.mkv, the original kept in
// .syp-trash under its own name, every step journaled, nothing ever replaced at the new name.

// mp4ToMkv is a scenario whose original is movie.mp4, converted with "Make it an MKV file" in replace
// mode, keeping originals for days.
func mp4ToMkv(t *testing.T, days int) (*scenario, *Worker, string) {
	s := newScenarioNamed(t, "movie.mp4")
	s.srcSpec.format = "mov,mp4,m4a,3gp,3g2,mj2"
	sel := rules.DefaultSelectors(3)
	sel.FileType, sel.SkipSmall = "mkv", false
	tmpl := sel.Template()
	w := s.keeping(days)
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	return s, w, filepath.Join(s.dir, "movie.mkv")
}

func (s *scenario) mp4Trash() string {
	return filepath.Join(s.dir, TrashDir, "movie."+strconv.FormatInt(s.id, 10)+".mp4")
}

func gone(p string) bool { _, err := os.Lstat(p); return errors.Is(err, os.ErrNotExist) }

func TestReplaceWithNewTypeRenamesTheFile(t *testing.T) {
	s, w, mkv := mp4ToMkv(t, 7)
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := s.job(); j.Status != jobs.Done || j.OutputPath != mkv {
		t.Fatalf("%+v", j)
	}
	if read(mkv) != "new" || !gone(s.src) || read(s.mp4Trash()) != "original-bytes" {
		t.Fatalf("mkv %q, mp4 gone %v, kept %q", read(mkv), gone(s.src), read(s.mp4Trash()))
	}
	o, err := s.store.OriginalOfJob(s.id)
	if err != nil || o.Path != s.src || o.NewPath != mkv || o.Dest() != mkv || o.State != jobs.OriginalKept {
		t.Errorf("%+v %v", o, err)
	}
	items, _, _ := s.store.DoneItems(10, 0)
	if len(items) != 1 || items[0].Beside || items[0].NewPath != mkv || items[0].Path != s.src {
		t.Errorf("done list %+v", items)
	}
}

// With originals kept 0 days, the change of name still goes through the journal; the kept original is
// deleted right after.
func TestReplaceWithNewTypeAndZeroDays(t *testing.T) {
	s, w, mkv := mp4ToMkv(t, 0)
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Done || read(mkv) != "new" || !gone(s.src) || !gone(s.mp4Trash()) {
		t.Fatalf("%+v mkv %q", j, read(mkv))
	}
	if rows, _ := s.store.Originals(jobs.OriginalPurged); len(rows) != 1 || rows[0].NewPath != mkv {
		t.Errorf("purged rows %+v", rows)
	}
	if _, err := os.Stat(filepath.Join(s.dir, TrashDir)); err == nil {
		t.Error("the empty .syp-trash folder was left")
	}
}

// movie.mkv already there: nothing is converted, nothing changed (design's sentence).
func TestReplaceWithNewTypeNameTakenBefore(t *testing.T) {
	s, w, mkv := mp4ToMkv(t, 7)
	os.WriteFile(mkv, []byte("theirs"), 0o644)
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Failed || j.Error != "A file named movie.mkv is already there. Nothing was changed. Move or rename that file, then press Retry." {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	if s.count("libx265") != 0 {
		t.Error("converted although the name is taken")
	}
	if read(mkv) != "theirs" || read(s.src) != "original-bytes" {
		t.Errorf("mkv %q, mp4 %q", read(mkv), read(s.src))
	}
}

// movie.mkv appears while converting: it is not replaced, and the original is back at its name.
func TestReplaceWithNewTypeNameTakenDuringTheEncode(t *testing.T) {
	s, w, mkv := mp4ToMkv(t, 7)
	s.onEncode = func() { os.WriteFile(mkv, []byte("theirs"), 0o644) }
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Failed || !strings.HasPrefix(j.Error, "A file named movie.mkv is already there.") {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	if read(mkv) != "theirs" || read(s.src) != "original-bytes" || !gone(s.mp4Trash()) {
		t.Errorf("mkv %q, mp4 %q, kept %q", read(mkv), read(s.src), read(s.mp4Trash()))
	}
	if tempLeft(s.src) {
		t.Error("a temp file was left behind")
	}
}

// A crash at each step of the renaming swap: the next start leaves either the original at movie.mp4
// (the job runs again) or the new file at movie.mkv with the original kept (the job done). With 0 days
// too: a stop after the kept original was deleted, before the job was recorded done, still ends done
// with movie.mkv as its file (review e5047e5).
func TestReplaceWithNewTypeCrashAtEveryStep(t *testing.T) {
	for _, days := range []int{7, 0} {
		for _, step := range []string{"journal written", "original moved", "new file in place", "done"} {
			t.Run(strconv.Itoa(days)+" days/"+step, func(t *testing.T) {
				s, w, mkv := mp4ToMkv(t, days)
				swapHook = func(at string) error {
					if at == step {
						return errCrash
					}
					return nil
				}
				defer func() { swapHook = nil }()
				job, err := s.store.Claim("t")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := w.process(context.Background(), job); !errors.Is(err, errCrash) {
					t.Fatalf("the swap must stop at %q: %v", step, err)
				}
				swapHook = nil
				RecoverSwaps(s.store, s.store, days, keepNow)
				s.store.RequeueRunning()
				j := s.job()
				switch {
				case read(s.src) == "original-bytes": // rolled back
					if !gone(mkv) || !gone(s.mp4Trash()) || j.Status != jobs.Queued {
						t.Errorf("rolled back, but mkv %q, kept %q, job %s", read(mkv), read(s.mp4Trash()), j.Status)
					}
				case read(mkv) == "new": // finished (with 0 days the kept original may be deleted already)
					keptOK := read(s.mp4Trash()) == "original-bytes" || days == 0 && gone(s.mp4Trash())
					if !gone(s.src) || !keptOK || j.Status != jobs.Done || j.OutputPath != mkv || j.SizeAfter != 3 {
						t.Errorf("finished, but mp4 %q, kept %q, job %+v", read(s.src), read(s.mp4Trash()), j)
					}
				default:
					t.Fatalf("mp4 %q, mkv %q, kept %q", read(s.src), read(mkv), read(s.mp4Trash()))
				}
				if m, _ := s.store.Originals(jobs.SwapMoving); len(m) != 0 {
					t.Errorf("left moving: %+v", m)
				}
			})
		}
	}
}

// A crash with the original in .syp-trash and nothing at movie.mkv, and meanwhile someone made a new
// movie.mp4: recovery does not put the original over it; both are kept and the row needs attention.
func TestReplaceWithNewTypeRecoveryNeverOverwritesTheOldName(t *testing.T) {
	s, w, mkv := mp4ToMkv(t, 7)
	swapHook = func(at string) error {
		if at == "original moved" {
			return errCrash
		}
		return nil
	}
	defer func() { swapHook = nil }()
	job, _ := s.store.Claim("t")
	w.process(context.Background(), job)
	swapHook = nil
	os.WriteFile(s.src, []byte("someone else's"), 0o644)
	said := RecoverSwaps(s.store, s.store, 7, keepNow)
	if read(s.src) != "someone else's" || read(s.mp4Trash()) != "original-bytes" || !gone(mkv) {
		t.Errorf("mp4 %q, kept %q, mkv %q", read(s.src), read(s.mp4Trash()), read(mkv))
	}
	if rows, _ := s.store.Originals(jobs.SwapAttention); len(rows) != 1 || !strings.Contains(strings.Join(said, ";"), "another file is at this name now") {
		t.Errorf("rows %+v said %v", rows, said)
	}
}

// Get original back: movie.mp4 returns, movie.mkv goes, and movie.mp4 is not converted again.
func TestRestoreAfterANewType(t *testing.T) {
	s, w, mkv := mp4ToMkv(t, 7)
	w.RunOne(context.Background())
	o, err := s.store.OriginalOfJob(s.id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(s.store, o.ID, false); err != nil {
		t.Fatal(err)
	}
	if read(s.src) != "original-bytes" || !gone(mkv) || !gone(s.mp4Trash()) {
		t.Errorf("mp4 %q, mkv %q, kept %q", read(s.src), read(mkv), read(s.mp4Trash()))
	}
	if f, _ := s.store.GetFile(s.src); f == nil || f.Decision != jobs.DecisionRestored {
		t.Errorf("file record %+v", f)
	}
}

// Get original back while another movie.mp4 is there: refused, nothing lost, movie.mkv still in place.
func TestRestoreAfterANewTypeWithTheOldNameTaken(t *testing.T) {
	s, w, mkv := mp4ToMkv(t, 7)
	w.RunOne(context.Background())
	o, _ := s.store.OriginalOfJob(s.id)
	os.WriteFile(s.src, []byte("someone else's"), 0o644)
	_, err := Restore(s.store, o.ID, false)
	var re *RestoreError
	if !errors.As(err, &re) || re.Code != "path_occupied" {
		t.Fatalf("%v", err)
	}
	if read(s.src) != "someone else's" || read(mkv) != "new" || read(s.mp4Trash()) != "original-bytes" {
		t.Errorf("mp4 %q, mkv %q, kept %q", read(s.src), read(mkv), read(s.mp4Trash()))
	}
}

// A crash in the middle of getting the original back after a change of type: the next start finishes
// it or leaves it kept, never loses a file.
func TestRestoreAfterANewTypeCrashAtEveryStep(t *testing.T) {
	for _, step := range []string{"restore journal written", "converted moved aside", "original back"} {
		t.Run(step, func(t *testing.T) {
			s, w, mkv := mp4ToMkv(t, 7)
			w.RunOne(context.Background())
			o, _ := s.store.OriginalOfJob(s.id)
			swapHook = func(at string) error {
				if at == step {
					return errCrash
				}
				return nil
			}
			defer func() { swapHook = nil }()
			if _, err := Restore(s.store, o.ID, false); !errors.Is(err, errCrash) {
				t.Fatalf("restore must stop at %q: %v", step, err)
			}
			swapHook = nil
			RecoverSwaps(s.store, s.store, 7, keepNow)
			r, _ := s.store.OriginalByID(o.ID)
			switch r.State {
			case jobs.OriginalRestored:
				if read(s.src) != "original-bytes" || !gone(mkv) {
					t.Errorf("restored, but mp4 %q, mkv %q", read(s.src), read(mkv))
				}
			case jobs.OriginalKept:
				if read(mkv) != "new" || read(s.mp4Trash()) != "original-bytes" || !gone(s.src) {
					t.Errorf("kept, but mkv %q, kept %q, mp4 %q", read(mkv), read(s.mp4Trash()), read(s.src))
				}
			default:
				t.Errorf("state %s", r.State)
			}
		})
	}
}

// movie.MKV with "Make it an MKV file" is no change of type: it keeps its name, a plain replace.
func TestSameTypeOtherCaseKeepsTheName(t *testing.T) {
	s := newScenarioNamed(t, "movie.MKV")
	sel := rules.DefaultSelectors(3)
	sel.FileType, sel.SkipSmall = "mkv", false
	tmpl := sel.Template()
	w := s.keeping(7)
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Done || j.OutputPath != s.src || read(s.src) != "new" {
		t.Fatalf("%+v %q", j, read(s.src))
	}
	if o, _ := s.store.OriginalOfJob(s.id); o.NewPath != "" {
		t.Errorf("new path %q", o.NewPath)
	}
}
