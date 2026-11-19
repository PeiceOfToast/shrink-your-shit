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

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
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
	in := APIInput{DataDir: t.TempDir(), Device: func(string) string { return "found" }}
	d := c.API(context.Background(), in, false)
	qsv := checkByID(t, d.Checks, "encoder:qsv")
	if qsv.Status != "ok" || !strings.HasPrefix(qsv.Detail, "Intel Quick Sync encoded a test clip in ") || !strings.HasSuffix(qsv.Detail, " s.") {
		t.Errorf("qsv: %+v", qsv)
	}
	if nv := checkByID(t, d.Checks, "encoder:nvenc"); nv.Status != "warn" || nv.Detail != "No NVIDIA card found." || nv.Hint != nil {
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
	if va := checkByID(t, absent.Checks, "encoder:vaapi"); va.Status != "warn" || va.Detail != "No VA-API graphics found." {
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
	if space.Status != "bad" || !strings.HasPrefix(space.Detail, "Movies: Only") || space.Hint == nil {
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
	d := c.API(context.Background(), APIInput{DataDir: t.TempDir(), Libs: []Library{{ID: 9, Name: "Gone", Path: filepath.Join(t.TempDir(), "missing")}}}, false)
	if access := checkByID(t, d.Checks, "library:9:access"); access.Status != "bad" || !strings.HasPrefix(access.Detail, "Gone: ") || access.Hint == nil {
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
