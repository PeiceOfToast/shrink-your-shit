// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCanTransition(t *testing.T) {
	ok := [][2]Status{{Queued, Running}, {Running, Done}, {Running, Failed}, {Running, Queued}, {Failed, Queued}, {Running, Kept}, {Kept, Queued}}
	for _, c := range ok {
		if !CanTransition(c[0], c[1]) {
			t.Errorf("%s -> %s should be allowed", c[0], c[1])
		}
	}
	bad := [][2]Status{{Queued, Done}, {Queued, Failed}, {Done, Queued}, {Done, Running}, {Failed, Done}, {Failed, Running}, {Queued, Kept}, {Kept, Done}, {Kept, Running}, {Done, Kept}}
	for _, c := range bad {
		if CanTransition(c[0], c[1]) {
			t.Errorf("%s -> %s should be rejected", c[0], c[1])
		}
	}
}

func TestLifecycle(t *testing.T) {
	s := open(t)
	if _, err := s.Claim("w"); !errors.Is(err, ErrNoJob) {
		t.Fatalf("empty queue: %v", err)
	}
	id, err := s.Enqueue(Job{Library: "/lib", Path: "/lib/a.mkv", Reason: "r", SizeBefore: 100})
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.Claim("builtin")
	if err != nil || j.ID != id || j.Status != Running || j.Worker != "builtin" {
		t.Fatalf("claim: %+v %v", j, err)
	}
	if _, err := s.Claim("other"); !errors.Is(err, ErrNoJob) {
		t.Fatal("running job must not be claimable twice")
	}
	s.Progress(id, 42)
	if j, _ := s.Get(id); j.Progress != 42 {
		t.Errorf("progress %v", j.Progress)
	}
	if err := s.Complete(id, "/lib/a.mkv", 40); err != nil {
		t.Fatal(err)
	}
	j, _ = s.Get(id)
	if j.Status != Done || j.Progress != 100 || j.SizeAfter != 40 {
		t.Errorf("after complete: %+v", j)
	}
	if err := s.Fail(id, "x"); err == nil {
		t.Error("done -> failed must be rejected")
	}
	if err := s.Retry(id); err == nil {
		t.Error("done -> queued must be rejected")
	}
}

func TestFailAndRetry(t *testing.T) {
	s := open(t)
	id, _ := s.Enqueue(Job{Path: "/a"})
	if err := s.Fail(id, "no"); err == nil {
		t.Error("queued -> failed must be rejected")
	}
	s.Claim("w")
	if err := s.Fail(id, "ffmpeg exploded"); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.Get(id); j.Status != Failed || j.Error != "ffmpeg exploded" {
		t.Errorf("%+v", j)
	}
	if err := s.Retry(id); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.Get(id); j.Status != Queued || j.Error != "" {
		t.Errorf("%+v", j)
	}
}

func TestRequeueRunning(t *testing.T) {
	s := open(t)
	s.Enqueue(Job{Path: "/a"})
	s.Claim("w")
	n, err := s.RequeueRunning()
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := s.Claim("w"); err != nil {
		t.Errorf("should be claimable again: %v", err)
	}
}

func TestFilesAndList(t *testing.T) {
	s := open(t)
	if f, err := s.GetFile("/x"); f != nil || err != nil {
		t.Fatalf("%v %v", f, err)
	}
	s.PutFile(File{Path: "/x", Size: 1, ModTime: 2, Codec: "h264", Decision: "queued"})
	s.PutFile(File{Path: "/x", Size: 3, ModTime: 4, Codec: "hevc", Decision: "skip"})
	f, _ := s.GetFile("/x")
	if f.Size != 3 || f.Codec != "hevc" {
		t.Errorf("%+v", f)
	}
	s.Enqueue(Job{Path: "/1"})
	s.Enqueue(Job{Path: "/2"})
	l, _ := s.List(10)
	if len(l) != 2 || l[0].Path != "/2" {
		t.Errorf("newest first: %+v", l)
	}
}

func TestKeepAndRetry(t *testing.T) {
	s := open(t)
	id, _ := s.Enqueue(Job{Path: "/a"})
	if err := s.Keep(id, "no"); err == nil {
		t.Error("queued -> kept must be rejected")
	}
	s.Claim("w")
	if err := s.Keep(id, "the output has 1 audio stream, the source has 2"); err != nil {
		t.Fatal(err)
	}
	j, _ := s.Get(id)
	if j.Status != Kept || !strings.Contains(j.Error, "2") {
		t.Errorf("%+v", j)
	}
	if err := s.Complete(id, "/a", 1); err == nil {
		t.Error("a kept job must not become done")
	}
	if err := s.Retry(id); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.Get(id); j.Status != Queued || j.Error != "" {
		t.Errorf("%+v", j)
	}
}

