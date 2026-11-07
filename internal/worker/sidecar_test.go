// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// srtScenario converts the scenario's file with "Save as separate .srt files": its English subtitle
// goes beside the new file as movie.eng.srt.
func srtScenario(t *testing.T) (*scenario, *Worker, string) {
	s := newScenario(t)
	out := goodOutput()
	out.streams = append(out.streams[:2], out.streams[3]) // the new file has no subtitle inside
	s.outSpec = out
	sel := rules.DefaultSelectors(3)
	sel.Subtitles, sel.SkipSmall = "srt", false
	tmpl := sel.Template()
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	w.Sidecars = s.store
	return s, w, filepath.Join(s.dir, "movie.eng.srt")
}

// A crash between putting the .srt file in place and the swap: the next start removes our file (it is
// still exactly the one we wrote), and a Retry then converts without seeing its own file as taken
// (review 25b-e336f8c).
func TestSrtLeftByACrashIsRecovered(t *testing.T) {
	s, w, srt := srtScenario(t)
	swapHook = func(at string) error {
		if at == "subtitles placed" {
			return errCrash
		}
		return nil
	}
	w.RunOne(context.Background())
	swapHook = nil
	if _, err := os.Stat(srt); err != nil {
		t.Fatalf("the crash should leave the .srt in place: %v", err)
	}
	s.assertOriginalSafe()
	said := RecoverSidecars(s.store)
	if _, err := os.Stat(srt); err == nil || len(said) != 1 {
		t.Errorf("recovery left %s (%v)", srt, said)
	}
	if left, _ := s.store.Sidecars(); len(left) != 0 {
		t.Errorf("records left: %+v", left)
	}
	s.store.Retry(s.id)
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("retry: %s %q", j.Status, j.Error)
	}
	if b, _ := os.ReadFile(srt); !strings.Contains(string(b), "subtitle from sub.0.srt") {
		t.Errorf("srt %q", b)
	}
	if left, _ := s.store.Sidecars(); len(left) != 0 {
		t.Errorf("records left after the swap: %+v", left)
	}
}

// A crash after the swap but before the record went: recovery sees the new video in place and keeps
// the subtitles (they are no longer inside it).
func TestSrtAfterASwapIsKept(t *testing.T) {
	s, w, srt := srtScenario(t)
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	// the record as it was just before it would have been dropped
	st, _ := os.Stat(srt)
	h, _ := fullHash(srt)
	vst, _ := os.Stat(s.src)
	vh, _ := SampleHash(s.src)
	s.store.AddSidecars([]jobs.Sidecar{{JobID: s.id, Path: srt, Bytes: st.Size(), MTime: st.ModTime().UnixNano(), Hash: h,
		VideoPath: s.src, VideoBytes: vst.Size(), VideoHash: vh}})
	RecoverSidecars(s.store)
	if _, err := os.Stat(srt); err != nil {
		t.Error("the subtitles of a swapped file must stay")
	}
}

// Only our own .srt is ever removed: one replaced by someone else's file meanwhile stays.
func TestSrtCleanupKeepsAReplacedFile(t *testing.T) {
	s, w, srt := srtScenario(t)
	swapHook = func(at string) error {
		if at == "subtitles placed" {
			os.WriteFile(srt, []byte("someone else's subtitles"), 0o644) // replaced right after we placed it
			return errCrash
		}
		return nil
	}
	w.RunOne(context.Background())
	swapHook = nil
	RecoverSidecars(s.store)
	if b, _ := os.ReadFile(srt); string(b) != "someone else's subtitles" {
		t.Errorf("a file that is not ours was removed or changed: %q", b)
	}
	s.assertOriginalSafe()
}

// Same size and modification time but different content is still someone else's file: compared by the
// whole content, it stays (review 25b-1e31bdb).
func TestSrtSameSizeAndTimeButOtherContentSurvives(t *testing.T) {
	s, w, srt := srtScenario(t)
	swapHook = func(at string) error {
		if at == "subtitles placed" {
			st, _ := os.Stat(srt)
			b, _ := os.ReadFile(srt)
			other := []byte(strings.Repeat("x", len(b))) // same length
			os.WriteFile(srt, other, 0o644)
			os.Chtimes(srt, st.ModTime(), st.ModTime()) // same time
			return errCrash
		}
		return nil
	}
	w.RunOne(context.Background())
	swapHook = nil
	RecoverSidecars(s.store)
	if b, _ := os.ReadFile(srt); !strings.HasPrefix(string(b), "xxx") {
		t.Errorf("the other file was removed or changed: %q", b)
	}
}

