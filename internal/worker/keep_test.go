// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

var keepNow = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func (s *scenario) keeping(days int) *Worker {
	w := s.worker(Replace)
	w.KeepDays = func() int { return days }
	w.Journal = s.store
	w.Now = func() time.Time { return keepNow }
	return w
}

func (s *scenario) trash() string {
	return filepath.Join(s.dir, TrashDir, "movie."+strconv.FormatInt(s.id, 10)+".mkv")
}

func read(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

// A converted original is moved into .syp-trash, recorded with its hash and delete date, and the new
// file takes its name.
func TestTheOriginalIsKept(t *testing.T) {
	s := newScenario(t)
	if _, err := s.keeping(7).RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := s.job(); j.Status != jobs.Done || j.OutputPath != s.src || j.SizeAfter != 3 {
		t.Fatalf("%+v", j)
	}
	if read(s.src) != "new" || read(s.trash()) != "original-bytes" {
		t.Fatalf("at the name %q, in the trash %q", read(s.src), read(s.trash()))
	}
	o, err := s.store.OriginalOfJob(s.id)
	h, _ := SampleHash(s.trash())
	if err != nil || o.Trash != s.trash() || o.Bytes != 14 || o.NewBytes != 3 || o.Hash != h || o.DeleteOn != "2026-11-14T12:00:00Z" {
		t.Errorf("%+v %v", o, err)
	}
	if tempLeft(s.src) {
		t.Error("a temp file was left behind")
	}
}

// keepOriginalsDays 0: exactly as before slice 17 (the rename replaces the original; nothing kept).
func TestKeepZeroDaysReplacesAsBefore(t *testing.T) {
	s := newScenario(t)
	s.keeping(0).RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Done || read(s.src) != "new" {
		t.Fatalf("%+v %q", j, read(s.src))
	}
	if _, err := os.Stat(filepath.Join(s.dir, TrashDir)); err == nil {
		t.Error("no .syp-trash folder when nothing is kept")
	}
	if rows, _ := s.store.Originals(jobs.OriginalKept); len(rows) != 0 {
		t.Errorf("%+v", rows)
	}
}

// A name already in the trash is never overwritten; files of the same name in other folders have
// their own trash.
func TestTrashNamesNeverCollide(t *testing.T) {
	s := newScenario(t)
	os.MkdirAll(filepath.Join(s.dir, TrashDir), 0o755)
	os.WriteFile(s.trash(), []byte("someone else's"), 0o644)
	s.keeping(7).RunOne(context.Background())
	second := strings.TrimSuffix(s.trash(), ".mkv") + ".2.mkv"
	if read(s.trash()) != "someone else's" || read(second) != "original-bytes" || read(s.src) != "new" {
		t.Errorf("trash %q, second %q, name %q", read(s.trash()), read(second), read(s.src))
	}
	if p, err := trashPath(filepath.Join(t.TempDir(), TrashDir), "movie.mkv", s.id); err != nil || filepath.Base(p) != filepath.Base(s.trash()) {
		t.Errorf("another folder's trash starts fresh: %s", p)
	}
}

// If the trash folder cannot be made, the job is kept with a plain reason and nothing is moved.
func TestTrashFolderNotCreatable(t *testing.T) {
	s := newScenario(t)
	os.WriteFile(filepath.Join(s.dir, TrashDir), []byte("a file in the way"), 0o644)
	s.keeping(7).RunOne(context.Background())
	s.assertKept("the folder that keeps originals couldn't be made")
	assertPlain(t, s.job().Error)
	s.assertOriginalSafe()
}

// A read-only library: a clear error, nothing moved.
func TestReadOnlyLibraryMovesNothing(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder this user cannot write (POSIX, not root)")
	}
	s := newScenario(t)
	os.Chmod(s.dir, 0o555)
	t.Cleanup(func() { os.Chmod(s.dir, 0o755) })
	s.keeping(7).RunOne(context.Background())
	if j := s.job(); j.Status == jobs.Done || j.Error == "" {
		t.Errorf("%+v", j)
	}
	if read(s.src) != "original-bytes" {
		t.Error("the original must stay")
	}
	if _, err := os.Stat(filepath.Join(s.dir, TrashDir)); err == nil {
		t.Error("nothing may be created")
	}
}

