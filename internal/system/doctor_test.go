// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
)

// docSink collects what the doctor emits.
type docSink struct {
	mu   sync.Mutex
	seen []events.Event
}

func (s *docSink) Emit(e events.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, e)
	return nil
}

func (s *docSink) all() []events.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]events.Event(nil), s.seen...)
}

// healthy is every check with something real behind it: a writable data folder with room, both tools, a library
// folder, and a card that passed its test. Each test changes the one source it is about.
func healthy(t *testing.T) (Sources, *docSink) {
	t.Helper()
	sink := &docSink{}
	lib := t.TempDir()
	src := Sources{
		Now:        func() time.Time { return time.Date(2026, 11, 27, 4, 0, 0, 0, time.UTC) },
		DataDir:    t.TempDir(),
		DiskFree:   func(string) (uint64, error) { return 400 << 30, nil },
		LowSpaceGB: func() int { return LowSpaceDefault },
		Tools:      func(context.Context) (string, string, error) { return "7.0.2", "7.0.2", nil },
		Libraries: func(context.Context) ([]Library, error) {
			return []Library{{ID: 1, Name: "Films", Path: lib}}, nil
		},
		Cards: func() ([]string, bool, bool) { return []string{"Intel Quick Sync"}, true, true },
		Emit:  sink,
	}
	return src, sink
}

// statuses reads a result as id → status.
func statuses(res Result) map[string]Status {
	out := map[string]Status{}
	for _, c := range res.Checks {
		out[c.ID] = c.Status
	}
	return out
}

// TestDoctorEveryCheckOK: with a real tool, room on disk, a library folder and a tested card, every check is ok.
func TestDoctorEveryCheckOK(t *testing.T) {
	src, _ := healthy(t)
	res := NewDoctor(src).Run(context.Background())
	got := statuses(res)
	want := map[string]Status{"disk": OK, "tools": OK, "gpu": OK, "library-1": OK}
	for id, status := range want {
		if got[id] != status {
			t.Errorf("%s = %q, want %q (checks: %+v)", id, got[id], status, res.Checks)
		}
	}
	if len(res.Checks) != len(want) {
		t.Errorf("checks = %d, want %d: %+v", len(res.Checks), len(want), res.Checks)
	}
	if !res.RanAt.Equal(src.Now()) {
		t.Errorf("ranAt = %v, want the clock's %v", res.RanAt, src.Now())
	}
}

