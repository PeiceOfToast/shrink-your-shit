// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"slices"
	"strings"
	"testing"
)

// User review 11-13 item A: Bitrate level aims at the level's figure for the new file's height (-b:v), with
// the peak 1.5x it (-maxrate, -bufsize 2x), never above the source. VBR with a max is the quality at CRF 23
// with the person's max as the peak, and no level.
func TestBitrateLevelAndVBR(t *testing.T) {
	s := DefaultSelectors(3)
	s.Resolution, s.MaxBitrate, s.RateControl = "720", "medium", "level"
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	// HEVC (the default) is 60% of the H.264 figure: 720p Medium is 2400 kbps, the peak 1.5x that
	p := s.Template().Compile(h264(1920, 1080, 8000))
	args := strings.Join(p.FFmpegArgs("in.mkv", "out.mkv"), " ")
	if p.Video.TargetKbps != 2400 || p.Video.MaxRateKbps != 3600 || !strings.Contains(args, "-b:v:0 2400k") || strings.Contains(args, "-crf 23") ||
		!strings.Contains(args, "-maxrate:v:0 3600k -bufsize:v:0 7200k") {
		t.Errorf("720p Medium level, HEVC: %+v\n%s", p.Video, args)
	}
	// a source with less than the figure: neither is more than it has now
	if p := s.Template().Compile(h264(1920, 1080, 2000)); p.Video.TargetKbps != 2000 || p.Video.MaxRateKbps != 2000 {
		t.Errorf("a 2 Mbps source: %+v", p.Video)
	}
	// VBR with a max: CRF 23 (Balanced), the peak is the person's max, the level is not used
	v := s
	v.RateControl, v.VBRMaxKbps = "vbr", 3000
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
	pv := v.Template().Compile(h264(1920, 1080, 8000))
	vargs := strings.Join(pv.FFmpegArgs("in.mkv", "out.mkv"), " ")
	if pv.Video.TargetKbps != 0 || pv.Video.MaxRateKbps != 3000 || pv.Video.PeakLimitKbps != 3000 || !strings.Contains(vargs, "-crf") ||
		strings.Contains(vargs, "-b:v") || !strings.Contains(vargs, "-maxrate:v:0 3000k") {
		t.Errorf("VBR with a 3 Mbps max: %+v\n%s", pv.Video, vargs)
	}
	// a 1500 kbps source under a 3000 max: the peak is the source's, never more than it has (review #211)
	if low := v.Template().Compile(h264(1920, 1080, 1500)); low.Video.MaxRateKbps != 1500 || low.Video.PeakLimitKbps != 3000 ||
		!strings.Contains(strings.Join(low.FFmpegArgs("in.mkv", "out.mkv"), " "), "-maxrate:v:0 1500k") {
		t.Errorf("VBR on a 1500 kbps source: %+v", low.Video)
	}
	if pv.Video.CRF != 23 {
		t.Errorf("VBR's quality is CRF 23, got %d", pv.Video.CRF)
	}
	// AV1 takes a peak limit only with CRF: VBR is refused for it
	SetAvailableFormats(map[string]bool{"libx265": true, "libsvtav1": true})
	defer SetAvailableFormats(nil)
	av := v
	av.Format = "av1"
	if err := av.Validate(); err == nil || !strings.Contains(err.Error(), "isn't available for AV1") {
		t.Errorf("AV1 VBR: %v", err)
	}
	// a level with no quality choice is refused, and so is quality with a level (the old meaning)
	for _, c := range []struct {
		change func(*Selectors)
		words  string
	}{
		{func(s *Selectors) { s.RateControl, s.MaxBitrate = "level", "none" }, "Bitrate level needs Low, Medium or High"},
		{func(s *Selectors) { s.RateControl, s.MaxBitrate = "quality", "medium" }, "A bitrate level with the quality is now Bitrate level"},
		{func(s *Selectors) { s.RateControl = "cbr" }, "rateControl"},
		{func(s *Selectors) { s.RateControl, s.VBRMaxKbps = "vbr", 0 }, "from 100 to 500,000 kbps"},
	} {
		bad := s
		c.change(&bad)
		if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), c.words) {
			t.Errorf("want %q: %v", c.words, err)
		}
	}
}

// A flow saved before item A keeps its files: quality_legacy and vbr_legacy compile with the old meanings and
// the old figures (the same numbers as before, so the same files come out until the flow is edited).
func TestLegacyBitrateMeaningsKeepTheirFiles(t *testing.T) {
	s := DefaultSelectors(3)
	s.Resolution, s.MaxBitrate, s.RateControl, s.VBRMaxKbps = "720", "medium", "vbr_legacy", 3000
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	p := s.Template().Compile(h264(1920, 1080, 8000))
	args := strings.Join(p.FFmpegArgs("in.mkv", "out.mkv"), " ")
	if p.Video.TargetKbps != 2500 || p.Video.MaxRateKbps != 3000 || !strings.Contains(args, "-b:v:0 2500k") || strings.Contains(args, "-crf 23") ||
		!strings.Contains(args, "-maxrate:v:0 3000k -bufsize:v:0 6000k") {
		t.Errorf("720p Medium vbr_legacy, 3 Mbps limit: %+v\n%s", p.Video, args)
	}
	// quality_legacy: the CRF, the old level as a cap
	q := s
	q.RateControl, q.VBRMaxKbps = "quality_legacy", 0
	if p := q.Template().Compile(h264(1920, 1080, 8000)); p.Video.TargetKbps != 0 || p.Video.MaxRateKbps != 2500 ||
		!slices.Contains(p.FFmpegArgs("in.mkv", "out.mkv"), "-crf") {
		t.Errorf("quality_legacy: %+v", p.Video)
	}
	// VP9's constant quality (-b:v 0) gives way to the target
	vp9 := VideoEncode{TargetKbps: 2500, Extra: VideoFormats["vp9"].Extra["normal"]}
	if got := strings.Join(extraArgs(vp9), " "); strings.Contains(got, "-b:v") || !strings.Contains(got, "-deadline good") {
		t.Errorf("vp9 extra: %s", got)
	}
}

// Review a772397: VP9 quality mode with a level is libvpx's constrained quality (-crf with -b:v as the
// cap, no -maxrate: libvpx refused that with -b:v 0); without a level it stays constant quality.
func TestVP9QualityWithALevel(t *testing.T) {
	SetAvailableFormats(map[string]bool{"libx265": true, "libvpx-vp9": true})
	defer SetAvailableFormats(nil)
	s := DefaultSelectors(3)
	s.Format, s.FileType, s.Audio, s.MaxBitrate, s.RateControl = "vp9", "webm", "stereo_opus", "medium", "quality_legacy"
	args := strings.Join(s.Template().Compile(h264(1280, 720, 8000)).FFmpegArgs("in.mkv", "out.webm"), " ")
	if !strings.Contains(args, "-crf") || !strings.Contains(args, "-b:v 2500k") || strings.Contains(args, "-maxrate") || strings.Contains(args, "-b:v 0") {
		t.Errorf("capped: %s", args)
	}
	s.MaxBitrate, s.RateControl = "none", ""
	if args := strings.Join(s.Template().Compile(h264(1280, 720, 8000)).FFmpegArgs("in.mkv", "out.webm"), " "); !strings.Contains(args, "-b:v 0") || strings.Contains(args, "-maxrate") {
		t.Errorf("no level: %s", args)
	}
}
