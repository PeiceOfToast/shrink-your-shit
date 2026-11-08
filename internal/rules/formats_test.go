// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

var allEncoders = map[string]bool{"libx264": true, "libsvtav1": true, "libvpx-vp9": true}

func withFormat(f, speed string) Template {
	s := DefaultSelectors(3)
	s.Format, s.Speed = f, speed
	return Template{Kind: KindSelectors, Selectors: &s, CompilerVersion: CompilerVersion}
}

// S6: each video format's encoder and arguments, and the codec the check expects.
func TestVideoFormats(t *testing.T) {
	SetAvailableFormats(allEncoders)
	defer SetAvailableFormats(nil)
	src := h264(1920, 1080, 8000) // an MPEG-2 file: every format converts it
	src.Streams[0].Codec, src.VideoCodec = "mpeg2video", "mpeg2video"
	for _, c := range []struct{ format, speed, want, codec string }{
		{"hevc", "normal", "-c:v:0 libx265 -crf 23 -preset medium -tag:v:0 hvc1", "hevc"},
		{"h264", "slower", "-c:v:0 libx264 -crf 18 -preset slow -tag:v:0 avc1", "h264"},
		{"av1", "faster", "-c:v:0 libsvtav1 -crf 32 -preset 8 -svtav1-params tune=0 -tag:v:0 av01", "av1"},
		{"vp9", "normal", "-c:v:0 libvpx-vp9 -crf 33 -b:v 0 -deadline good -cpu-used 2", "vp9"},
	} {
		p := withFormat(c.format, c.speed).Compile(src)
		args := strings.Join(p.FFmpegArgs("in.mkv", "out.mkv"), " ")
		if p.Skip != "" || !strings.Contains(args, c.want) {
			t.Errorf("%s %s: %q\n%s", c.format, c.speed, p.Skip, args)
		}
		if c.format == "vp9" && strings.Contains(args, "-tag:") {
			t.Errorf("vp9 has no tag: %s", args)
		}
		if e := p.Expect(src, 0); e.Streams[0].Codec != c.codec {
			t.Errorf("%s: expected codec %s", c.format, e.Streams[0].Codec)
		}
	}
	// a format already Plans had before S6 keeps its JSON (no "format" field): fingerprints unchanged
	if p := withFormat("hevc", "normal").Compile(src); p.Video.Format != "" || p.Video.Extra != nil {
		t.Errorf("hevc plan %+v", p.Video)
	}
}

// Which sources each target converts, repackages or leaves (S6).
func TestFormatSources(t *testing.T) {
	SetAvailableFormats(allEncoders)
	defer SetAvailableFormats(nil)
	hevcIn := func(container string) media.Info {
		i := h264(1920, 1080, 8000)
		i.Streams[0].Codec, i.VideoCodec, i.Container = "hevc", "hevc", container
		return i
	}
	av1 := h264(1920, 1080, 8000)
	av1.Streams[0].Codec, av1.VideoCodec = "av1", "av1"
	for _, c := range []struct {
		name string
		t    Template
		info media.Info
		code string
		copy bool
	}{
		{"HEVC to H.264 is converted", withFormat("h264", "normal"), hevcIn("matroska,webm"), "", false},
		{"HEVC to HEVC, nothing to do", withFormat("hevc", "normal"), hevcIn("matroska,webm"), SkipAlreadyInFormat, true},
		{"HEVC made smaller is encoded", func() Template { t := withFormat("hevc", "normal"); t.Selectors.Resolution = "720"; return t }(), hevcIn("matroska,webm"), "", false},
		{"HEVC MP4 to MKV is repackaged", func() Template {
			t := withFormat("hevc", "normal")
			t.Selectors.FileType, t.Selectors.AfterConverting = "mkv", "beside"
			return t
		}(), hevcIn("mov,mp4,m4a,3gp,3g2,mj2"), "", true},
		{"AV1 to HEVC: modern_format", withFormat("hevc", "normal"), av1, SkipModernFormat, false},
		{"AV1 to H.264: modern_format", withFormat("h264", "normal"), av1, SkipModernFormat, false},
		{"AV1 to VP9 is converted", withFormat("vp9", "normal"), av1, "", false},
	} {
		p := c.t.Compile(c.info)
		if p.SkipCode != c.code || p.Video.Copy != c.copy {
			t.Errorf("%s: %q copy %v", c.name, p.SkipCode, p.Video.Copy)
		}
	}
}

