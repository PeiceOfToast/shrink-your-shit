// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

const h264Big = `{"streams":[{"codec_type":"video","codec_name":"h264","bit_rate":"8000000"}],"format":{"duration":"60","size":"5"}}`
const hevcBig = `{"streams":[{"codec_type":"video","codec_name":"hevc","bit_rate":"8000000"}],"format":{"duration":"60","size":"5"}}`

func setup(t *testing.T, probe string) (*Scanner, *jobs.Store, string, *media.FakeExec) {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetPaused(false) // the test's worker is started (S2: a new install starts stopped)
	t.Cleanup(func() { st.Close() })
	lib := t.TempDir()
	os.WriteFile(filepath.Join(lib, "a.mkv"), []byte("12345"), 0o644)
	os.WriteFile(filepath.Join(lib, "notes.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(lib, "b.syp-tmp.mkv"), []byte("x"), 0o644)
	f := &media.FakeExec{Out: []byte(probe)}
	l, err := st.AddLibrary("lib", lib, rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	targets := func() ([]Target, error) {
		return []Target{{LibraryID: l.ID, Path: lib, Template: rules.LegacyTemplate(rules.Default(), "replace")}}, nil
	}
	return &Scanner{Store: st, Exec: f, FFprobe: "ffprobe", Targets: targets}, st, lib, f
}

func TestIsVideo(t *testing.T) {
	for p, want := range map[string]bool{"a.mkv": true, "A.MP4": true, "a.txt": false, "a.syp-tmp.mkv": false, "a.hevc.mkv": false, "._a.mp4": false, "B.MKV": true, "Show.S01E01.hevc.x265.mkv": true, "clip.webm": true, "C.WEBM": true} {
		if IsVideo(p) != want {
			t.Errorf("%s: want %v", p, want)
		}
	}
}

func TestQueuesMatchingFileOnce(t *testing.T) {
	s, st, lib, f := setup(t, h264Big)
	if n := settle(s); n != 1 {
		t.Fatalf("queued %d", n)
	}
	if len(f.Recorded()) != 1 {
		t.Errorf("only a.mkv should be probed, got %d calls", len(f.Recorded()))
	}
	l, _ := st.List(10)
	if len(l) != 1 || l[0].Path != filepath.Join(lib, "a.mkv") || l[0].Status != jobs.Queued || l[0].DurationSec != 60 {
		t.Errorf("%+v", l)
	}
	// Second scan: unchanged file is neither probed nor queued again.
	if n := settle(s); n != 0 || len(f.Recorded()) != 1 {
		t.Errorf("rescan queued %d, calls %d", n, len(f.Recorded()))
	}
}

func TestSkipsNonMatching(t *testing.T) {
	s, st, _, _ := setup(t, hevcBig)
	if n := settle(s); n != 0 {
		t.Errorf("queued %d", n)
	}
	if l, _ := st.List(10); len(l) != 0 {
		t.Errorf("%+v", l)
	}
}

func TestChangedFileIsRescanned(t *testing.T) {
	s, _, lib, f := setup(t, hevcBig)
	settle(s)
	p := filepath.Join(lib, "a.mkv")
	os.WriteFile(p, []byte("123456789"), 0o644)
	f.Out = []byte(h264Big)
	if n := settle(s); n != 1 {
		t.Errorf("changed file should be rescanned and queued, got %d", n)
	}
}

func TestMinAgeSkipsFreshFiles(t *testing.T) {
	s, _, _, f := setup(t, h264Big)
	s.MinAge = time.Hour
	if n := settle(s); n != 0 || len(f.Recorded()) != 0 {
		t.Errorf("fresh file should be left alone: %d %d", n, len(f.Recorded()))
	}
}

func TestNoSecondJobWhileOpen(t *testing.T) {
	s, st, lib, _ := setup(t, h264Big)
	settle(s)
	os.WriteFile(filepath.Join(lib, "a.mkv"), []byte("changed-before-worker-ran"), 0o644)
	if n := settle(s); n != 0 {
		t.Errorf("queued a duplicate: %d", n)
	}
	if l, _ := st.List(10); len(l) != 1 {
		t.Errorf("%+v", l)
	}
}

func TestSkipsHiddenFolders(t *testing.T) {
	s, st, lib, _ := setup(t, h264Big)
	for _, d := range []string{".Trash/files", "@eaDir/x", "$RECYCLE.BIN/s", "Movies/Action"} {
		os.MkdirAll(filepath.Join(lib, d), 0o755)
		os.WriteFile(filepath.Join(lib, d, "t.mkv"), []byte("12345"), 0o644)
	}
	settle(s)
	l, _ := st.List(10)
	got := map[string]bool{}
	for _, j := range l {
		rel, _ := filepath.Rel(lib, j.Path)
		got[filepath.ToSlash(rel)] = true
	}
	if len(got) != 2 || !got["a.mkv"] || !got["Movies/Action/t.mkv"] {
		t.Errorf("queued %v", got)
	}
}

// A library removed while a pass is probing its files gets no job: the pass started before the
// removal, but the job is only added if the library still exists.
func TestLibraryRemovedDuringScanGetsNoJob(t *testing.T) {
	sc, st, _, f := setup(t, `{"format":{"duration":"10","bit_rate":"8000000"},"streams":[{"codec_type":"video","codec_name":"h264","bit_rate":"8000000"}]}`)
	sc.Now = func() time.Time { return time.Now().Add(time.Hour) }
	libs, _ := st.Libraries()
	inner := f.Handler
	f.Handler = func(name string, args []string) (string, string, error) {
		st.DeleteLibrary(libs[0].ID) // the delete lands while the file is being probed
		if inner != nil {
			return inner(name, args)
		}
		return string(f.Out), "", nil
	}
	if n := settle(sc); n != 0 {
		t.Errorf("queued %d", n)
	}
	if list, _ := st.List(10); len(list) != 0 {
		t.Errorf("jobs after the library was removed: %+v", list)
	}
}

// A library folder that is a link to a folder is stored as the real folder, and scanned.
func TestLinkedLibraryFolderIsScanned(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("making links needs extra rights on Windows")
	}
	sc, st, lib, _ := setup(t, `{"format":{"duration":"10","bit_rate":"8000000"},"streams":[{"codec_type":"video","codec_name":"h264","bit_rate":"8000000"}]}`)
	sc.Now = func() time.Time { return time.Now().Add(time.Hour) }
	link := filepath.Join(t.TempDir(), "videos")
	if err := os.Symlink(lib, link); err != nil {
		t.Fatal(err)
	}
	libs, _ := st.Libraries()
	st.DeleteLibrary(libs[0].ID)
	real, _ := jobs.RealPath(link)
	l, err := st.AddLibrary("linked", real, rules.SliderTemplate(3), true)
	if err != nil || l.Path == link {
		t.Fatalf("%+v %v: want the real folder stored", l, err)
	}
	sc.Targets = func() ([]Target, error) {
		return []Target{{LibraryID: l.ID, Path: l.Path, Template: rules.LegacyTemplate(rules.Default(), "replace")}}, nil
	}
	if n := settle(sc); n != 1 {
		t.Errorf("queued %d from a linked library folder, want 1", n)
	}
}

// The scan keeps what ffprobe said, for the Preview; a changed file is probed again (above).
func TestScanFillsTheProbeCache(t *testing.T) {
	s, st, lib, f := setup(t, h264Big)
	settle(s)
	p := filepath.Join(lib, "a.mkv")
	fi, _ := os.Stat(p)
	if v, ok := st.GetProbe(p, fi.Size(), fi.ModTime().UnixNano()); !ok || !strings.Contains(v, `"videoCodec":"h264"`) {
		t.Fatalf("%q %v", v, ok)
	}
	n := len(f.Recorded())
	if info, err := Probe(context.Background(), st, f, "ffprobe", p, fi); err != nil || info.VideoCodec != "h264" || len(f.Recorded()) != n {
		t.Errorf("%+v %v: the cached probe is used (%d calls)", info, err, len(f.Recorded())-n)
	}
}

// Kept originals (slice 17) are never scanned, so never converted again or counted.
func TestTrashIsNeverScanned(t *testing.T) {
	s, _, lib, f := setup(t, h264Big)
	os.MkdirAll(filepath.Join(lib, ".syp-trash"), 0o755)
	os.WriteFile(filepath.Join(lib, ".syp-trash", "old.7.mkv"), []byte("kept original"), 0o644)
	settle(s)
	for _, c := range f.Recorded() {
		if strings.Contains(strings.Join(c, " "), ".syp-trash") {
			t.Errorf("probed a kept original: %v", c)
		}
	}
	if !SkipDir(".syp-trash") {
		t.Error("the trash folder must be skipped")
	}
}

// A skipped file's record carries the skip as a code, for the Skipped list (slice 21b). HEVC already in
// MKV, with no audio to make stereo AAC: nothing would change (S4; HEVC in another form is repackaged).
func TestSkipRecordsItsCode(t *testing.T) {
	s, st, lib, _ := setup(t, `{"streams":[{"codec_type":"video","codec_name":"hevc","bit_rate":"8000000"}],"format":{"format_name":"matroska,webm","duration":"60","size":"5"}}`)
	s.Targets = func() ([]Target, error) {
		return []Target{{Path: lib, Template: rules.SliderTemplate(3)}}, nil
	}
	settle(s)
	if f, _ := st.GetFile(filepath.Join(lib, "a.mkv")); f == nil || f.Reason != rules.SkipAlreadyInFormat || !strings.HasPrefix(f.Decision, "skip: ") {
		t.Errorf("%+v", f)
	}
}

// A file whose original was put back (slice 18) is not converted again, even when it changed since.
func TestRestoredFileIsNotQueued(t *testing.T) {
	s, st, lib, _ := setup(t, h264Big)
	p := filepath.Join(lib, "a.mkv")
	st.PutFile(jobs.File{Path: p, Size: 1, ModTime: 1, Decision: jobs.DecisionRestored})
	if n := settle(s); n != 0 {
		t.Errorf("queued %d", n)
	}
}

// A selectors flow's Ignore list (slice 25a): folders, files and patterns in it are not looked at.
func TestIgnoredPathsAreNotLookedAt(t *testing.T) {
	s, st, lib, f := setup(t, h264Big)
	os.MkdirAll(filepath.Join(lib, "Extras", "deep"), 0o755)
	os.WriteFile(filepath.Join(lib, "Extras", "deep", "x.mkv"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(lib, "trailer.sample.mkv"), []byte("1"), 0o644)
	os.MkdirAll(filepath.Join(lib, "Shows", "Extras"), 0o755)
	os.WriteFile(filepath.Join(lib, "Shows", "Extras", "y.mkv"), []byte("1"), 0o644)
	sel := rules.DefaultSelectors(3)
	sel.Ignore = []string{"Extras", "*.sample.mkv"}
	tmpl := sel.Template()
	s.Targets = func() ([]Target, error) { return []Target{{LibraryID: 1, Path: lib, Template: tmpl}}, nil }
	if n := settle(s); n != 1 {
		t.Errorf("queued %d, want only a.mkv", n)
	}
	if len(f.Recorded()) != 1 {
		t.Errorf("probed %d files, want only a.mkv: %v", len(f.Recorded()), f.Recorded())
	}
	for _, p := range []string{"Extras/deep/x.mkv", "trailer.sample.mkv", "Shows/Extras/y.mkv"} {
		if r, _ := st.GetFile(filepath.Join(lib, p)); r != nil {
			t.Errorf("%s was looked at: %+v", p, r)
		}
	}
}

// settle scans twice, as two scans a while apart do: a file is decided on the second look at it
// unchanged (the two-look rule, slice 23). MinAge is 0 in these tests.
func settle(s *Scanner) int {
	s.ScanOnce(context.Background())
	return s.ScanOnce(context.Background())
}

// A file is decided only once it has not changed between two looks at least MinAge apart (slice 23): the
// first look and every change record it as still_changing, without reading it.
func TestStillChangingWaitsForTwoLooks(t *testing.T) {
	s, st, lib, f := setup(t, h264Big)
	clock := time.Now()
	s.Now, s.MinAge = func() time.Time { return clock }, time.Minute
	a := filepath.Join(lib, "a.mkv")
	os.Chtimes(a, clock.Add(-time.Hour), clock.Add(-time.Hour))
	look := func(want int, wantReason string) {
		t.Helper()
		if n := s.ScanOnce(context.Background()); n != want {
			t.Errorf("queued %d, want %d", n, want)
		}
		if r, _ := st.GetFile(a); wantReason != "" && (r == nil || r.Reason != wantReason) {
			t.Errorf("record %+v, want %s", r, wantReason)
		}
	}
	look(0, rules.SkipStillChanging) // first look
	if len(f.Recorded()) != 0 {
		t.Errorf("a file still changing must not be read: %v", f.Recorded())
	}
	clock = clock.Add(10 * time.Second)
	look(0, rules.SkipStillChanging) // unchanged, but not MinAge since the first look
	// it grows (a download goes on): the wait starts again
	os.WriteFile(a, []byte("123456789"), 0o644)
	os.Chtimes(a, clock.Add(-time.Hour), clock.Add(-time.Hour))
	clock = clock.Add(2 * time.Minute)
	look(0, rules.SkipStillChanging)
	clock = clock.Add(2 * time.Minute)
	look(1, "") // unchanged for MinAge: decided (queued)
	// and no longer listed as skipped (ui-shop's S4 run saw a queued file still listed as still_changing)
	if r, _ := st.GetFile(a); r == nil || r.Reason != "" || !strings.HasPrefix(r.Decision, "queued: ") {
		t.Errorf("queued file's record %+v", r)
	}
	if items, total, _ := st.SkippedFiles(nil, 10, 0); total != 0 {
		t.Errorf("skipped list %d %+v", total, items)
	}
}

// A file with another name elsewhere (a hardlink: a seeding torrent, a media manager's copy) is left
// alone as shared_file, unread, and both names stay byte-identical.
func TestSharedFileIsLeftAlone(t *testing.T) {
	s, st, lib, f := setup(t, h264Big)
	other := filepath.Join(t.TempDir(), "seeding.mkv")
	if err := os.Link(filepath.Join(lib, "a.mkv"), other); err != nil {
		t.Skipf("no hardlinks here: %v", err)
	}
	if n := settle(s); n != 0 || len(f.Recorded()) != 0 {
		t.Errorf("queued %d, probed %v", n, f.Recorded())
	}
	if r, _ := st.GetFile(filepath.Join(lib, "a.mkv")); r == nil || r.Reason != rules.SkipSharedFile {
		t.Errorf("record %+v", r)
	}
	a, _ := os.ReadFile(filepath.Join(lib, "a.mkv"))
	b, _ := os.ReadFile(other)
	if string(a) != "12345" || string(b) != "12345" {
		t.Errorf("both names must keep their bytes: %q %q", a, b)
	}
}

// Links are never followed or converted: one leading outside the library is recorded as symlink; one to
// a file inside it is not looked at, and its real file is converted once, under its own name.
func TestLinksAreNeverConverted(t *testing.T) {
	s, st, lib, f := setup(t, h264Big)
	outside := filepath.Join(t.TempDir(), "elsewhere.mkv")
	os.WriteFile(outside, []byte("x"), 0o644)
	if err := os.Symlink(outside, filepath.Join(lib, "out.mkv")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	os.Symlink(filepath.Join(lib, "a.mkv"), filepath.Join(lib, "in.mkv"))
	os.Symlink(filepath.Join(lib, "gone.mkv"), filepath.Join(lib, "broken.mkv"))
	if n := settle(s); n != 1 {
		t.Errorf("queued %d, want only a.mkv", n)
	}
	if len(f.Recorded()) != 1 {
		t.Errorf("probed %d files, want only a.mkv: %v", len(f.Recorded()), f.Recorded())
	}
	if r, _ := st.GetFile(filepath.Join(lib, "out.mkv")); r == nil || r.Reason != rules.SkipSymlink {
		t.Errorf("outside link: %+v", r)
	}
	for _, n := range []string{"in.mkv", "broken.mkv"} {
		if r, _ := st.GetFile(filepath.Join(lib, n)); r != nil {
			t.Errorf("%s must not be listed: %+v", n, r)
		}
	}
	if l, _ := st.List(10); len(l) != 1 || filepath.Base(l[0].Path) != "a.mkv" {
		t.Errorf("jobs %+v", l)
	}
}

// The first look is tied to the file itself, not just its path, size and times: a file deleted and made
// again between two looks (same name, size and modification time) starts its wait again, and so does
// one that disappears for a pass and comes back (review f4c41fd).
func TestStillChangingFollowsTheFileNotThePath(t *testing.T) {
	s, _, lib, _ := setup(t, h264Big)
	// a fake clock and no sleeps: the result never depends on timing (it was flaky on Windows CI)
	clock := time.Now()
	s.Now, s.MinAge = func() time.Time { return clock }, time.Minute
	a := filepath.Join(lib, "a.mkv")
	var seen []string // what each look saw of a.mkv, printed if the test fails (which field moved?)
	scan := func() int {
		clock = clock.Add(2 * time.Minute) // each look well after the previous one
		if st, err := os.Lstat(a); err == nil {
			seen = append(seen, fmt.Sprintf("%+v", stateOf(a, st)))
		}
		return s.ScanOnce(context.Background())
	}
	defer func() {
		if t.Failed() {
			t.Logf("a.mkv as each look saw it:\n%s", strings.Join(seen, "\n"))
		}
	}()
	old := clock.Add(-time.Hour)
	os.Chtimes(a, old, old)
	scan()       // first look
	os.Remove(a) // made again: same name, size and times, but another file
	os.WriteFile(a, []byte("12345"), 0o644)
	os.Chtimes(a, old, old)
	// NTFS can give the new file the old one's file index and (tunneling) creation time; its change time
	// still moves, so it always waits again (review 3ea035c)
	if n := scan(); n != 0 {
		t.Errorf("a file made again was decided on its first look: queued %d", n)
	}
	if n := scan(); n != 1 {
		t.Errorf("unchanged since: queued %d, want 1", n)
	}

	// gone for a pass, then back: its earlier look is forgotten
	b := filepath.Join(lib, "b.mkv")
	os.WriteFile(b, []byte("abcde"), 0o644)
	os.Chtimes(b, old, old)
	scan() // first look at b
	os.Rename(b, b+".away")
	scan() // b not seen: its look goes
	os.Rename(b+".away", b)
	if n := scan(); n != 0 {
		t.Errorf("b came back and was decided at once: queued %d", n)
	}
}

// A guard is looked at again on every scan: a file that stops being shared (its other name removed) is
// no longer skipped as shared (review f4c41fd), and it then waits like any new file: never read before
// -settle has passed since it was first seen eligible (review 7ff706c).
func TestGuardIsReevaluatedEveryScan(t *testing.T) {
	s, st, lib, _ := setup(t, h264Big)
	clock := time.Now()
	s.Now, s.MinAge = func() time.Time { return clock }, time.Minute
	os.Chtimes(filepath.Join(lib, "a.mkv"), clock.Add(-time.Hour), clock.Add(-time.Hour))
	other := filepath.Join(t.TempDir(), "seeding.mkv")
	if err := os.Link(filepath.Join(lib, "a.mkv"), other); err != nil {
		t.Skipf("no hardlinks here: %v", err)
	}
	settle(s)
	if r, _ := st.GetFile(filepath.Join(lib, "a.mkv")); r == nil || r.Reason != rules.SkipSharedFile {
		t.Fatalf("record %+v", r)
	}
	os.Remove(other)
	clock = clock.Add(10 * time.Second) // well within -settle
	if n := s.ScanOnce(context.Background()); n != 0 {
		t.Errorf("first scan after it stopped being shared: queued %d, want 0 (it waits)", n)
	}
	if r, _ := st.GetFile(filepath.Join(lib, "a.mkv")); r == nil || r.Reason != rules.SkipStillChanging {
		t.Errorf("record %+v, want still_changing", r)
	}
	clock = clock.Add(2 * time.Minute)
	if n := s.ScanOnce(context.Background()); n != 1 {
		t.Errorf("after -settle: queued %d, want 1", n)
	}
}

// A library reached through a linked folder (on macOS every temp folder is: /var is a link to
// /private/var) still sees a link to one of its own files as inside it (CI on #54, macOS).
func TestGuardResolvesTheLibraryRoot(t *testing.T) {
	real := t.TempDir()
	os.WriteFile(filepath.Join(real, "a.mkv"), []byte("x"), 0o644)
	root := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(real, root); err != nil {
		t.Skip(err)
	}
	in := filepath.Join(root, "in.mkv")
	os.Symlink(filepath.Join(real, "a.mkv"), in)
	lst, _ := os.Lstat(in)
	if code, ignore := Guard(root, in, lst); code != "" || !ignore {
		t.Errorf("a link inside a library reached through a linked folder: code %q ignore %v, want not listed", code, ignore)
	}
}

// A link out of the library is never looked into: no identity is read through it, so on Windows (where
// reading one opens the file) the target outside is never opened (review aa9cd46).
func TestOutsideLinkIsNeverOpened(t *testing.T) {
	s, _, lib, _ := setup(t, h264Big)
	outside := filepath.Join(t.TempDir(), "elsewhere.mkv")
	os.WriteFile(outside, []byte("x"), 0o644)
	link := filepath.Join(lib, "out.mkv")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip(err)
	}
	var keyed []string
	defer func(f func(string, os.FileInfo) (media.State, error)) { readFileState = f }(readFileState)
	readFileState = func(p string, fi os.FileInfo) (media.State, error) {
		keyed = append(keyed, p)
		return media.State{Key: "k:" + p, Size: fi.Size(), MTime: fi.ModTime()}, nil
	}
	settle(s)
	for _, p := range keyed {
		if p == link || p == outside {
			t.Errorf("an identity was read through the link: %s", p)
		}
	}
	if len(keyed) == 0 {
		t.Error("the watch saw nothing (a.mkv should have been keyed)")
	}
}

// An untouched file reads the same at every look: identity, size and times (if it did not, it would
// look changed at every scan and never be converted). Where the directory listing's copy of size or
// time differs from the file's, that is logged: the scanner reads the file itself.
func TestUntouchedFileStateIsStable(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.mkv")
	os.WriteFile(p, []byte("12345"), 0o644)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(p, old, old)
	var first fileState
	for i := 0; i < 5; i++ {
		st, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		cur := stateOf(p, st)
		if i == 0 {
			first = cur
		} else if !cur.same(first) {
			t.Errorf("look %d differs:\n first %+v\n now   %+v", i, first, cur)
		}
		entries, _ := os.ReadDir(dir)
		if fi, err := entries[0].Info(); err == nil && (fi.Size() != st.Size() || !fi.ModTime().Equal(st.ModTime())) {
			t.Logf("the listing's copy differs from the file: listing %d %v, file %d %v", fi.Size(), fi.ModTime(), st.Size(), st.ModTime())
		}
	}
}

// A file whose state cannot be read (FileState fails: on Windows, a download holding it locked) is never
// taken as unchanged: its size and time come from the listing, and its identity is unknown, so it waits
// while that lasts, even while the listing's size and time keep moving. Once it can be read, it waits
// like any new file (review 9cf6a03).
func TestUnreadableFileStateIsNeverReady(t *testing.T) {
	s, _, lib, _ := setup(t, h264Big)
	clock := time.Now()
	s.Now, s.MinAge = func() time.Time { return clock }, time.Minute
	a := filepath.Join(lib, "a.mkv")
	old := clock.Add(-time.Hour)
	os.Chtimes(a, old, old)
	defer func(f func(string, os.FileInfo) (media.State, error)) { readFileState = f }(readFileState)
	readFileState = func(p string, fi os.FileInfo) (media.State, error) {
		return media.State{}, &os.PathError{Op: "open", Path: p, Err: os.ErrPermission}
	}
	scan := func() int { clock = clock.Add(2 * time.Minute); return s.ScanOnce(context.Background()) }
	for i, grow := range []bool{false, true, false, true, false} {
		if grow { // still being written: the listing shows it growing and its time moving
			f, _ := os.OpenFile(a, os.O_APPEND|os.O_WRONLY, 0)
			f.Write([]byte("more"))
			f.Close()
			old = old.Add(time.Second)
			os.Chtimes(a, old, old)
		}
		if n := scan(); n != 0 {
			t.Fatalf("look %d: a file that cannot be read was queued (%d)", i, n)
		}
	}
	readFileState = media.FileState // it can be read again
	if n := scan(); n != 0 {
		t.Errorf("readable again: decided on its first readable look (%d)", n)
	}
	if n := scan(); n != 1 {
		t.Errorf("readable and unchanged since: queued %d, want 1", n)
	}
}

// A finished walk records when and how many videos it found, for the library card (slice A).
func TestScanRecordsTheLibrarysLastScan(t *testing.T) {
	s, st, _, _ := setup(t, h264Big)
	libs, _ := st.Libraries()
	if libs[0].LastScan != "" {
		t.Fatalf("before any scan: %+v", libs[0])
	}
	s.ScanOnce(context.Background())
	libs, _ = st.Libraries()
	if libs[0].LastScan == "" || libs[0].LastScanVideos != 1 { // a.mkv (the temp file b.syp-tmp.mkv is not counted)
		t.Errorf("after a scan: %+v", libs[0])
	}
	if len(s.Scanning()) != 0 {
		t.Errorf("still scanning: %v", s.Scanning())
	}
}

// A probe cached by an older version is taken again (review 7ab8f72: version 2 had no Dolby Vision
// compatibility id, so a cached profile 8 looked convertible).
func TestOldProbeCacheIsTakenAgain(t *testing.T) {
	s, st, lib, f := setup(t, `{"streams":[{"codec_type":"video","codec_name":"hevc","side_data_list":[{"side_data_type":"DOVI configuration record","dv_profile":8,"dv_bl_signal_compatibility_id":0}]}],"format":{}}`)
	_ = s
	p := filepath.Join(lib, "a.mkv")
	fi, _ := os.Stat(p)
	st.PutProbe(p, fi.Size(), fi.ModTime().UnixNano(), `{"probe":2,"container":"matroska,webm","videoCodec":"hevc","streams":[{"type":"video","codec":"hevc","dolbyVision":true,"dvProfile":8}]}`)
	info, err := Probe(context.Background(), st, f, "ffprobe", p, fi)
	if err != nil || len(f.Calls) == 0 {
		t.Fatalf("the old cache was used: %v, %d calls", err, len(f.Calls))
	}
	if v := info.Video(); v.DVCompat == nil || *v.DVCompat != 0 || info.Probe != media.ProbeVersion {
		t.Errorf("re-probed: %+v", v)
	}
}

// ui-shop's S4 run: a file is queued, then the library's setting is saved (its file decisions are
// forgotten; the job stays). The next scans must not list the waiting file as still_changing: it is
// decided already, and the skipped list would show it next to To do for as long as the job waits.
func TestQueuedFileIsNotStillChangingAfterASave(t *testing.T) {
	s, st, lib, _ := setup(t, h264Big)
	clock := time.Now()
	s.Now, s.MinAge = func() time.Time { return clock }, time.Minute
	a := filepath.Join(lib, "a.mkv")
	os.Chtimes(a, clock.Add(-time.Hour), clock.Add(-time.Hour))
	s.ScanOnce(context.Background())
	clock = clock.Add(2 * time.Minute)
	if n := s.ScanOnce(context.Background()); n != 1 {
		t.Fatalf("queued %d", n)
	}
	libs, _ := st.Libraries()
	if _, err := st.SaveSlider(libs[0].ID, 2, ""); err != nil { // forgets the library's file decisions
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		clock = clock.Add(2 * time.Minute)
		s.ScanOnce(context.Background())
		if r, _ := st.GetFile(a); r != nil && r.Reason == rules.SkipStillChanging {
			t.Fatalf("scan %d: the waiting file is listed as still changing: %+v", i+1, r)
		}
	}
	if items, total, _ := st.SkippedFiles(nil, 10, 0); total != 0 {
		t.Errorf("skipped %d %+v", total, items)
	}
	if open, _ := st.HasOpenJob(a); !open {
		t.Error("its job is still waiting")
	}
}

// V3c: with intake paused a scan queues nothing new, and the first scan after it resumes queues the file.
func TestIntakePause(t *testing.T) {
	s, st, _, _ := setup(t, h264Big)
	st.SetIntakePaused(true)
	if n := settle(s); n != 0 {
		t.Fatalf("queued %d while intake was paused", n)
	}
	if l, _ := st.List(10); len(l) != 0 {
		t.Fatalf("jobs while paused: %+v", l)
	}
	st.SetIntakePaused(false)
	if n := settle(s); n != 1 {
		t.Errorf("queued %d after resuming", n)
	}
}

// A job done before its output's time was recorded (output_mtime 0: before migration 7, or not readable
// then) still owns its output when the size is the one it wrote: an upgrading install doesn't convert
// its old outputs once more (review fd13e2e). One the person replaced by hand (another size) is looked at.
func TestOldDoneJobsOwnTheirOutputs(t *testing.T) {
	s, st, lib, _ := setup(t, h264Big)
	os.Remove(filepath.Join(lib, "a.mkv"))
	done := func(name string) int64 {
		p := filepath.Join(lib, name)
		id, err := st.Enqueue(jobs.Job{Library: lib, Path: p})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Claim("w"); err != nil {
			t.Fatal(err)
		}
		if err := st.Complete(id, p, 5); err != nil { // the output not there yet: its time is 0, as for an old job
			t.Fatal(err)
		}
		return id
	}
	done("old.mkv")
	done("changed.mkv")
	os.WriteFile(filepath.Join(lib, "old.mkv"), []byte("12345"), 0o644)       // as the job wrote it
	os.WriteFile(filepath.Join(lib, "changed.mkv"), []byte("1234567"), 0o644) // replaced by hand
	past := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(lib, "old.mkv"), past, past)
	os.Chtimes(filepath.Join(lib, "changed.mkv"), past, past)
	s.ScanOnce(context.Background())
	if n := s.ScanOnce(context.Background()); n != 1 {
		t.Errorf("%d queued, want 1 (only the file changed by hand)", n)
	}
	if open, _ := st.HasOpenJob(filepath.Join(lib, "old.mkv")); open {
		t.Error("the old job's output was queued again")
	}
	if open, _ := st.HasOpenJob(filepath.Join(lib, "changed.mkv")); !open {
		t.Error("the file replaced by hand was not looked at")
	}
	// its time is now known: a later change of the file (same size, new time) is looked at again
	fi, _ := os.Stat(filepath.Join(lib, "old.mkv"))
	if own, _ := st.IsOwnOutput(filepath.Join(lib, "old.mkv"), 5, fi.ModTime().UnixNano()+1); own {
		t.Error("after the first look, another time still counts as ours")
	}
}

// Settings' scanEvery, applied without a restart: the next pass is due that long after the last one.
func TestSetEveryRetimesTheNextScan(t *testing.T) {
	s, _, _, _ := setup(t, hevcBig)
	passes := make(chan struct{}, 10)
	targets := s.Targets
	s.Targets = func() ([]Target, error) { passes <- struct{}{}; return targets() }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx, time.Hour)
	<-passes // the first pass
	deadline := time.Now().Add(2 * time.Second)
	for s.NextScan().IsZero() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if next := time.Until(s.NextScan()); next < 50*time.Minute {
		t.Fatalf("an hour's interval: next in %s", next)
	}
	s.SetEvery(50 * time.Millisecond)
	select {
	case <-passes: // the shorter interval took effect without waiting out the hour
	case <-time.After(3 * time.Second):
		t.Fatal("no pass after SetEvery")
	}
	if next := time.Until(s.NextScan()); next > time.Second {
		t.Errorf("next in %s after SetEvery(50ms)", next)
	}
}
