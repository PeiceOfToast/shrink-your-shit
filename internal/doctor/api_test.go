// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package doctor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

func apiChecker(ex *media.FakeExec, free uint64) *Checker {
	return &Checker{Exec: ex, FFmpeg: "/usr/bin/ffmpeg", FFprobe: "/usr/bin/ffprobe",
		FreeSpace: func(string) (uint64, error) { return free, nil }}
}

func checkByID(t *testing.T, cs []DoctorCheck, id string) DoctorCheck {
	t.Helper()
	for _, c := range cs {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check %q in %+v", id, cs)
	return DoctorCheck{}
}

func TestDoctorToolsAndDataAreOK(t *testing.T) {
	c := apiChecker(fakeFFmpeg(true, true, nil, nil), 1<<40)
	d := c.API(context.Background(), APIInput{Version: "0.4.0", DataDir: t.TempDir()}, false)
	for _, id := range []string{"ffmpeg", "ffprobe", "x265", "ssim", "data"} {
		if ch := checkByID(t, d.Checks, id); ch.Status != "ok" || ch.Hint != nil {
			t.Errorf("%s: %+v", id, ch)
		}
	}
	if ch := checkByID(t, d.Checks, "ffmpeg"); ch.Detail != "ffmpeg 7.1.5-0+deb13u1 found." {
		t.Errorf("ffmpeg detail %q", ch.Detail)
	}
	if use := checkByID(t, d.Checks, "encoder:in-use"); use.Status != "ok" || use.Detail != "Conversions use the CPU (x265)." {
		t.Errorf("in-use: %+v", use)
	}
	if ch := checkByID(t, d.Checks, "libraries"); ch.Status != "warn" {
		t.Errorf("no libraries should warn: %+v", ch)
	}
	if d.Version != "0.4.0" || d.EncodersTestedAt == nil || d.Encoder.Name != "libx265" || d.Encoder.Hardware || d.Encoder.Label != "CPU (x265)" {
		t.Errorf("%+v", d)
	}
}

func TestDoctorMissingConverterIsBad(t *testing.T) {
	c := &Checker{Exec: fakeFFmpeg(true, true, nil, nil), FreeSpace: func(string) (uint64, error) { return 1 << 40, nil }}
	d := c.API(context.Background(), APIInput{DataDir: t.TempDir()}, false)
	ch := checkByID(t, d.Checks, "ffmpeg")
	if ch.Status != "bad" || ch.Detail != "ffmpeg is not found." || ch.Hint == nil {
		t.Errorf("%+v", ch)
	}
}

func TestDoctorPassedEncoderIsTimedAndCPUStillConverts(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the platform's encoder families are checked by GOOS")
	}
	c := apiChecker(fakeFFmpeg(true, true, []string{"hevc_qsv"}, map[string]bool{"hevc_qsv": true}), 1<<40)
	in := APIInput{DataDir: t.TempDir(), Device: func(enc string) string {
		if enc == "hevc_qsv" {
			return "found"
		}
		return "missing"
	}}
	d := c.API(context.Background(), in, false)
	qsv := checkByID(t, d.Checks, "encoder:qsv")
	if qsv.Status != "ok" || !strings.HasPrefix(qsv.Detail, "Intel Quick Sync encoded a test clip in ") || !strings.HasSuffix(qsv.Detail, " s.") {
		t.Errorf("qsv: %+v", qsv)
	}
	if nv := checkByID(t, d.Checks, "encoder:nvenc"); nv.Status != "absent" || nv.Detail != "No NVIDIA card found. That is fine." || nv.Hint != nil {
		t.Errorf("nvenc: %+v", nv)
	}
	use := checkByID(t, d.Checks, "encoder:in-use")
	if use.Status != "warn" || use.Detail != "Conversions use the CPU (x265); Intel Quick Sync passed its test." || use.Hint == nil {
		t.Errorf("in-use: %+v", use)
	}
}

func TestDoctorPresentEncoderThatFailsIsBad(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the platform's encoder families are checked by GOOS")
	}
	c := apiChecker(fakeFFmpeg(true, true, []string{"hevc_vaapi"}, nil), 1<<40) // listed, no device to encode on
	present := c.API(context.Background(), APIInput{DataDir: t.TempDir(), Device: func(string) string { return "found" }}, false)
	va := checkByID(t, present.Checks, "encoder:vaapi")
	if va.Status != "bad" || va.Detail != "VA-API is present but the test encode failed: no device" || va.Hint == nil || !strings.Contains(*va.Hint, "/dev/dri") {
		t.Errorf("present: %+v", va)
	}
	absent := c.API(context.Background(), APIInput{DataDir: t.TempDir(), Device: func(string) string { return "missing" }}, false)
	if va := checkByID(t, absent.Checks, "encoder:vaapi"); va.Status != "absent" || va.Detail != "No VA-API graphics found. That is fine." {
		t.Errorf("absent: %+v", va)
	}
}

