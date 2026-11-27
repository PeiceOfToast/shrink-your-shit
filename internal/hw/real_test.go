// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package hw

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// TestRealProveWithLibx264 proves the processor encoder with the real tools and no GPU. Real ffmpeg writes the stream's
// ENCODER tag as "Lavc<version> libx264", so this is the check that the tag parsing matches what ffmpeg 8 writes.
// Under SYP_REQUIRE_FFMPEG a missing tool fails the test instead of skipping it, as CI's real step needs.
func TestRealProveWithLibx264(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			if os.Getenv("SYP_REQUIRE_FFMPEG") != "" {
				t.Fatalf("%s not found and SYP_REQUIRE_FFMPEG is set", bin)
			}
			t.Skipf("%s not found", bin)
		}
	}
	m := media.MediaFuncs{ProbeFunc: media.Probe, RunFunc: media.Run}
	c := Candidate{Encoder: "libx264", Family: encode.FamilyCPU, Codec: "h264"}
	v := Prove(context.Background(), m, t.TempDir(), c)
	if !v.Available {
		t.Fatalf("libx264 not available: %s", v.Reason)
	}
}

// TestRealHDRSourceCarriesSideData checks the generated HDR10 source with real ffmpeg: libx265 must write both the
// mastering display and the content light side data, or the device probe's HDR step would test nothing.
func TestRealHDRSourceCarriesSideData(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			if os.Getenv("SYP_REQUIRE_FFMPEG") != "" {
				t.Fatalf("%s not found and SYP_REQUIRE_FFMPEG is set", bin)
			}
			t.Skipf("%s not found", bin)
		}
	}
	m := media.MediaFuncs{ProbeFunc: media.Probe, RunFunc: media.Run}
	src := filepath.Join(t.TempDir(), "hdr-source.mkv")
	if err := makeHDRClip(context.Background(), m, src); err != nil {
		t.Fatalf("make the HDR source: %v", err)
	}
	h, err := media.ProbeFrameHDR(context.Background(), src)
	if err != nil {
		t.Fatalf("probe the HDR source: %v", err)
	}
	if !h.MasteringDisplay || !h.ContentLight {
		t.Fatalf("HDR source = %+v; want mastering display and content light side data", h)
	}
}
