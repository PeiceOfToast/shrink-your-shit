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
	for _, c := range []struct {
		change func(*Selectors)
		words  string
	}{
		{func(s *Selectors) { s.MaxBitrate = "none" }, "choose Low, Medium or High"},
		{func(s *Selectors) { s.VBRMaxKbps = 2000 }, "The limit is below Medium's 2.5 Mbps for 720p"},
		{func(s *Selectors) { s.VBRMaxKbps = 0 }, "from 0.1 to 500 Mbps"},
		{func(s *Selectors) { s.RateControl = "cbr" }, "rateControl"},
	} {
		bad := s
		c.change(&bad)
		if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), c.words) {
			t.Errorf("want %q: %v", c.words, err)
		}
	}
	keep := s
	keep.Resolution, keep.VBRMaxKbps = "keep", 1000 // any height: the limit wins where it is lower
	if err := keep.Validate(); err != nil {
		t.Errorf("keep: %v", err)
	}
}
