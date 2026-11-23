// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// convertHDR10 converts an HDR10 plan with mastering data on a worker whose report is caps, with the server's
// main encoder primary, and returns the job-log notes, the encoders reported, and the error.
func convertHDR10(t *testing.T, primary *string, caps []map[string]any, x *recordExec) (notes, encs []string, err error) {
	t.Helper()
	srv, rec := recordingServer(t)
	c := &Client{Server: srv.URL, Version: "test", Caps: caps}
	l := &Lease{ID: "L1", Primary: primary}
	cv := Converter{Exec: x, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Work: t.TempDir()}
	plan := rules.Plan{Video: rules.VideoEncode{Codec: "libx265", CRF: 20, Preset: "medium", HDR: &rules.HDR{
		Transfer: "smpte2084", Primaries: "bt2020", Matrix: "bt2020nc",
		MasterDisplay: "G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1)", CLL: "1000,400",
	}}}
	out := filepath.Join(t.TempDir(), "out.mkv")
	if err := os.WriteFile(out, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = c.convert(context.Background(), cv, l, rules.Snapshot{Plan: plan}, "/in.mkv", out)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.notes, rec.encoders, err
}

// R4 leftover (review #483 note 1): an HDR10 plan with mastering data, sent through a lease to a worker whose
// main encoder is NVENC (not a family that keeps mastering data), gets the processor's arguments and the note.
func TestRemoteHDR10ToAnUnprovenFamilyGetsProcessorArgs(t *testing.T) {
	x := &recordExec{}
	notes, encs, err := convertHDR10(t, ptr("hevc_nvenc"), []map[string]any{encoderCap("hevc_nvenc", "passed"), tenBitCap("hevc_nvenc")}, x)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.calls) != 1 || !slices.Contains(x.calls[0], "libx265") || slices.Contains(x.calls[0], "hevc_nvenc") {
		t.Errorf("ffmpeg args %v: want the processor (libx265)", x.calls)
	}
	want := "HDR10 with mastering data: hevc_nvenc isn't on the list of encoders proven to keep that data; using the processor"
	chosen := "Chosen: x265 (processor). Settings: Encoder: libx265" // the note above said why; not repeated
	if len(notes) != 2 || notes[0] != want || !strings.HasPrefix(notes[1], chosen) || !slices.Equal(encs, []string{""}) {
		t.Errorf("notes %q, encoders %q: want %q, then the Chosen line, and the processor reported", notes, encs, want)
	}
}

// The same plan on a proven family (QSV, with its 10-bit test passed) keeps the graphics encoder, no note.
func TestRemoteHDR10ToAProvenFamilyKeepsTheGraphicsEncoder(t *testing.T) {
	x := &recordExec{}
	notes, encs, err := convertHDR10(t, ptr("hevc_qsv"), []map[string]any{encoderCap("hevc_qsv", "passed"), tenBitCap("hevc_qsv")}, x)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.calls) != 1 || !slices.Contains(x.calls[0], "hevc_qsv") {
		t.Errorf("ffmpeg args %v: want hevc_qsv", x.calls)
	}
	chosen := "Chosen: Intel Quick Sync (graphics), HEVC — hevc_qsv passed its test on this computer and can do this file. Settings: Encoder: hevc_qsv"
	if len(notes) != 1 || !strings.HasPrefix(notes[0], chosen) || !slices.Equal(encs, []string{"hevc_qsv"}) {
		t.Errorf("notes %q, encoders %q: want only the Chosen line and hevc_qsv reported", notes, encs)
	}
}

func tenBitCap(name string) map[string]any {
	c := encoderCap(name, "passed")
	c["tenBit"] = true
	return c
}