// The core proof: a crash at every step of the swap, then a restart. Afterwards exactly one of the
// original and the new file is at the file's name, the other is where it belongs, the journal has
// nothing left "moving", and a job whose new file is in place is done (never converted again).
func TestCrashAtEveryStepRecovers(t *testing.T) {
	for _, step := range []string{"journal written", "original moved", "new file in place", "done"} {
		t.Run(step, func(t *testing.T) {
			s := newScenario(t)
			w := s.keeping(7)
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
			// The restart: recovery, then the usual requeue of jobs left running.
			RecoverSwaps(s.store, s.store, 7, keepNow)
			s.store.RequeueRunning()

			at, kept := read(s.src), read(s.trash())
			j := s.job()
			switch at {
			case "original-bytes": // rolled back: the job runs again
				if !strings.HasPrefix(kept, "<") || j.Status != jobs.Queued {
					t.Errorf("rolled back, but trash %q, job %s", kept, j.Status)
				}
			case "new": // finished: the original kept, the job done
				if kept != "original-bytes" || j.Status != jobs.Done || j.SizeAfter != 3 {
					t.Errorf("finished, but trash %q, job %s %d", kept, j.Status, j.SizeAfter)
				}
				if o, err := s.store.OriginalOfJob(s.id); err != nil || o.DeleteOn == "" {
					t.Errorf("%+v %v", o, err)
				}
			default:
				t.Fatalf("at the name: %q (trash %q)", at, kept)
			}
			if m, _ := s.store.Originals(jobs.SwapMoving); len(m) != 0 {
				t.Errorf("left moving: %+v", m)
			}
			// and running the queue again never converts a done file a second time
			s.calls = nil
			s.keeping(7).RunOne(context.Background())
			if at == "new" && s.count("libx265") != 0 {
				t.Error("a done job was converted again")
			}
		})
	}
}

// Recovery leaves alone what it cannot account for.
func TestRecoveryNeedsAttentionWhenBothAreGone(t *testing.T) {
	s := newScenario(t)
	id, _ := s.store.BeginSwap(jobs.Original{JobID: s.id, Path: s.src, Trash: s.trash(), Staged: "x", Bytes: 999, Hash: "nope"})
	said := RecoverSwaps(s.store, s.store, 7, keepNow)
	if len(said) != 1 || !strings.Contains(said[0], "Please check this file") {
		t.Errorf("%q", said)
	}
	if read(s.src) != "original-bytes" {
		t.Error("nothing may be touched")
	}
	if a, _ := s.store.Originals(jobs.SwapAttention); len(a) != 1 || a[0].ID != id {
		t.Errorf("%+v", a)
	}
}

func TestSampleHash(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, 5<<20)
	for i := range big {
		big[i] = byte(i)
	}
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	os.WriteFile(a, big, 0o644)
	big[len(big)-1] ^= 1 // a change at the end is seen
	os.WriteFile(b, big, 0o644)
	ha, _ := SampleHash(a)
	hb, _ := SampleHash(b)
	if ha == "" || ha == hb {
		t.Errorf("%s %s", ha, hb)
	}
	os.WriteFile(filepath.Join(dir, "small"), []byte("x"), 0o644)
	if h, err := SampleHash(filepath.Join(dir, "small")); err != nil || len(h) != 64 {
		t.Errorf("%s %v", h, err)
	}
}

// A .syp-trash this user may not search (common with a Docker UID): the job is kept with a plain
// reason, the original untouched, and the worker goes on (review f2b48c0: it used to loop forever).
func TestUnsearchableTrashFolderKeepsTheJob(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder this user cannot search (POSIX, not root)")
	}
	s := newScenario(t)
	trash := filepath.Join(s.dir, TrashDir)
	os.Mkdir(trash, 0o755)
	os.Chmod(trash, 0o600) // readable, not searchable
	t.Cleanup(func() { os.Chmod(trash, 0o755) })
	done := make(chan struct{})
	go func() { s.keeping(7).RunOne(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the worker is stuck")
	}
	s.assertKept("the folder that keeps originals couldn't be used")
	assertPlain(t, s.job().Error)
	s.assertOriginalSafe()
}