// availableFormats follows ffmpeg's encoders; a format this computer can't make can't be saved.
func TestAvailableFormats(t *testing.T) {
	defer SetAvailableFormats(nil)
	SetAvailableFormats(map[string]bool{"libx264": true, "libvpx-vp9": true, "libaom-av1": true})
	if got := strings.Join(Available(), ","); got != "hevc,h264,vp9" {
		t.Errorf("%s", got)
	}
	s := DefaultSelectors(3)
	s.Format = "av1"
	if err := s.Validate(); err == nil {
		t.Error("av1 without libsvtav1 was accepted")
	}
	s.Format = "vp9"
	if err := s.Validate(); err != nil {
		t.Errorf("vp9: %v", err)
	}
}

// Max bitrate (S6): research's ladder for the new file's height and format, with room for HDR and 50-60
// fps, never above the source; old Mbps values become levels.
func TestBitrateCap(t *testing.T) {
	sdr := &media.Stream{FrameRate: 24}
	for _, c := range []struct {
		level, format string
		w, h          int
		v             *media.Stream
		src, want     int
	}{
		{"none", "hevc", 1920, 1080, sdr, 0, 0},
		{"medium", "hevc", 1920, 1080, sdr, 0, 5000},
		{"low", "h264", 1280, 720, sdr, 0, 2500},
		{"high", "av1", 3840, 2160, sdr, 0, 15000},
		{"medium", "vp9", 1920, 1080, sdr, 0, 5000},                           // HEVC's column
		{"medium", "hevc", 1920, 1080, &media.Stream{FrameRate: 60}, 0, 5000}, // the ladder's number (design E); allowances raise the rough line
		{"medium", "hevc", 3840, 2160, &media.Stream{BitDepth: 10, FrameRate: 24}, 0, 12000},
		{"high", "hevc", 1920, 1080, sdr, 3000, 3000}, // never more than the file has
		{"low", "hevc", 640, 360, sdr, 0, 800},
	} {
		if got := CapKbps(c.level, c.format, c.w, c.h, c.v, c.src); got != c.want {
			t.Errorf("%s %s %dx%d: %d, want %d", c.level, c.format, c.w, c.h, got, c.want)
		}
	}
	for old, want := range map[string]string{"40": "high", "20": "high", "10": "high", "6": "medium", "4": "medium", "none": "none", "low": "low"} {
		if got := OldBitrateLevel(old); got != want {
			t.Errorf("%s: %s", old, got)
		}
	}
}

// The rough-picture warning (design E): the cap below research's "looks rough" line for a video of the
// flow's libraries, after HDR and 50-60 fps allowances; the worst case is named.
func TestRoughWarning(t *testing.T) {
	sdr1080 := Shape{Row: "1080"}
	hdr4k60 := Shape{Row: "2160", HDR: true, HighFPS: true}
	for _, c := range []struct {
		level, format string
		shapes        []Shape
		want          string
	}{
		{"low", "hevc", []Shape{sdr1080}, ""}, // 3 Mbps is above 2
		{"low", "hevc", []Shape{sdr1080, hdr4k60}, "Low may look blocky in dark or fast scenes for 4K HDR 60 fps files (8 Mbps). Choose Medium or No limit to avoid that."},
		{"low", "hevc", []Shape{{Row: "2160", HDR: true}}, ""}, // 6 x 1.25 = 7.5, below 8
		{"low", "h264", []Shape{{Row: "1080", HighFPS: true}}, "Low may look blocky in dark or fast scenes for 1080p 60 fps files (5 Mbps). Choose Medium or No limit to avoid that."}, // 4 x 1.4 = 5.6
		{"none", "hevc", []Shape{hdr4k60}, ""},
		{"high", "hevc", []Shape{hdr4k60}, ""},
	} {
		if got := RoughWarning(c.level, c.format, c.shapes); got != c.want {
			t.Errorf("%s %s %v: %q", c.level, c.format, c.shapes, got)
		}
	}
}