func TestProbeCache(t *testing.T) {
	s := open(t)
	if _, ok := s.GetProbe("/m/a.mkv", 10, 20); ok {
		t.Fatal("empty cache")
	}
	s.PutProbe("/m/a.mkv", 10, 20, `{"videoCodec":"h264"}`)
	if v, ok := s.GetProbe("/m/a.mkv", 10, 20); !ok || v != `{"videoCodec":"h264"}` {
		t.Errorf("%q %v", v, ok)
	}
	if _, ok := s.GetProbe("/m/a.mkv", 11, 20); ok {
		t.Error("a file of another size is probed again")
	}
	if _, ok := s.GetProbe("/m/a.mkv", 10, 21); ok {
		t.Error("a changed file is probed again")
	}
	s.PutProbe("/m/a.mkv", 11, 21, `{}`)
	if v, ok := s.GetProbe("/m/a.mkv", 11, 21); !ok || v != `{}` {
		t.Errorf("replaced: %q %v", v, ok)
	}
}

func TestSwapJournal(t *testing.T) {
	s := open(t)
	id, err := s.BeginSwap(Original{JobID: 7, LibraryID: 2, Path: "/m/a.mkv", Trash: "/m/.syp-trash/a.7.mkv", Staged: "/m/.a.syp-tmp/new.mkv", Bytes: 100, ModTime: 5, Hash: "h", NewBytes: 40})
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Originals(SwapMoving); len(m) != 1 || m[0].ID != id || m[0].Trash != "/m/.syp-trash/a.7.mkv" || m[0].LibraryID != 2 {
		t.Fatalf("%+v", m)
	}
	if _, err := s.OriginalOfJob(7); err != ErrNotFound {
		t.Error("a swap in progress is not a kept original yet")
	}
	del := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	if err := s.FinishSwap(id, del); err != nil {
		t.Fatal(err)
	}
	if o, err := s.OriginalOfJob(7); err != nil || o.State != OriginalKept || o.DeleteOn != "2026-11-14T00:00:00Z" || o.KeptAt == "" {
		t.Errorf("%+v %v", o, err)
	}
	if m, _ := s.Originals(SwapMoving); len(m) != 0 {
		t.Errorf("%+v", m)
	}
	if err := s.SetSwapState(999, SwapRolledBack); err != ErrNotFound {
		t.Errorf("%v", err)
	}
}

