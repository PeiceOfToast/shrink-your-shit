// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// The Doctor's ?retest=1 runs the encoder test again, and conversions use the result from then on. This goes
// through the real path: the Doctor page's answer and the encoder LocalEncoderFor gives a conversion. The fake
// ffmpeg fails the hevc_qsv test until passing is set.
func TestRetestChangesTheConversionEncoderThroughTheRealPath(t *testing.T) {
	prev := usesDevDRI
	usesDevDRI = false // the render-node check is about /dev/dri, not the test result this is about
	t.Cleanup(func() { usesDevDRI = prev })

	passing := false
	fake := &media.FakeExec{Handler: func(name string, args []string) (string, string, error) {
		a := strings.Join(args, " ")
		switch {
		case strings.Contains(a, "-version"):
			return "ffmpeg version 7.1.5", "", nil
		case strings.Contains(a, "-encoders"):
			return " V....D libx265              H.265 / HEVC\n V....D hevc_qsv           hardware HEVC\n", "", nil
		case strings.Contains(a, "-filters"):
			return " ... ssim              VV->V      SSIM\n", "", nil
		case strings.Contains(a, "-c:v libx265"):
			return "", "", nil
		case strings.Contains(a, "-c:v hevc_qsv") && passing:
			return "", "", nil
		case strings.Contains(a, "-c:v hevc_qsv"):
			return "", "", errors.New("qsv: no device")
		}
		return "", "", fmt.Errorf("no")
	}}
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	chk := &doctor.Checker{Exec: fake, FFmpeg: "/usr/bin/ffmpeg", FFprobe: "/usr/bin/ffprobe", FreeSpace: func(string) (uint64, error) { return 1 << 40, nil }}
	a, _, _ := OpenAccess(t.TempDir(), false)
	srv := httptest.NewServer(Handler(st, a, Engine{Doctor: chk, DataDir: t.TempDir(), Version: "1.2.3"}))
	t.Cleanup(srv.Close)

	page := func(path string) doctor.Doctor {
		t.Helper()
		code, body, _ := do(t, nil, srv, call{method: "GET", path: path})
		var d doctor.Doctor
		if err := json.Unmarshal([]byte(body), &d); err != nil || code != 200 {
			t.Fatalf("%s: %d %s", path, code, body)
		}
		return d
	}
	conversion := func() string {
		enc, _ := LocalEncoderFor(st, chk)("libx265", false, nil)
		return enc
	}

	// The first page run tests QSV; it fails, so conversions use the processor.
	if d := page("/api/v2/doctor"); d.Encoder.Name != "libx265" || conversion() != "" {
		t.Fatalf("before: page %q, conversion %q", d.Encoder.Name, conversion())
	}
	// QSV starts working, but a plain page run keeps the kept result: still the processor.
	passing = true
	if d := page("/api/v2/doctor"); d.Encoder.Name != "libx265" || conversion() != "" {
		t.Fatalf("plain page after QSV works: page %q, conversion %q (the kept result should hold)", d.Encoder.Name, conversion())
	}
	// ?retest=1 runs the test again: the page's answer and the next conversion both use Quick Sync, in the
	// same response (the in-use encoder is read after the test, not before).
	d := page("/api/v2/doctor?retest=1")
	if d.Encoder.Name != "hevc_qsv" {
		t.Errorf("retest page says encoder %q, want hevc_qsv", d.Encoder.Name)
	}
	if got := conversion(); got != "hevc_qsv" {
		t.Errorf("conversion after retest = %q, want hevc_qsv", got)
	}
}
