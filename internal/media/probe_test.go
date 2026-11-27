// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// loadProbe parses one checked-in ffprobe answer. No ffmpeg is needed, so these run on every OS.
func loadProbe(t *testing.T, name string) Info {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "probe", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	info, err := ParseProbe(data)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return info
}

func TestParseProbeMP4(t *testing.T) {
	info := loadProbe(t, "mp4-h264-aac.json")

	if got := info.Format.Names[0]; got != "mov" {
		t.Errorf("container name = %q, want mov", got)
	}
	if info.Format.Duration != 2*time.Second {
		t.Errorf("duration = %v, want 2s", info.Format.Duration)
	}
	if info.Format.Size != 222943 {
		t.Errorf("size = %d, want 222943", info.Format.Size)
	}
	if info.Format.BitRate <= 0 {
		t.Errorf("bit rate = %d, want > 0", info.Format.BitRate)
	}

	if len(info.Video) != 1 {
		t.Fatalf("video tracks = %d, want 1", len(info.Video))
	}
	v := info.Video[0]
	if v.Index != 0 || v.Codec != "h264" || v.Profile != "Constrained Baseline" {
		t.Errorf("video = %+v, want index 0, h264, Constrained Baseline", v)
	}
	if v.Width != 320 || v.Height != 240 {
		t.Errorf("size = %dx%d, want 320x240", v.Width, v.Height)
	}
	if v.FrameRate.Float() != 30 {
		t.Errorf("frame rate = %v, want 30", v.FrameRate.Float())
	}
	if v.PixelFormat != "yuv420p" || v.BitDepth != 8 {
		t.Errorf("pixel format/bit depth = %s/%d, want yuv420p/8", v.PixelFormat, v.BitDepth)
	}
	if v.AttachedPicture {
		t.Error("the main video is marked as cover art")
	}
	if v.HDRKind() != "SDR" {
		t.Errorf("HDR kind = %q, want SDR", v.HDRKind())
	}

	if len(info.Audio) != 1 {
		t.Fatalf("audio tracks = %d, want 1", len(info.Audio))
	}
	a := info.Audio[0]
	if a.Codec != "aac" || a.Channels != 2 || a.ChannelLayout != "stereo" || a.SampleRate != 48000 {
		t.Errorf("audio = %+v, want aac, 2 channels, stereo, 48000", a)
	}
	if a.Language != "und" || !a.Default {
		t.Errorf("audio language/default = %q/%v, want und/true", a.Language, a.Default)
	}
	if len(info.Subtitles) != 0 {
		t.Errorf("subtitles = %d, want 0", len(info.Subtitles))
	}
}

func TestParseProbeTenBit(t *testing.T) {
	info := loadProbe(t, "mkv-hevc10.json")

	if len(info.Video) != 1 {
		t.Fatalf("video tracks = %d, want 1", len(info.Video))
	}
	v := info.Video[0]
	if v.Codec != "hevc" || v.Profile != "Main 10" {
		t.Errorf("video = %s/%s, want hevc/Main 10", v.Codec, v.Profile)
	}
	if v.PixelFormat != "yuv420p10le" || v.BitDepth != 10 {
		t.Errorf("pixel format/bit depth = %s/%d, want yuv420p10le/10", v.PixelFormat, v.BitDepth)
	}
	if got := info.Format.Names[0]; got != "matroska" {
		t.Errorf("container name = %q, want matroska", got)
	}
}

func TestParseProbeHDR10SideData(t *testing.T) {
	info := loadProbe(t, "mkv-hdr10-side-data.json")

	v := info.Video[0]
	if v.ColorPrimaries != "bt2020" || v.ColorTransfer != "smpte2084" || v.ColorSpace != "bt2020nc" {
		t.Errorf("colour = %s/%s/%s, want bt2020/smpte2084/bt2020nc",
			v.ColorPrimaries, v.ColorTransfer, v.ColorSpace)
	}
	if !v.HDR.MasteringDisplay || !v.HDR.ContentLight {
		t.Errorf("HDR side data = %+v, want mastering display and content light", v.HDR)
	}
	if v.HDR.DolbyVision {
		t.Error("HDR10 is marked as Dolby Vision")
	}
	if v.HDRKind() != "HDR10" {
		t.Errorf("HDR kind = %q, want HDR10", v.HDRKind())
	}
	if v.BitDepth != 10 {
		t.Errorf("bit depth = %d, want 10", v.BitDepth)
	}
}