func TestDoctorInUseHardwareIsOK(t *testing.T) {
	c := apiChecker(fakeFFmpeg(true, true, nil, nil), 1<<40)
	d := c.API(context.Background(), APIInput{DataDir: t.TempDir(), InUse: "hevc_qsv"}, false)
	use := checkByID(t, d.Checks, "encoder:in-use")
	if use.Status != "ok" || use.Detail != "Conversions use Intel Quick Sync." {
		t.Errorf("%+v", use)
	}
	if !d.Encoder.Hardware || d.Encoder.Name != "hevc_qsv" || d.Encoder.Label != "Intel Quick Sync" {
		t.Errorf("%+v", d.Encoder)
	}
}

func TestDoctorKeepsTheEncoderTestUntilRetest(t *testing.T) {
	ex := fakeFFmpeg(true, true, []string{"hevc_qsv"}, map[string]bool{"hevc_qsv": true})
	encodes := 0
	inner := ex.Handler
	ex.Handler = func(name string, args []string) (string, string, error) {
		if strings.Contains(strings.Join(args, " "), "-c:v") {
			encodes++
		}
		return inner(name, args)
	}
	c := apiChecker(ex, 1<<40)
	in := APIInput{DataDir: t.TempDir()}
	c.API(context.Background(), in, false)
	first := encodes
	if first == 0 {
		t.Fatal("the first call did not test the encoders")
	}
	c.API(context.Background(), in, false)
	if encodes != first {
		t.Errorf("a second call tested again: %d encodes, first call %d", encodes, first)
	}
	c.API(context.Background(), in, true)
	if encodes <= first {
		t.Errorf("?retest=1 did not test again: %d encodes", encodes)
	}
}

func TestDoctorLibraryChecksHaveTheirOwnIDs(t *testing.T) {
	dir := videosIn(t, 1000)
	c := apiChecker(fakeFFmpeg(true, true, nil, nil), 10<<20) // 10 MB free; needs about 64 MB
	d := c.API(context.Background(), APIInput{DataDir: t.TempDir(), Libs: []Library{{ID: 7, Name: "Movies", Path: dir}}}, false)
	space := checkByID(t, d.Checks, "library:7:space")
	if space.Status != "bad" || !strings.HasPrefix(space.Detail, "Movies: 0.0 GB free; the largest video here needs ") || space.Hint == nil {
		t.Errorf("space: %+v", space)
	}
	if access := checkByID(t, d.Checks, "library:7:access"); access.Status != "ok" || access.Detail != "Movies: readable and writable." {
		t.Errorf("access: %+v", access)
	}
	if links := checkByID(t, d.Checks, "library:7:hardlinks"); links.Status != "ok" {
		t.Errorf("hardlinks: %+v", links)
	}
}

func TestDoctorHardlinksAreAWarning(t *testing.T) {
	dir := videosIn(t, 1000)
	if err := os.Link(filepath.Join(dir, "moviea.mkv"), filepath.Join(dir, "copy.mkv")); err != nil {
		t.Fatal(err)
	}
	c := apiChecker(fakeFFmpeg(true, true, nil, nil), 1<<40)
	d := c.API(context.Background(), APIInput{DataDir: t.TempDir(), Libs: []Library{{ID: 3, Name: "Films", Path: dir}}}, false)
	links := checkByID(t, d.Checks, "library:3:hardlinks")
	if links.Status != "warn" || !strings.HasPrefix(links.Detail, "Films: 2 files are shared") || links.Hint == nil {
		t.Errorf("%+v", links)
	}
}

func TestDoctorUnreadableLibraryIsBadAccess(t *testing.T) {
	c := apiChecker(fakeFFmpeg(true, true, nil, nil), 1<<40)
	gone := filepath.Join(t.TempDir(), "missing")
	d := c.API(context.Background(), APIInput{DataDir: t.TempDir(), Libs: []Library{{ID: 9, Name: "Gone", Path: gone}}}, false)
	access := checkByID(t, d.Checks, "library:9:access")
	if access.Status != "bad" || !strings.HasPrefix(access.Detail, "Gone ("+gone+"): ") || access.Hint == nil {
		t.Errorf("%+v", access)
	}
	if access.Hint == nil || *access.Hint != "Check that "+gone+" exists, and give the app's user read access to it." {
		t.Errorf("unreadable hint should say read access and name the folder: %v", access.Hint)
	}
}

