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

// Until F2 the main encoder is HEVC or cpu. H.264 passing first in the doctor's order must not make it the
// default, and a saved H.264 choice is not used.
func TestPrimaryEncoderIsHEVCUntilF2(t *testing.T) {
	caps := capabilities(doctor.Tools{Hardware: []string{"h264_nvenc", "hevc_qsv"}}, true)
	if got := primaryEncoder(nil, caps); got != "hevc_qsv" {
		t.Errorf("h264 passed first: default = %q, want hevc_qsv", got)
	}
	pick := func(s string) *string { return &s }
	if got := primaryEncoder(pick("h264_nvenc"), caps); got != "hevc_qsv" {
		t.Errorf("saved h264 encoder: got %q, want the default hevc_qsv", got)
	}
	onlyH264 := capabilities(doctor.Tools{Hardware: []string{"h264_nvenc", "av1_qsv"}, Software: []string{"libx265"}}, true)
	if got := primaryEncoder(nil, onlyH264); got != "cpu" {
		t.Errorf("only h264 and av1 passed: default = %q, want cpu", got)
	}
}

// A card that passes only H.264 or AV1 is refused as the main encoder, the same way as one that failed.
func TestPatchPrimaryEncoderRefusesH264UntilF2(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a, _, _ := OpenAccess(t.TempDir(), false)
	fake := &media.FakeExec{Handler: func(name string, args []string) (string, string, error) {
		switch a := strings.Join(args, " "); {
		case strings.Contains(a, "-version"):
			return "ffmpeg version 7.1.5", "", nil
		case strings.Contains(a, "-encoders"):
			return " V....D libx265  HEVC\n V....D hevc_nvenc  NVENC\n V....D h264_nvenc  NVENC H.264\n", "", nil
		case strings.Contains(a, "-filters"):
			return " ... ssim              VV->V      SSIM\n", "", nil
		case strings.Contains(a, "-c:v libx265"), strings.Contains(a, "-c:v hevc_nvenc"), strings.Contains(a, "-c:v h264_nvenc"):
			return "", "", nil
		}
		return "", "", errors.New("exit status 1")
	}}
	chk := &doctor.Checker{Exec: fake, FFmpeg: "/usr/bin/ffmpeg", FFprobe: "/usr/bin/ffprobe", FreeSpace: func(string) (uint64, error) { return 1 << 40, nil }}
	srv := httptest.NewServer(Handler(st, a, Engine{Doctor: chk, DataDir: t.TempDir(), Version: "1.2.3", Logs: &doctor.LogTail{}}))
	t.Cleanup(srv.Close)

	if code, b, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/local/capabilities/test", body: "{}", ctype: "application/json"}); code != 200 {
		t.Fatalf("capability test %d %s", code, b)
	}
	patch := func(enc string) (int, string) {
		code, b, _ := do(t, nil, srv, call{method: "PATCH", path: "/api/v2/workers/local", ctype: "application/json", body: `{"primaryEncoder":"` + enc + `"}`})
		return code, b
	}
	if code, b := patch("h264_nvenc"); code != 409 || errCode(b) != "encoder_not_working" {
		t.Errorf("PATCH h264_nvenc: %d %s, want 409 encoder_not_working", code, b)
	}
	if code, b := patch("hevc_nvenc"); code != 200 {
		t.Errorf("PATCH hevc_nvenc: %d %s, want 200", code, b)
	}
}