// TestDoctorSaysWhatIsWrong: each check's problem is its own answer, in plain words, so the page can show it and
// the fix or hint that goes with it.
func TestDoctorSaysWhatIsWrong(t *testing.T) {
	noTools := func(ctx context.Context) (string, string, error) {
		return "", "", errors.New("ffmpeg not found")
	}
	tests := []struct {
		name   string
		fix    func(*Sources)
		id     string
		status Status
		want   string // a word the detail or hint must carry
	}{
		{"tools missing", func(s *Sources) { s.Tools = noTools }, "tools", Bad, "ffmpeg not found"},
		{"data folder unwritable", func(s *Sources) { s.DataDir = filepath.Join(t.TempDir(), "gone") }, "disk", Bad, "can't be written"},
		{"disk too full", func(s *Sources) {
			s.DiskFree = func(string) (uint64, error) { return 3 << 30, nil }
		}, "disk", Bad, "3 GB"},
		{"free space unreadable", func(s *Sources) {
			s.DiskFree = func(string) (uint64, error) { return 0, errors.New("no such volume") }
		}, "disk", Bad, "couldn't be read"},
		{"library missing", func(s *Sources) {
			s.Libraries = func(context.Context) ([]Library, error) {
				return []Library{{ID: 2, Name: "Gone", Path: filepath.Join(t.TempDir(), "gone")}}, nil
			}
		}, "library-2", Bad, "can't be reached"},
		{"library path is a file", func(s *Sources) {
			file := filepath.Join(t.TempDir(), "not-a-folder")
			if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			s.Libraries = func(context.Context) ([]Library, error) {
				return []Library{{ID: 3, Name: "Odd", Path: file}}, nil
			}
		}, "library-3", Bad, "is not a folder"},
		{"libraries unreadable", func(s *Sources) {
			s.Libraries = func(context.Context) ([]Library, error) { return nil, errors.New("database is locked") }
		}, "libraries", Bad, "database is locked"},
		{"library disk low", func(s *Sources) {
			s.DiskFree = func(string) (uint64, error) { return 3 << 30, nil }
		}, "library-1", Warn, "low space"},
		{"library disk low against a raised limit", func(s *Sources) {
			s.DiskFree = func(string) (uint64, error) { return 40 << 30, nil }
			s.LowSpaceGB = func() int { return 50 }
		}, "library-1", Warn, "low space"},
		{"library free space unreadable", func(s *Sources) {
			s.DiskFree = func(string) (uint64, error) { return 0, errors.New("no such volume") }
		}, "library-1", Warn, "couldn't be read"},
		{"card failed its test", func(s *Sources) {
			s.Cards = func() ([]string, bool, bool) { return nil, true, true }
		}, "gpu", Bad, "did not pass"},
		{"card not tested yet", func(s *Sources) {
			s.Cards = func() ([]string, bool, bool) { return nil, false, false }
		}, "gpu", Warn, "not been tested"},
		{"no card", func(s *Sources) {
			s.Cards = func() ([]string, bool, bool) { return nil, false, true }
		}, "gpu", Absent, "No graphics card found"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src, _ := healthy(t)
			tc.fix(&src)
			res := NewDoctor(src).Run(context.Background())
			c := find(res, tc.id)
			if c == nil {
				t.Fatalf("no %s check in %+v", tc.id, res.Checks)
			}
			if c.Status != tc.status {
				t.Errorf("%s = %q (%s), want %q", tc.id, c.Status, c.Detail, tc.status)
			}
			text := c.Detail
			if c.Hint != nil {
				text += " " + *c.Hint
			}
			if !strings.Contains(text, tc.want) {
				t.Errorf("%s detail+hint = %q, want it to say %q", tc.id, text, tc.want)
			}
		})
	}
}

// TestDoctorSaysOnlyOnAChange: a check whose status changed is one line in the system log, a failure is the
// notifiable health.failed warning, and a check that did not change says nothing — however often it runs.
func TestDoctorSaysOnlyOnAChange(t *testing.T) {
	src, sink := healthy(t)
	free := uint64(400 << 30)
	src.DiskFree = func(string) (uint64, error) { return free, nil }
	d := NewDoctor(src)
	ctx := context.Background()
	if d.Run(ctx); len(sink.all()) != 0 {
		t.Fatalf("the first run of all-ok checks said %d lines, want none", len(sink.all()))
	}
	free = 3 << 30
	d.Run(ctx)
	said := sink.all()
	if len(said) != 1 {
		t.Fatalf("after the disk failed, events = %d, want one: %+v", len(said), said)
	}
	if e := said[0]; e.Source != "health" || e.Kind != events.KindWarning || e.Level != events.LevelWarn ||
		e.Code != "health.failed" || !strings.Contains(e.Message, "Disk space") {
		t.Errorf("failure event = %+v, want a health.failed warning naming Disk space", e)
	}
	d.Run(ctx)
	if n := len(sink.all()); n != 1 {
		t.Errorf("a second run with nothing changed said %d lines, want the one from before", n)
	}
	free = 400 << 30
	d.Run(ctx)
	said = sink.all()
	if len(said) != 2 {
		t.Fatalf("after the disk was fine again, events = %d, want two: %+v", len(said), said)
	}
	if e := said[1]; e.Source != "health" || e.Level != events.LevelInfo || !strings.Contains(e.Message, "is fine again") {
		t.Errorf("recovery event = %+v, want a health note saying the check is fine again", e)
	}
}