// A file of ours that cannot be removed keeps its record, and the next start tries again (review
// 25b-1e31bdb).
func TestSrtRemovalFailureKeepsTheRecord(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder the test cannot delete from (POSIX, not root)")
	}
	s, w, srt := srtScenario(t)
	swapHook = func(at string) error {
		if at == "subtitles placed" {
			return errCrash
		}
		return nil
	}
	w.RunOne(context.Background())
	swapHook = nil
	os.Chmod(s.dir, 0o555) // removing from the folder fails
	said := RecoverSidecars(s.store)
	os.Chmod(s.dir, 0o755)
	if left, _ := s.store.Sidecars(); len(left) != 1 || !strings.Contains(strings.Join(said, ";"), "could not remove") {
		t.Fatalf("the record must stay after a failed removal: %+v %v", left, said)
	}
	if _, err := os.Stat(srt); err != nil {
		t.Fatal("the file should still be there")
	}
	RecoverSidecars(s.store) // the next start
	if _, err := os.Stat(srt); err == nil {
		t.Error("the next recovery should remove it")
	}
	if left, _ := s.store.Sidecars(); len(left) != 0 {
		t.Errorf("records left: %+v", left)
	}
}

// A big .srt changed only between the parts a sampled hash reads (5 MiB, one byte at 1.5 MiB) is still
// told apart: the whole content is compared (review 25b-1e31bdb).
func TestSrtChangedOutsideTheSamplesSurvives(t *testing.T) {
	s, w, srt := srtScenario(t)
	s.srtBytes = 5 << 20
	swapHook = func(at string) error {
		if at == "subtitles placed" {
			st, _ := os.Stat(srt)
			b, _ := os.ReadFile(srt)
			b[3<<19] ^= 0xff // 1.5 MiB: between the first and the middle sample
			os.WriteFile(srt, b, 0o644)
			os.Chtimes(srt, st.ModTime(), st.ModTime())
			return errCrash
		}
		return nil
	}
	w.RunOne(context.Background())
	swapHook = nil
	RecoverSidecars(s.store)
	if _, err := os.Stat(srt); err != nil {
		t.Error("a file that is not exactly ours was removed")
	}
}

// When we cannot tell whether a recorded .srt is ours (Lstat or reading it fails), the file is left
// alone and its record stays; once the check works again, the next start removes it (review 25b-28abb71).
func TestSrtUncheckableKeepsTheRecord(t *testing.T) {
	for _, fail := range []string{"lstat", "hash"} {
		t.Run(fail, func(t *testing.T) {
			s, w, srt := srtScenario(t)
			swapHook = func(at string) error {
				if at == "subtitles placed" {
					return errCrash
				}
				return nil
			}
			w.RunOne(context.Background())
			swapHook = nil
			ioErr := &os.PathError{Op: fail, Path: srt, Err: syscall.EIO}
			if fail == "lstat" {
				sidecarLstat = func(string) (os.FileInfo, error) { return nil, ioErr }
			} else {
				sidecarHash = func(string) (string, error) { return "", ioErr }
			}
			said := RecoverSidecars(s.store)
			sidecarLstat, sidecarHash = os.Lstat, fullHash
			if left, _ := s.store.Sidecars(); len(left) != 1 || !strings.Contains(strings.Join(said, ";"), "could not tell") {
				t.Fatalf("the record must stay when the file cannot be checked: %+v %v", left, said)
			}
			if _, err := os.Stat(srt); err != nil {
				t.Fatal("a file we could not check was removed")
			}
			RecoverSidecars(s.store) // the next start, with the check working
			if _, err := os.Stat(srt); err == nil {
				t.Error("the next recovery should remove it")
			}
			if left, _ := s.store.Sidecars(); len(left) != 0 {
				t.Errorf("records left: %+v", left)
			}
		})
	}
}

// A name taken while the file converted, by a file with exactly what we would write (same content and
// time): the job fails with design's sentence, no record is left for the file we never placed, and the
// next start leaves that file alone (review 25b-28abb71).
func TestSrtLateCollisionLeavesNoRecord(t *testing.T) {
	s, w, srt := srtScenario(t)
	swapHook = func(at string) error {
		if at != "before swap" {
			return nil
		}
		staged, _ := filepath.Glob(filepath.Join(s.dir, ".movie.*.syp-tmp", "sub.0.srt"))
		if len(staged) != 1 {
			t.Fatalf("staged .srt: %v", staged)
		}
		b, _ := os.ReadFile(staged[0])
		st, _ := os.Stat(staged[0])
		os.WriteFile(srt, b, 0o644)
		os.Chtimes(srt, st.ModTime(), st.ModTime())
		return nil
	}
	w.RunOne(context.Background())
	swapHook = nil
	if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, "is already there") {
		t.Fatalf("job: %s %q", j.Status, j.Error)
	}
	if left, _ := s.store.Sidecars(); len(left) != 0 {
		t.Fatalf("records left for a file we never placed: %+v", left)
	}
	RecoverSidecars(s.store)
	if _, err := os.Stat(srt); err != nil {
		t.Error("the other file was removed")
	}
	s.assertOriginalSafe()
}