func TestParseProbeLanguagesAndSubtitles(t *testing.T) {
	info := loadProbe(t, "mkv-chapters.json")

	if len(info.Audio) != 2 {
		t.Fatalf("audio tracks = %d, want 2", len(info.Audio))
	}
	if a := info.Audio[0]; a.Language != "eng" || a.Title != "English" || !a.Default {
		t.Errorf("first audio = %+v, want eng/English/default", a)
	}
	if a := info.Audio[1]; a.Language != "ger" || a.Title != "Deutsch" || a.Default {
		t.Errorf("second audio = %+v, want ger/Deutsch/not default", a)
	}
	if len(info.Subtitles) != 1 {
		t.Fatalf("subtitles = %d, want 1", len(info.Subtitles))
	}
	s := info.Subtitles[0]
	if s.Codec != "subrip" || s.Language != "eng" || !s.Text {
		t.Errorf("subtitle = %+v, want subrip/eng/text", s)
	}

	if len(info.Chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(info.Chapters))
	}
	if c := info.Chapters[0]; c.Title != "Intro" || c.Start != 0 || c.End != 500*time.Millisecond {
		t.Errorf("first chapter = %+v, want Intro 0-500ms", c)
	}
	if c := info.Chapters[1]; c.Title != "Main" || c.Start != 500*time.Millisecond {
		t.Errorf("second chapter = %+v, want Main from 500ms", c)
	}
}

func TestParseProbeCoverArt(t *testing.T) {
	info := loadProbe(t, "mp4-cover-art.json")

	if len(info.Video) != 2 {
		t.Fatalf("video tracks = %d, want 2 (the video and its cover)", len(info.Video))
	}
	if info.Video[0].AttachedPicture {
		t.Error("the main video is marked as cover art")
	}
	if !info.Video[1].AttachedPicture || info.Video[1].Codec != "png" {
		t.Errorf("second video = %+v, want a png cover", info.Video[1])
	}
}

// TestParseProbePlainValues checks the parse without any fixture: streams the app does not use are
// left out, bitmap subtitles are not text, and ffprobe's "0/0" frame rate reads as unknown.
func TestParseProbePlainValues(t *testing.T) {
	const answer = `{
	  "format": {"format_name": "matroska,webm", "duration": "N/A", "size": 10, "bit_rate": "N/A"},
	  "streams": [
	    {"index": 0, "codec_type": "data", "codec_name": "bin_data"},
	    {"index": 1, "codec_type": "subtitle", "codec_name": "hdmv_pgs_subtitle",
	     "disposition": {"default": 0, "forced": 1}},
	    {"index": 2, "codec_type": "video", "codec_name": "hevc", "avg_frame_rate": "0/0",
	     "r_frame_rate": "0/0", "pix_fmt": "yuv420p10le"}
	  ]
	}`
	info, err := ParseProbe([]byte(answer))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(info.Video) != 1 || len(info.Subtitles) != 1 {
		t.Fatalf("video/subtitles = %d/%d, want 1/1", len(info.Video), len(info.Subtitles))
	}
	if s := info.Subtitles[0]; s.Text || !s.Forced {
		t.Errorf("PGS subtitle = %+v, want bitmap, forced", s)
	}
	if v := info.Video[0]; v.FrameRate.Float() != 0 || v.BitDepth != 10 {
		t.Errorf("video = %+v, want unknown frame rate and 10-bit pixel format", v)
	}
	if info.Format.Names[1] != "webm" {
		t.Errorf("container names = %v, want matroska,webm", info.Format.Names)
	}
}

func TestProbeWithNoFFprobe(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := Probe(context.Background(), "clip.mp4")
	if !errors.Is(err, ErrFFprobeMissing) {
		t.Fatalf("error = %v, want ErrFFprobeMissing", err)
	}
}

func TestProbeCancelledBeforeItStarts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Probe(ctx, "clip.mp4")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