// Several workers claiming at once never take the same job, and none gets an error for another
// having been faster (parallelJobs, slice 24).
func TestClaimIsAtomic(t *testing.T) {
	s := open(t)
	for i := 0; i < 20; i++ {
		s.Enqueue(Job{Library: "/m", Path: fmt.Sprintf("/m/%d.mkv", i)})
	}
	var mu sync.Mutex
	got := map[int64]int{}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				j, err := s.Claim("w")
				if err != nil {
					if !errors.Is(err, ErrNoJob) {
						t.Errorf("a concurrent claim failed: %v", err) // the old two-step claim: "changed under us"
					}
					return
				}
				mu.Lock()
				got[j.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(got) != 20 {
		t.Errorf("%d jobs claimed, want 20", len(got))
	}
	for id, n := range got {
		if n != 1 {
			t.Errorf("job %d claimed %d times", id, n)
		}
	}
}

func TestPausedAndUserStopped(t *testing.T) {
	s := open(t)
	if s.Paused() {
		t.Fatal("not paused at first")
	}
	s.SetPaused(true)
	if !s.Paused() {
		t.Error("paused")
	}
	s.SetPaused(false)
	id, _ := s.Enqueue(Job{Library: "/m", Path: "/m/a.mkv"})
	if err := s.UserStopped(id); err != nil {
		t.Fatal(err)
	}
	if f, _ := s.GetFile("/m/a.mkv"); f == nil || f.Reason != UserStoppedReason {
		t.Errorf("%+v", f)
	}
	if err := s.UserStopped(id); err != ErrNotFound {
		t.Errorf("gone: %v", err)
	}
	done, _ := s.Enqueue(Job{Library: "/m", Path: "/m/b.mkv"})
	s.Claim("w")
	s.Complete(done, "/m/b.mkv", 1)
	if err := s.UserStopped(done); err != ErrNotStoppable {
		t.Errorf("done: %v", err)
	}
}

// While a library is being removed (BeginRemove) none of its jobs is claimed and it cannot be started;
// it goes only in one transaction with no job of it running; a removal given up or failing leaves
// everything as it was (review 5cdd27f).
func TestLibraryRemoval(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	other, _ := s.AddLibrary("Shows", "/s", rules.SliderTemplate(3), true)
	run, _ := s.Enqueue(Job{Library: "/m", LibraryID: l.ID, Path: "/m/1.mkv"})
	s.Claim("w")
	waiting, _ := s.Enqueue(Job{Library: "/m", LibraryID: l.ID, Path: "/m/2.mkv"})
	theirs, _ := s.Enqueue(Job{Library: "/s", LibraryID: other.ID, Path: "/s/1.mkv"})

	release, err := s.BeginRemove(l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginRemove(l.ID); err != ErrLibraryBusy {
		t.Errorf("a second removal at once: %v", err)
	}
	if err := s.StartLibrary(l.ID); err != ErrLibraryBusy {
		t.Errorf("started during removal: %v", err)
	}
	if j, err := s.Claim("w2"); err != nil || j.ID != theirs {
		t.Errorf("claim during removal must skip its jobs and take the other library's: %+v %v", j, err)
	}
	if err := s.DeleteLibrary(l.ID); err != ErrLibraryBusy {
		t.Errorf("removed with a job running: %v", err)
	}
	// given up: everything as before
	release()
	release() // twice is harmless
	if err := s.StartLibrary(l.ID); err != nil {
		t.Errorf("start after: %v", err)
	}
	if j, err := s.Claim("w3"); err != nil || j.ID != waiting {
		t.Errorf("its waiting job is claimable again: %+v %v", j, err)
	}

	// a store error inside the removal: nothing removed
	s.Release(waiting)
	s.Release(run)
	release, _ = s.BeginRemove(l.ID)
	s.db.Exec(`CREATE TRIGGER fail_delete BEFORE DELETE ON libraries BEGIN SELECT RAISE(ABORT, 'injected'); END`)
	if err := s.DeleteLibrary(l.ID); err == nil || err == ErrLibraryBusy {
		t.Errorf("injected error: %v", err)
	}
	if _, err := s.GetLibrary(l.ID); err != nil {
		t.Errorf("the library must stay: %v", err)
	}
	for _, id := range []int64{run, waiting} {
		if j, err := s.Get(id); err != nil || j.Status != Queued {
			t.Errorf("job %d must stay waiting: %+v %v", id, j, err)
		}
	}
	s.db.Exec(`DROP TRIGGER fail_delete`)
	// and done: the library and its waiting jobs go together
	if err := s.DeleteLibrary(l.ID); err != nil {
		t.Fatal(err)
	}
	release()
	for _, id := range []int64{run, waiting} {
		if _, err := s.Get(id); err == nil {
			t.Errorf("job %d of the removed library is still there", id)
		}
	}
}

// A job made before libraries were records (no library) is still claimed while some library is being
// removed: the removal guard only holds back that library's jobs (review c41c9db).
func TestRemovalGuardKeepsJobsWithoutALibrary(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	old, err := s.Enqueue(Job{Library: "/old", Path: "/old/a.mkv"}) // LibraryID 0: library_id NULL
	if err != nil {
		t.Fatal(err)
	}
	release, _ := s.BeginRemove(l.ID)
	defer release()
	if j, err := s.Claim("w"); err != nil || j.ID != old {
		t.Errorf("the unlinked job must be claimable: %+v %v", j, err)
	}
}

// KnownOriginals are the .syp-trash files the journal points at, whatever their state.
func TestKnownOriginals(t *testing.T) {
	s := open(t)
	id, _ := s.Enqueue(Job{Library: "/m", Path: "/m/a.mkv"})
	s.Claim("w")
	if _, err := s.BeginSwap(Original{JobID: id, Path: "/m/a.mkv", Trash: "/m/.syp-trash/a.1.mkv", Staged: "x"}); err != nil {
		t.Fatal(err)
	}
	known, err := s.KnownOriginals()
	if err != nil || !known[filepath.Clean("/m/.syp-trash/a.1.mkv")] || len(known) != 1 {
		t.Errorf("%v %v", known, err)
	}
}

// A job waiting to be tried again (DD-2) is not claimed before its time, then it is; its tries count
// across retries and the final failure; Retry by the person starts fresh. The wait is in the database,
// so a restart keeps it.
func TestRetryLaterWaitsItsTime(t *testing.T) {
	s := open(t)
	now := time.Date(2026, 11, 7, 20, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	id, _ := s.Enqueue(Job{Path: "/m/a.mkv"})
	s.Claim("w")
	if err := s.RetryLater(id, now.Add(5*time.Minute), "The disk is full."); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.Get(id); j.Status != Queued || j.Attempts != 1 || j.RetryAt != "2026-11-07T20:05:00Z" || j.Error != "The disk is full." {
		t.Fatalf("%+v", j)
	}
	if r, _ := s.Retrying(); len(r) != 1 || r[0].ID != id {
		t.Errorf("retrying %+v", r)
	}
	if _, err := s.Claim("w"); !errors.Is(err, ErrNoJob) {
		t.Fatalf("claimed before its time: %v", err)
	}
	now = now.Add(5 * time.Minute)
	if j, err := s.Claim("w"); err != nil || j.ID != id || j.Attempts != 1 {
		t.Fatalf("not claimed when due: %+v %v", j, err)
	}
	s.Fail(id, "The disk is full.")
	if j, _ := s.Get(id); j.Attempts != 2 || j.RetryAt != "" {
		t.Errorf("after the final failure: %+v", j)
	}
	s.Retry(id)
	if j, _ := s.Get(id); j.Status != Queued || j.Attempts != 0 || j.RetryAt != "" || j.Error != "" {
		t.Errorf("after Retry: %+v", j)
	}
	if r, _ := s.Retrying(); len(r) != 0 {
		t.Errorf("still listed as retrying: %+v", r)
	}
}