// TestDoctorFirstRunSaysWhatIsWrong: a problem already there when the app starts is worth a line; a working check
// is not.
func TestDoctorFirstRunSaysWhatIsWrong(t *testing.T) {
	src, sink := healthy(t)
	dataDir := src.DataDir
	src.DiskFree = func(path string) (uint64, error) {
		if path == dataDir {
			return 3 << 30, nil
		}
		return 400 << 30, nil
	}
	NewDoctor(src).Run(context.Background())
	said := sink.all()
	if len(said) != 1 || said[0].Code != "health.failed" || !strings.Contains(said[0].Message, "Disk space") {
		t.Fatalf("first run said %+v, want one health.failed line for the disk", said)
	}
}

// TestDoctorServesTheLastRunUntilRetested: before the first run a plain read says it has nothing yet; after it, a
// plain read is the last run even as time passes; ?retest=1 runs every check again.
func TestDoctorServesTheLastRunUntilRetested(t *testing.T) {
	src, _ := healthy(t)
	now := src.Now()
	var calls int
	src.Tools = func(context.Context) (string, string, error) {
		calls++
		return "7.0.2", "7.0.2", nil
	}
	src.Now = func() time.Time { return now }
	d := NewDoctor(src)
	ctx := context.Background()
	if _, ok := d.Report(ctx, false); ok {
		t.Fatal("a plain read before the first run said it had a result")
	}
	first := d.Run(ctx)
	if calls != 1 {
		t.Fatalf("the first run ran the tools check %d times, want once", calls)
	}
	now = now.Add(time.Minute)
	second, ok := d.Report(ctx, false)
	if !ok || calls != 1 || !second.RanAt.Equal(first.RanAt) {
		t.Errorf("a second read ran again (calls=%d, ranAt=%v), want the last run's %v", calls, second.RanAt, first.RanAt)
	}
	third, ok := d.Report(ctx, true)
	if !ok || calls != 2 {
		t.Errorf("retest = (%v, %v) with %d tools calls, want a result after running them twice", third.RanAt, ok, calls)
	}
	if !third.RanAt.Equal(now) {
		t.Errorf("retest ranAt = %v, want %v", third.RanAt, now)
	}
}

// TestDoctorWithoutASourceIsAbsent: a check with nothing behind it is listed as absent rather than claiming the
// machine is fine, and it has no fix of its own.
func TestDoctorWithoutASourceIsAbsent(t *testing.T) {
	res := NewDoctor(Sources{}).Run(context.Background())
	if got := statuses(res); got["disk"] != Absent || got["tools"] != Absent || got["gpu"] != Absent {
		t.Errorf("statuses = %v, want disk, tools and gpu absent", got)
	}
	for _, c := range res.Checks {
		if c.Fix != nil {
			t.Errorf("%s has fix %+v with no source behind it", c.ID, c.Fix)
		}
		if c.Status != Absent || c.Detail == "" {
			t.Errorf("%s = %q %q, want an absent check that says why", c.ID, c.Status, c.Detail)
		}
	}
}

// TestDoctorFixOnlyWhereARouteExists: the graphics card has page 10's Test the encoders behind it; the checks with
// no route of their own carry no button.
func TestDoctorFixOnlyWhereARouteExists(t *testing.T) {
	src, _ := healthy(t)
	res := NewDoctor(src).Run(context.Background())
	for _, c := range res.Checks {
		if c.ID != "gpu" {
			if c.Fix != nil {
				t.Errorf("%s has a fix %+v; only the card has a route", c.ID, c.Fix)
			}
			continue
		}
		if c.Fix == nil || c.Fix.Label != "Test the encoders" || c.Fix.Method != "POST" ||
			c.Fix.Path != "/api/v2/workers/local/capabilities/test" {
			t.Errorf("gpu fix = %+v, want Test the encoders (POST /api/v2/workers/local/capabilities/test)", c.Fix)
		}
	}
}

// TestDoctorStartRunsNowAndOnASchedule: Start checks straight away in the background, then again on the interval,
// until ctx ends.
func TestDoctorStartRunsNowAndOnASchedule(t *testing.T) {
	old := Every
	Every = 5 * time.Millisecond
	defer func() { Every = old }()
	src, _ := healthy(t)
	var runs atomic.Int32
	src.Tools = func(context.Context) (string, string, error) {
		runs.Add(1)
		return "7.0.2", "7.0.2", nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	NewDoctor(src).Start(ctx)
	deadline := time.Now().Add(2 * time.Second)
	// Start returns before its first pass ends, so the first run is waited for rather than assumed done.
	for runs.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the schedule ran the checks %d times, want the start's run and more", runs.Load())
		}
		time.Sleep(time.Millisecond)
	}
}

