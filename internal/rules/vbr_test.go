// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"slices"
	"strings"
	"testing"
)

// User review 11-12 item 1: VBR aims at the level's bitrate for the new file's height (-b:v), never
// above the person's limit (-maxrate, -bufsize 2x), and neither above the source's bitrate.
func TestVBR(t *testing.T) {
	s := DefaultSelectors(3)
	s.Resolution, s.MaxBitrate, s.RateControl, s.VBRMaxKbps = "720", "medium", "vbr", 3000
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	p := s.Template().Compile(h264(1920, 1080, 8000))
	args := strings.Join(p.FFmpegArgs("in.mkv", "out.mkv"), " ")
	if p.Video.TargetKbps != 2500 || p.Video.MaxRateKbps != 3000 || !strings.Contains(args, "-b:v:0 2500k") || strings.Contains(args, "-crf") ||
		!strings.Contains(args, "-maxrate:v:0 3000k -bufsize:v:0 6000k") {
		t.Errorf("720p Medium VBR, 3 Mbps limit: %+v\n%s", p.Video, args)
	}
	// a source with less than the limit and the level: neither is more than it has now
	if p := s.Template().Compile(h264(1920, 1080, 2000)); p.Video.TargetKbps != 2000 || p.Video.MaxRateKbps != 2000 {
		t.Errorf("a 2 Mbps source: %+v", p.Video)
	}
	// quality mode is today's: the CRF, the level as a cap
	q := s
	q.RateControl, q.VBRMaxKbps = "", 0
	if p := q.Template().Compile(h264(1920, 1080, 8000)); p.Video.TargetKbps != 0 || p.Video.MaxRateKbps != 2500 ||
		!slices.Contains(p.FFmpegArgs("in.mkv", "out.mkv"), "-crf") {
		t.Errorf("quality: %+v", p.Video)
	}
	// VP9's constant quality (-b:v 0) gives way to the target
	vp9 := VideoEncode{TargetKbps: 2500, Extra: VideoFormats["vp9"].Extra["normal"]}
	if got := strings.Join(extraArgs(vp9), " "); strings.Contains(got, "-b:v") || !strings.Contains(got, "-deadline good") {
		t.Errorf("vp9 extra: %s", got)
	}
	if got := extraArgs(VideoEncode{Extra: VideoFormats["vp9"].Extra["normal"]}); !slices.Contains(got, "-b:v") {
		t.Errorf("quality keeps it: %v", got)
	}
	SetAvailableFormats(map[string]bool{"libx265": true, "libx264": true, "libsvtav1": true, "libvpx-vp9": true})
	defer SetAvailableFormats(nil)
	for _, c := range []struct {
		change func(*Selectors)
		words  string
	}{
		{func(s *Selectors) { s.MaxBitrate = "none" }, "choose Low, Medium or High"},
		{func(s *Selectors) { s.VBRMaxKbps = 2000 }, "Must be at least 2.5 Mbps, the Medium figure for 720p."},
		{func(s *Selectors) { s.Resolution, s.VBRMaxKbps = "keep", 1000 }, "Must be at least 1.2 Mbps, the Medium figure for 480p and below."},
		{func(s *Selectors) { s.VBRMaxKbps = 0 }, "from 0.1 to 500 Mbps"},
		{func(s *Selectors) { s.RateControl = "cbr" }, "rateControl"},
		{func(s *Selectors) { s.Format = "av1" }, "VBR with a peak limit isn't available for AV1; use quality mode."},
	} {
		bad := s
		c.change(&bad)
		if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), c.words) {
			t.Errorf("want %q: %v", c.words, err)
		}
	}
	// Same resolution: checked against the lowest row the level reaches (never refused for a file it
	// could fit); a file whose own row needs more is encoded at the limit, never refused at run time
	keep := s
	keep.Resolution, keep.VBRMaxKbps = "keep", 1300
	if err := keep.Validate(); err != nil {
		t.Errorf("keep: %v", err)
	}
	if p := keep.Template().Compile(h264(1920, 1080, 8000)); p.Video.TargetKbps != 1300 || p.Video.MaxRateKbps != 1300 || p.Video.PeakLimitKbps != 1300 {
		t.Errorf("a 1080p file under a 1.3 Mbps limit: %+v", p.Video)
	}
	if Mbps(3000) != "3" || Mbps(2500) != "2.5" {
		t.Errorf("Mbps %s %s", Mbps(3000), Mbps(2500))
	}
}

// Review a772397: VP9 quality mode with a level is libvpx's constrained quality (-crf with -b:v as the
// cap, no -maxrate: libvpx refused that with -b:v 0); without a level it stays constant quality.
func TestVP9QualityWithALevel(t *testing.T) {
	SetAvailableFormats(map[string]bool{"libx265": true, "libvpx-vp9": true})
	defer SetAvailableFormats(nil)
	s := DefaultSelectors(3)
	s.Format, s.FileType, s.Audio, s.MaxBitrate = "vp9", "webm", "stereo_opus", "medium"
	args := strings.Join(s.Template().Compile(h264(1280, 720, 8000)).FFmpegArgs("in.mkv", "out.webm"), " ")
	if !strings.Contains(args, "-crf") || !strings.Contains(args, "-b:v 2500k") || strings.Contains(args, "-maxrate") || strings.Contains(args, "-b:v 0") {
		t.Errorf("capped: %s", args)
	}
	s.MaxBitrate = "none"
	if args := strings.Join(s.Template().Compile(h264(1280, 720, 8000)).FFmpegArgs("in.mkv", "out.webm"), " "); !strings.Contains(args, "-b:v 0") || strings.Contains(args, "-maxrate") {
		t.Errorf("no level: %s", args)
	}
}
