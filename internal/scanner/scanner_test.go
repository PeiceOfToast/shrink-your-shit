// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package scanner

import (
	"context"
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
	for p, want := range map[string]bool{"a.mkv": true, "A.MP4": true, "a.txt": false, "a.syp-tmp.mkv": false, "a.hevc.mkv": false, "._a.mp4": false, "B.MKV": true, "Show.S01E01.hevc.x265.mkv": true} {
		if IsVideo(p) != want {
			t.Errorf("%s: want %v", p, want)
		}
	}
}

func TestQueuesMatchingFileOnce(t *testing.T) {
	s, st, lib, f := setup(t, h264Big)
	if n := s.ScanOnce(context.Background()); n != 1 {
		t.Fatalf("queued %d", n)
	}
	if len(f.Calls) != 1 {
		t.Errorf("only a.mkv should be probed, got %d calls", len(f.Calls))
	}
	l, _ := st.List(10)
	if len(l) != 1 || l[0].Path != filepath.Join(lib, "a.mkv") || l[0].Status != jobs.Queued || l[0].DurationSec != 60 {
		t.Errorf("%+v", l)
	}
	// Second scan: unchanged file is neither probed nor queued again.
	if n := s.ScanOnce(context.Background()); n != 0 || len(f.Calls) != 1 {
		t.Errorf("rescan queued %d, calls %d", n, len(f.Calls))
	}
}

func TestSkipsNonMatching(t *testing.T) {
	s, st, _, _ := setup(t, hevcBig)
	if n := s.ScanOnce(context.Background()); n != 0 {
		t.Errorf("queued %d", n)
	}
	if l, _ := st.List(10); len(l) != 0 {
		t.Errorf("%+v", l)
	}
}

func TestChangedFileIsRescanned(t *testing.T) {
	s, _, lib, f := setup(t, hevcBig)
	s.ScanOnce(context.Background())
	p := filepath.Join(lib, "a.mkv")
	os.WriteFile(p, []byte("123456789"), 0o644)
	f.Out = []byte(h264Big)
	if n := s.ScanOnce(context.Background()); n != 1 {
		t.Errorf("changed file should be rescanned and queued, got %d", n)
	}
}

func TestMinAgeSkipsFreshFiles(t *testing.T) {
	s, _, _, f := setup(t, h264Big)
	s.MinAge = time.Hour
	if n := s.ScanOnce(context.Background()); n != 0 || len(f.Calls) != 0 {
		t.Errorf("fresh file should be left alone: %d %d", n, len(f.Calls))
	}
}

func TestNoSecondJobWhileOpen(t *testing.T) {
	s, st, lib, _ := setup(t, h264Big)
	s.ScanOnce(context.Background())
	os.WriteFile(filepath.Join(lib, "a.mkv"), []byte("changed-before-worker-ran"), 0o644)
	if n := s.ScanOnce(context.Background()); n != 0 {
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
	s.ScanOnce(context.Background())
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
	if n := sc.ScanOnce(context.Background()); n != 0 {
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
	if n := sc.ScanOnce(context.Background()); n != 1 {
		t.Errorf("queued %d from a linked library folder, want 1", n)
	}
}

// The scan keeps what ffprobe said, for the Preview; a changed file is probed again (above).
func TestScanFillsTheProbeCache(t *testing.T) {
	s, st, lib, f := setup(t, h264Big)
	s.ScanOnce(context.Background())
	p := filepath.Join(lib, "a.mkv")
	fi, _ := os.Stat(p)
	if v, ok := st.GetProbe(p, fi.Size(), fi.ModTime().UnixNano()); !ok || !strings.Contains(v, `"videoCodec":"h264"`) {
		t.Fatalf("%q %v", v, ok)
	}
	n := len(f.Calls)
	if info, err := Probe(context.Background(), st, f, "ffprobe", p, fi); err != nil || info.VideoCodec != "h264" || len(f.Calls) != n {
		t.Errorf("%+v %v: the cached probe is used (%d calls)", info, err, len(f.Calls)-n)
	}
}

// Kept originals (slice 17) are never scanned, so never converted again or counted.
func TestTrashIsNeverScanned(t *testing.T) {
	s, _, lib, f := setup(t, h264Big)
	os.MkdirAll(filepath.Join(lib, ".syp-trash"), 0o755)
	os.WriteFile(filepath.Join(lib, ".syp-trash", "old.7.mkv"), []byte("kept original"), 0o644)
	s.ScanOnce(context.Background())
	for _, c := range f.Calls {
		if strings.Contains(strings.Join(c, " "), ".syp-trash") {
			t.Errorf("probed a kept original: %v", c)
		}
	}
	if !SkipDir(".syp-trash") {
		t.Error("the trash folder must be skipped")
	}
}

// A skipped file's record carries the skip as a code, for the Skipped list (slice 21b).
func TestSkipRecordsItsCode(t *testing.T) {
	s, st, lib, _ := setup(t, hevcBig)
	s.Targets = func() ([]Target, error) {
		return []Target{{Path: lib, Template: rules.SliderTemplate(3)}}, nil
	}
	s.ScanOnce(context.Background())
	if f, _ := st.GetFile(filepath.Join(lib, "a.mkv")); f == nil || f.Reason != rules.SkipAlreadyHEVC || !strings.HasPrefix(f.Decision, "skip: ") {
		t.Errorf("%+v", f)
	}
}

// A file whose original was put back (slice 18) is not converted again, even when it changed since.
func TestRestoredFileIsNotQueued(t *testing.T) {
	s, st, lib, _ := setup(t, h264Big)
	p := filepath.Join(lib, "a.mkv")
	st.PutFile(jobs.File{Path: p, Size: 1, ModTime: 1, Decision: jobs.DecisionRestored})
	if n := s.ScanOnce(context.Background()); n != 0 {
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
	sel.SkipSmall, sel.Ignore = false, []string{"Extras", "*.sample.mkv"}
	tmpl := sel.Template()
	s.Targets = func() ([]Target, error) { return []Target{{LibraryID: 1, Path: lib, Template: tmpl}}, nil }
	if n := s.ScanOnce(context.Background()); n != 1 {
		t.Errorf("queued %d, want only a.mkv", n)
	}
	if len(f.Calls) != 1 {
		t.Errorf("probed %d files, want only a.mkv: %v", len(f.Calls), f.Calls)
	}
	for _, p := range []string{"Extras/deep/x.mkv", "trailer.sample.mkv", "Shows/Extras/y.mkv"} {
		if r, _ := st.GetFile(filepath.Join(lib, p)); r != nil {
			t.Errorf("%s was looked at: %+v", p, r)
		}
	}
}