// A system folder is the wrong folder, not a permission to grant: the hint says to pick another.
func TestDoctorSystemFolderHintSaysPickAnother(t *testing.T) {
	if runtime.GOOS != "linux" || !jobs.SystemFolder("/etc") {
		t.Skip("needs a system folder this test knows (Linux /etc)")
	}
	c := apiChecker(fakeFFmpeg(true, true, nil, nil), 1<<40)
	d := c.API(context.Background(), APIInput{DataDir: t.TempDir(), Libs: []Library{{ID: 10, Name: "Sys", Path: "/etc"}}}, false)
	access := checkByID(t, d.Checks, "library:10:access")
	if access.Status != "bad" || access.Hint == nil || *access.Hint != "Change the folder on the Libraries page, then press Check again." {
		t.Errorf("%+v", access)
	}
}

func TestDoctorJSONUsesTheContractKeys(t *testing.T) {
	ch := DoctorCheck{ID: "ffmpeg", Label: "ffmpeg", Status: "ok", Detail: "d"}
	b, _ := json.Marshal(ch)
	if !strings.Contains(string(b), `"hint":null`) {
		t.Errorf("ok check must carry hint null: %s", b)
	}
}

// largestVideo is the size of the biggest video in dir: the conversion gate's need is worker.SpaceNeeded of it.
func largestVideo(t *testing.T, dir string) int64 {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var big int64
	for _, e := range ents {
		if info, err := e.Info(); err == nil && !e.IsDir() && info.Size() > big {
			big = info.Size()
		}
	}
	return big
}

func TestDoctorSpaceTiersFollowTheConversionGate(t *testing.T) {
	dir := videosIn(t, 1000)
	need := worker.SpaceNeeded(largestVideo(t, dir))
	lib := []Library{{ID: 4, Name: "Movies", Path: dir}}
	for _, tc := range []struct {
		name   string
		free   uint64
		status string
	}{
		{"one byte under the gate's need refuses", need - 1, "bad"},
		{"exactly the need is still under twice it", need, "warn"},
		{"just under twice the need", 2*need - 1, "warn"},
		{"twice the need is ok", 2 * need, "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := apiChecker(fakeFFmpeg(true, true, nil, nil), tc.free)
			d := c.API(context.Background(), APIInput{DataDir: t.TempDir(), Libs: lib}, false)
			space := checkByID(t, d.Checks, "library:4:space")
			if space.Status != tc.status {
				t.Fatalf("status %q, want %q: %+v", space.Status, tc.status, space)
			}
			if tc.status == "ok" {
				return
			}
			want := "Movies: " + gb(tc.free) + " free; the largest video here needs " + gb(need) + "."
			if !strings.Contains(space.Detail, want) || space.Hint == nil {
				t.Errorf("detail %q, want it to contain %q, with a hint", space.Detail, want)
			}
		})
	}
}

func TestDoctorPresentDeviceWithoutEncoderSaysSo(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("no Intel Quick Sync check on macOS")
	}
	c := apiChecker(fakeFFmpeg(true, true, nil, nil), 1<<40)
	in := APIInput{DataDir: t.TempDir(), Device: func(enc string) string {
		if enc == "hevc_qsv" {
			return "found"
		}
		return "missing"
	}}
	ch := checkByID(t, c.API(context.Background(), in, false).Checks, "encoder:qsv")
	if ch.Status != "warn" || ch.Detail != "Intel Quick Sync is present, but ffmpeg has no hevc_qsv encoder." {
		t.Errorf("%+v", ch)
	}
}

// An orphaned original has its own hint: it is not an access problem, so "Fix the folder" doesn't fit it.
func TestDoctorOrphanedOriginalsHaveTheirOwnHint(t *testing.T) {
	dir := t.TempDir()
	trash := filepath.Join(dir, ".syp-trash")
	if err := os.MkdirAll(trash, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trash, "old.1.mkv"), make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	c := apiChecker(fakeFFmpeg(true, true, nil, nil), 1<<40)
	c.KnownOriginals = func() (map[string]bool, error) { return map[string]bool{}, nil }
	d := c.API(context.Background(), APIInput{DataDir: t.TempDir(), Libs: []Library{{ID: 9, Name: "Movies", Path: dir}}}, false)
	orph := checkByID(t, d.Checks, "library:9:orphaned_originals")
	if orph.Hint == nil || *orph.Hint != "Move them back or delete them, then press Check again." {
		t.Errorf("orphaned originals hint: %+v", orph)
	}
}