// After a crash with the original in the trash, a different file put at the name while the program
// was stopped (a media manager's new download) is not taken for the converted file: nothing is moved,
// the row needs attention, the job is not marked done (review f2b48c0).
func TestRecoveryLeavesAStrangerAtTheName(t *testing.T) {
	s := newScenario(t)
	w := s.keeping(7)
	swapHook = func(at string) error {
		if at == "original moved" {
			return errCrash
		}
		return nil
	}
	job, _ := s.store.Claim("t")
	_, err := w.process(context.Background(), job)
	swapHook = nil
	if !errors.Is(err, errCrash) {
		t.Fatal(err)
	}
	os.WriteFile(s.src, []byte("a new download"), 0o644)
	said := RecoverSwaps(s.store, s.store, 7, keepNow)
	if len(said) != 1 || !strings.Contains(said[0], "not the converted one") {
		t.Errorf("%q", said)
	}
	if read(s.src) != "a new download" || read(s.trash()) != "original-bytes" {
		t.Errorf("both must stay: %q, %q", read(s.src), read(s.trash()))
	}
	if a, _ := s.store.Originals(jobs.SwapAttention); len(a) != 1 {
		t.Errorf("%+v", a)
	}
	if j := s.job(); j.Status == jobs.Done {
		t.Error("the job must not be marked done")
	}
}

// editUnsampled changes one byte that the sampled hash does not read (between the start and middle
// samples) and gives the file another modification time; its size stays.
func editUnsampled(t *testing.T, p string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{'!'}, 1<<20+100); err != nil {
		t.Fatal(err)
	}
	f.Close()
	later := time.Now().Add(time.Hour)
	os.Chtimes(p, later, later)
}

// big makes a scenario whose original and new file are larger than the three 1 MiB samples, so an
// edit can fall between them.
func big(t *testing.T) *scenario {
	s := newScenario(t)
	os.WriteFile(s.src, append([]byte("original-"), make([]byte, 6<<20)...), 0o644)
	s.content = "new-" + string(make([]byte, 5<<20))
	return s
}

func crashAt(t *testing.T, s *scenario, step string) {
	t.Helper()
	swapHook = func(at string) error {
		if at == step {
			return errCrash
		}
		return nil
	}
	defer func() { swapHook = nil }()
	job, _ := s.store.Claim("t")
	if _, err := s.keeping(7).process(context.Background(), job); !errors.Is(err, errCrash) {
		t.Fatalf("must stop at %q: %v", step, err)
	}
}

// The kept original edited in an unsampled part while the program was stopped (size the same, time
// not): recovery does not take it for the original and moves nothing (review 9cb9617).
func TestRecoveryChecksTheOriginalsTime(t *testing.T) {
	s := big(t)
	crashAt(t, s, "original moved")
	editUnsampled(t, s.trash())
	said := RecoverSwaps(s.store, s.store, 7, keepNow)
	if len(said) != 1 || !strings.Contains(said[0], "Please check this file") {
		t.Errorf("%q", said)
	}
	if _, err := os.Stat(s.src); err == nil {
		t.Error("nothing may be put at the name")
	}
	if a, _ := s.store.Originals(jobs.SwapAttention); len(a) != 1 {
		t.Errorf("%+v", a)
	}
}

// The converted file at the name edited the same way: not taken for the checked new file.
func TestRecoveryChecksTheNewFilesTime(t *testing.T) {
	s := big(t)
	crashAt(t, s, "new file in place")
	editUnsampled(t, s.src)
	said := RecoverSwaps(s.store, s.store, 7, keepNow)
	if len(said) != 1 || !strings.Contains(said[0], "not the converted one") {
		t.Errorf("%q", said)
	}
	if j := s.job(); j.Status == jobs.Done {
		t.Error("the job must not be marked done")
	}
}
