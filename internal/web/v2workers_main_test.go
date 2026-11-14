// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// The main encoder prefers HEVC when a graphics HEVC encoder passed (the doctor's order does not decide it), and
// otherwise takes the first graphics encoder of any format that passed. A saved choice is kept while it passes.
func TestPrimaryEncoderPrefersHEVC(t *testing.T) {
	caps := capabilities(doctor.Tools{Hardware: []string{"h264_nvenc", "hevc_qsv"}}, true)
	if got := primaryEncoder(nil, caps); got != "hevc_qsv" {
		t.Errorf("h264 passed first: default = %q, want hevc_qsv", got)
	}
	pick := func(s string) *string { return &s }
	if got := primaryEncoder(pick("h264_nvenc"), caps); got != "h264_nvenc" {
		t.Errorf("saved h264 encoder that passed: got %q, want h264_nvenc", got)
	}
	if got := primaryEncoder(pick("hevc_amf"), caps); got != "hevc_qsv" {
		t.Errorf("saved encoder that did not pass: got %q, want the default hevc_qsv", got)
	}
	onlyH264 := capabilities(doctor.Tools{Hardware: []string{"h264_nvenc", "av1_qsv"}, Software: []string{"libx265"}}, true)
	if got := primaryEncoder(nil, onlyH264); got != "h264_nvenc" {
		t.Errorf("only h264 and av1 passed: default = %q, want h264_nvenc", got)
	}
	none := capabilities(doctor.Tools{Software: []string{"libx265"}}, true)
	if got := primaryEncoder(nil, none); got != "cpu" {
		t.Errorf("nothing passed: default = %q, want cpu", got)
	}
}

// fakeGraphics is a doctor whose ffmpeg has the given encoders, and whose test frames pass for every one of them.
func fakeGraphics(t *testing.T, encoders ...string) *doctor.Checker {
	t.Helper()
	list := " V....D libx265  HEVC\n V....D libx264  H.264\n V....D libsvtav1  AV1\n"
	pass := []string{"-c:v libx265", "-c:v libx264", "-c:v libsvtav1"}
	for _, e := range encoders {
		list += " V....D " + e + "  graphics\n"
		pass = append(pass, "-c:v "+e)
	}
	fake := &media.FakeExec{Handler: func(name string, args []string) (string, string, error) {
		switch a := strings.Join(args, " "); {
		case strings.Contains(a, "-version"):
			return "ffmpeg version 7.1.5", "", nil
		case strings.Contains(a, "-encoders"):
			return list, "", nil
		case strings.Contains(a, "-filters"):
			return " ... ssim              VV->V      SSIM\n", "", nil
		}
		for _, p := range pass {
			if strings.Contains(strings.Join(args, " "), p) {
				return "", "", nil
			}
		}
		return "", "", errors.New("exit status 1")
	}}
	return &doctor.Checker{Exec: fake, FFmpeg: "/usr/bin/ffmpeg", FFprobe: "/usr/bin/ffprobe", FreeSpace: func(string) (uint64, error) { return 1 << 40, nil }}
}

// A plan's format uses the main encoder's family, and only when that format's encoder passed here; the PATCH
// accepts an H.264 encoder that passed, and cpu switches every format to the processor.
func TestLocalEncoderForUsesTheMainFamilyPerFormat(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a, _, _ := OpenAccess(t.TempDir(), false)
	chk := fakeGraphics(t, "hevc_qsv", "h264_qsv")
	srv := httptest.NewServer(Handler(st, a, Engine{Doctor: chk, DataDir: t.TempDir(), Version: "1.2.3", Logs: &doctor.LogTail{}}))
	t.Cleanup(srv.Close)
	if code, b, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/local/capabilities/test", body: "{}", ctype: "application/json"}); code != 200 {
		t.Fatalf("capability test %d %s", code, b)
	}
	enc := LocalEncoderFor(st, chk)

	if got := enc("libx265"); got != "hevc_qsv" {
		t.Errorf("HEVC plan, main hevc_qsv: %q", got)
	}
	if got := enc("libx264"); got != "h264_qsv" {
		t.Errorf("H.264 plan, main hevc_qsv, h264_qsv passed: %q, want h264_qsv", got)
	}
	if got := enc("libsvtav1"); got != "" {
		t.Errorf("AV1 plan, av1_qsv did not pass: %q, want the processor", got)
	}
	if got := enc("libvpx-vp9"); got != "" {
		t.Errorf("VP9 plan: %q, want the processor", got)
	}

	patch := func(body string) int {
		code, _, _ := do(t, nil, srv, call{method: "PATCH", path: "/api/v2/workers/local", ctype: "application/json", body: body})
		return code
	}
	if code := patch(`{"primaryEncoder":"h264_qsv"}`); code != 200 {
		t.Fatalf("PATCH h264_qsv: %d, want 200", code)
	}
	if got := enc("libx265"); got != "hevc_qsv" {
		t.Errorf("main h264_qsv, HEVC plan: %q, want the same family hevc_qsv", got)
	}
	if code := patch(`{"primaryEncoder":"hevc_amf"}`); code != 409 {
		t.Errorf("PATCH hevc_amf (not passed): %d, want 409", code)
	}
	if code := patch(`{"primaryEncoder":"cpu"}`); code != 200 {
		t.Fatalf("PATCH cpu: %d", code)
	}
	if got := enc("libx264"); got != "" {
		t.Errorf("main cpu, H.264 plan: %q, want the processor", got)
	}
}