// A moved-aside output has its own code and hint, and "Move them back" would overwrite the new conversion, so the hint
// never says it. Two of them are one check.
func TestDoctorMovedAsideHasItsOwnCodeAndHint(t *testing.T) {
	dir := t.TempDir()
	trash := filepath.Join(dir, ".syp-trash")
	if err := os.MkdirAll(trash, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(trash, "movie.2.mkv"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(trash, "other.3.mkv"), []byte("b"), 0o644)
	c := apiChecker(fakeFFmpeg(true, true, nil, nil), 1<<40)
	c.KnownOriginals = func() (map[string]bool, error) { return map[string]bool{}, nil }
	c.JobOutput = func(id int64) (string, bool, error) {
		return map[int64]string{2: "/lib/movie.mkv", 3: "/lib/other.mkv"}[id], id == 2 || id == 3, nil
	}
	d := c.API(context.Background(), APIInput{DataDir: t.TempDir(), Libs: []Library{{ID: 9, Name: "Movies", Path: dir}}}, false)
	n := 0
	for _, ch := range d.Checks {
		if ch.ID == "library:9:moved_aside_outputs" {
			n++
			if ch.Hint == nil || strings.Contains(*ch.Hint, "Move them back") || !strings.Contains(*ch.Hint, "Delete them when you no longer need them") {
				t.Errorf("moved-aside hint: %+v", ch)
			}
		}
	}
	if n != 1 {
		t.Errorf("%d checks with the moved-aside ID, want 1", n)
	}
}

// The HEVC and picture-check rows say what each is for, both when present and when missing.
func TestDoctorHEVCAndPictureCheckSentences(t *testing.T) {
	ok := checkByID(t, apiChecker(fakeFFmpeg(true, true, nil, nil), 1<<40).API(context.Background(), APIInput{DataDir: t.TempDir()}, false).Checks, "x265")
	if ok.Detail != "This ffmpeg has the HEVC encoder (x265)." {
		t.Errorf("x265 ok: %+v", ok)
	}
	d := apiChecker(fakeFFmpeg(false, false, nil, nil), 1<<40).API(context.Background(), APIInput{DataDir: t.TempDir()}, false)
	if x := checkByID(t, d.Checks, "x265"); x.Status != "bad" || x.Detail != "This ffmpeg has no HEVC encoder (x265)." {
		t.Errorf("x265 bad: %+v", x)
	}
	if s := checkByID(t, d.Checks, "ssim"); s.Status != "bad" || s.Detail != "This ffmpeg has no picture-check filter (ssim), which the safety check needs." {
		t.Errorf("ssim bad: %+v", s)
	}
	if s := checkByID(t, apiChecker(fakeFFmpeg(true, true, nil, nil), 1<<40).API(context.Background(), APIInput{DataDir: t.TempDir()}, false).Checks, "ssim"); s.Detail != "This ffmpeg has the picture-check filter (ssim)." {
		t.Errorf("ssim ok: %+v", s)
	}
}

// A library with no videos gets the folder hint in its new words.
func TestDoctorFolderHintSaysWhereToChange(t *testing.T) {
	c := apiChecker(fakeFFmpeg(true, true, nil, nil), 1<<40)
	c.KnownOriginals = func() (map[string]bool, error) { return map[string]bool{}, nil }
	d := c.API(context.Background(), APIInput{DataDir: t.TempDir(), Libs: []Library{{ID: 9, Name: "Movies", Path: t.TempDir()}}}, false)
	for _, ch := range d.Checks {
		if strings.HasPrefix(ch.ID, "library:9:") && ch.Hint != nil && strings.Contains(*ch.Hint, "Check again") {
			if *ch.Hint != "Change the folder on the Libraries page, then press Check again." {
				t.Errorf("folder hint: %+v", ch)
			}
			return
		}
	}
	t.Errorf("no library check with the folder hint: %+v", d.Checks)
}

// A folder the app can read but not write keeps the write wording, and names the folder in its detail.
func TestDoctorReadOnlyLibraryKeepsWriteWording(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder this user cannot write (POSIX, not root)")
	}
	dir := videosIn(t, 1000)
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	c := apiChecker(fakeFFmpeg(true, true, nil, nil), 1<<40)
	d := c.API(context.Background(), APIInput{DataDir: t.TempDir(), Libs: []Library{{ID: 4, Name: "Films", Path: dir}}}, false)
	access := checkByID(t, d.Checks, "library:4:access")
	if access.Status != "bad" || !strings.HasPrefix(access.Detail, "Films ("+dir+"): ") || access.Hint == nil || *access.Hint != "Give the app's user write access to the folder." {
		t.Errorf("%+v", access)
	}
}