// hangingLibrary makes every library check wait until release is closed, and reports when one has started.
func hangingLibrary(t *testing.T, entered, release chan struct{}) {
	t.Helper()
	old := checkLibrary
	checkLibrary = func(d *Doctor, lib Library) Check {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return old(d, lib)
	}
	t.Cleanup(func() { checkLibrary = old })
}

// TestDoctorStartDoesNotWaitForTheFirstRun: a library folder that does not answer must not hold up the start of the
// app: Start returns at once and the checks answer later.
func TestDoctorStartDoesNotWaitForTheFirstRun(t *testing.T) {
	old := LibraryTimeout
	LibraryTimeout = time.Hour
	defer func() { LibraryTimeout = old }()
	release := make(chan struct{})
	defer close(release)
	hangingLibrary(t, make(chan struct{}, 1), release)
	src, _ := healthy(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	go func() {
		NewDoctor(src).Start(ctx)
		close(started)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Error("Start waited for a library that does not answer, want it to return at once")
	}
}

// TestDoctorReportAnswersDuringARun: GET /doctor is answered while a pass is still waiting on a folder: it serves
// the last run rather than queueing behind the pass.
func TestDoctorReportAnswersDuringARun(t *testing.T) {
	src, _ := healthy(t)
	d := NewDoctor(src)
	ctx := context.Background()
	first := d.Run(ctx)

	// A retest runs now and forces the library folders, so it is the pass that is waiting on the folder.
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	hangingLibrary(t, entered, release)
	retestDone := make(chan struct{})
	go func() {
		defer close(retestDone)
		d.Report(ctx, true)
	}()
	defer func() {
		close(release)
		<-retestDone
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the retest never reached the library check")
	}

	type answer struct {
		res Result
		ok  bool
	}
	served := make(chan answer, 1)
	go func() {
		res, ok := d.Report(ctx, false)
		served <- answer{res, ok}
	}()
	select {
	case a := <-served:
		if !a.ok || !a.res.RanAt.Equal(first.RanAt) {
			t.Errorf("a read during a run = (%v, %v), want the last run's %v", a.res.RanAt, a.ok, first.RanAt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a read waited for the pass that is waiting on a folder")
	}
}

// TestDoctorLibraryThatDoesNotAnswerIsBad: a folder that does not answer within its time is a problem, not a pass
// that waits for it.
func TestDoctorLibraryThatDoesNotAnswerIsBad(t *testing.T) {
	old := LibraryTimeout
	LibraryTimeout = 20 * time.Millisecond
	defer func() { LibraryTimeout = old }()
	release := make(chan struct{})
	defer close(release)
	hangingLibrary(t, make(chan struct{}, 1), release)
	src, _ := healthy(t)
	res := NewDoctor(src).Run(context.Background())
	c := find(res, "library-1")
	if c == nil {
		t.Fatalf("no library-1 check in %+v", res.Checks)
	}
	if c.Status != Bad || !strings.Contains(c.Detail, "did not answer in time") {
		t.Errorf("library-1 = %q %q, want bad with 'did not answer in time'", c.Status, c.Detail)
	}
}

// TestDoctorLibraryWriteProbeIsBad: the folder check uses the library's own write probe, so a folder that takes the
// file but refuses to give it back is a problem, not an ok check with a hidden file left behind.
func TestDoctorLibraryWriteProbeIsBad(t *testing.T) {
	old := checkWritable
	checkWritable = func(string) error { return errors.New("remove .syp-check-1: permission denied") }
	t.Cleanup(func() { checkWritable = old })
	src, _ := healthy(t)
	c := find(NewDoctor(src).Run(context.Background()), "library-1")
	if c == nil || c.Status != Bad || !strings.Contains(c.Detail, "permission denied") {
		t.Errorf("library-1 = %+v, want bad with the probe's error", c)
	}
}

// TestDoctorLibraryFolderThatRefusesFilesIsBad: with the real probe, a folder the library would refuse for writing
// is bad on the health page too, and a folder it accepts is left exactly as it was.
func TestDoctorLibraryFolderThatRefusesFilesIsBad(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only folder")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	src, _ := healthy(t)
	src.Libraries = func(context.Context) ([]Library, error) { return []Library{{ID: 1, Name: "Films", Path: dir}}, nil }
	c := find(NewDoctor(src).Run(context.Background()), "library-1")
	if c == nil || c.Status != Bad || !strings.Contains(c.Detail, "can't be written") {
		t.Errorf("library-1 = %+v, want bad because the folder refuses a new file", c)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("the refused folder holds %v (err %v), want it untouched", entries, err)
	}
}

// TestDoctorLibraryFolderIsLeftClean: the probe's file is gone once the check answers, so a passing folder does not
// collect hidden files.
func TestDoctorLibraryFolderIsLeftClean(t *testing.T) {
	dir := t.TempDir()
	src, _ := healthy(t)
	src.Libraries = func(context.Context) ([]Library, error) { return []Library{{ID: 1, Name: "Films", Path: dir}}, nil }
	c := find(NewDoctor(src).Run(context.Background()), "library-1")
	if c == nil || c.Status != OK {
		t.Fatalf("library-1 = %+v, want ok for a writable folder", c)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("the checked folder holds %v (err %v), want it left empty", entries, err)
	}
}

// TestDoctorLibraryFreeSpaceIsTheFoldersDisk: each library is measured where its folder is, not on the data disk.
func TestDoctorLibraryFreeSpaceIsTheFoldersDisk(t *testing.T) {
	src, _ := healthy(t)
	lib := t.TempDir()
	src.Libraries = func(context.Context) ([]Library, error) { return []Library{{ID: 1, Name: "Films", Path: lib}}, nil }
	var asked []string
	src.DiskFree = func(path string) (uint64, error) {
		asked = append(asked, path)
		return 400 << 30, nil
	}
	NewDoctor(src).Run(context.Background())
	var atLib bool
	for _, p := range asked {
		if p == lib {
			atLib = true
		}
	}
	if !atLib {
		t.Errorf("the free space was read at %v, want the library's own folder %s", asked, lib)
	}
}

// TestDoctorLibrariesWaitTheirHour: the folder check writes into the person's media, so it runs on its own hourly
// schedule; the other checks keep their five minutes, and a retest asks for the folders too.
func TestDoctorLibrariesWaitTheirHour(t *testing.T) {
	src, _ := healthy(t)
	now := src.Now()
	src.Now = func() time.Time { return now }
	var checks int
	src.Libraries = func(context.Context) ([]Library, error) {
		checks++
		return []Library{{ID: 1, Name: "Films", Path: t.TempDir()}}, nil
	}
	d := NewDoctor(src)
	ctx := context.Background()
	d.Run(ctx)
	if checks != 1 {
		t.Fatalf("the first run checked the folders %d times, want once", checks)
	}
	now = now.Add(5 * time.Minute)
	res := d.Run(ctx)
	if checks != 1 {
		t.Errorf("a pass 5 minutes later checked the folders %d times, want the kept answer", checks)
	}
	if c := find(res, "library-1"); c == nil || c.Status != OK {
		t.Errorf("the kept library check is missing from the pass: %+v", res.Checks)
	}
	now = now.Add(time.Hour)
	d.Run(ctx)
	if checks != 2 {
		t.Errorf("an hour later the folders were checked %d times, want once more", checks)
	}
	now = now.Add(5 * time.Minute)
	d.Report(ctx, true)
	if checks != 3 {
		t.Errorf("a retest left the folders checked %d times, want them checked again", checks)
	}
}

// find is the check with id, or nil.
func find(res Result, id string) *Check {
	for i := range res.Checks {
		if res.Checks[i].ID == id {
			return &res.Checks[i]
		}
	}
	return nil
}
