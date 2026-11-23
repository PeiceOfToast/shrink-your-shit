// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"slices"
	"strconv"
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
	if p.Video.TargetKbps != 2400 || p.Video.MaxRateKbps != 3600 || !strings.Contains(args, "-b:v:0 2400k") || strings.Contains(args, "-crf:v:0 23") ||
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
	if pv.Video.TargetKbps != 0 || pv.Video.MaxRateKbps != 2400 || pv.Video.BufsizeKbps != 600 || pv.Video.PeakLimitKbps != 3000 || !strings.Contains(vargs, "-crf") ||
		strings.Contains(vargs, "-b:v") || !strings.Contains(vargs, "-maxrate:v:0 2400k -bufsize:v:0 600k") {
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
	if p.Video.TargetKbps != 2500 || p.Video.MaxRateKbps != 3000 || !strings.Contains(args, "-b:v:0 2500k") || strings.Contains(args, "-crf:v:0 23") ||
		!strings.Contains(args, "-maxrate:v:0 3000k -bufsize:v:0 6000k") {
		t.Errorf("720p Medium vbr_legacy, 3 Mbps limit: %+v\n%s", p.Video, args)
	}
	// quality_legacy: the CRF, the old level as a cap
	q := s
	q.RateControl, q.VBRMaxKbps = "quality_legacy", 0
	if p := q.Template().Compile(h264(1920, 1080, 8000)); p.Video.TargetKbps != 0 || p.Video.MaxRateKbps != 2500 ||
		!slices.Contains(p.FFmpegArgs("in.mkv", "out.mkv"), "-crf:v:0") {
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

// A9 (decided 11-21): VP9 with a target bitrate is constrained quality, not a bare -b:v target, which overshoots
// (libvpx sat near 36.6 Mbit/s on the A9 clip). It takes the CRF the quality path would use for the same job, with
// the target as the cap, and no -maxrate.
func TestVP9TargetIsConstrainedQuality(t *testing.T) {
	SetAvailableFormats(map[string]bool{"libx265": true, "libvpx-vp9": true})
	defer SetAvailableFormats(nil)
	s := DefaultSelectors(3)
	s.Format, s.FileType, s.Audio, s.Resolution, s.MaxBitrate, s.RateControl = "vp9", "webm", "stereo_opus", "720", "medium", "level"
	p := s.Template().Compile(h264(1920, 1080, 8000))
	if p.Video.TargetKbps == 0 {
		t.Fatalf("no target: %+v", p.Video)
	}
	// the quality path's CRF for the same job: the same selectors with no level
	q := s
	q.MaxBitrate, q.RateControl = "none", ""
	crf := s.Template().Compile(h264(1920, 1080, 8000)).Video.CRF
	if qp := q.Template().Compile(h264(1920, 1080, 8000)); qp.Video.CRF != crf {
		t.Fatalf("quality CRF %d, target job CRF %d", qp.Video.CRF, crf)
	}
	args := strings.Join(p.FFmpegArgs("in.mkv", "out.webm"), " ")
	want := "-c:v:0 libvpx-vp9 -crf:v:0 " + strconv.Itoa(crf) + " -b:v:0 " + strconv.Itoa(p.Video.TargetKbps) + "k"
	if !strings.Contains(args, want) || strings.Contains(args, "-b:v 0") || strings.Contains(args, "-maxrate") {
		t.Errorf("want %q with no -maxrate or -b:v 0: %s", want, args)
	}
}

// A9 follow-up (#610 review2 P3): a software VP9 target job keeps its MaxRateKbps for the remux, but no -maxrate is
// passed, so the step sentence names the target only and makes no peak claim.
func TestVP9TargetSentenceMakesNoPeakClaim(t *testing.T) {
	SetAvailableFormats(map[string]bool{"libx265": true, "libvpx-vp9": true})
	defer SetAvailableFormats(nil)
	s := DefaultSelectors(3)
	s.Format, s.FileType, s.Audio, s.Resolution, s.MaxBitrate, s.RateControl = "vp9", "webm", "stereo_opus", "720", "medium", "level"
	info := h264(1920, 1080, 8000)
	p := s.Template().Compile(info)
	if p.Video.MaxRateKbps == 0 || !p.Video.IsVP9Target() {
		t.Fatalf("want a VP9 target with a remux peak: %+v", p.Video)
	}
	d := videoDecision(p, info).Decision
	if strings.Contains(d, "peaks") || !strings.Contains(d, "at about "+strconv.Itoa(p.Video.TargetKbps)+" kbps") {
		t.Errorf("step sentence: %q", d)
	}
}

// Review a772397 item 2: a saved AV1 VBR flow is skipped with the words that say what to change.
func TestSavedAV1VBRSkipsWithItsWords(t *testing.T) {
	SetAvailableFormats(map[string]bool{"libx265": true, "libsvtav1": true})
	defer SetAvailableFormats(nil)
	s := DefaultSelectors(3)
	s.Format, s.Resolution, s.RateControl, s.VBRMaxKbps = "av1", "720", "vbr", 3000
	p := s.Template().Compile(h264(1920, 1080, 8000))
	const want = "AV1 can't use VBR with a max. Edit the workflow's Convert step and pick Quality only."
	if p.Skip != want || p.SkipCode != SkipSettingCannotRun {
		t.Errorf("AV1 VBR skip: %q (%v), want %q", p.Skip, p.SkipCode, want)
	}
}

// The max that set a VBR file's bitrate: the limit when there is no average or the average was cut to it.
func TestPeakLimitBindsAndAverageIsMax(t *testing.T) {
	for _, c := range []struct {
		name            string
		v               VideoEncode
		binds, avgIsMax bool
	}{
		{"no limit", VideoEncode{TargetKbps: 3000, MaxRateKbps: 3000}, false, false},
		{"quality mode, limit is the peak", VideoEncode{MaxRateKbps: 2400, PeakLimitKbps: 3000}, true, false},
		{"average cut to the limit", VideoEncode{TargetKbps: 3000, MaxRateKbps: 3000, PeakLimitKbps: 3000}, true, true},
		{"average under the limit", VideoEncode{TargetKbps: 2000, MaxRateKbps: 2000, PeakLimitKbps: 3000}, false, false},
		{"average under limit, max at limit", VideoEncode{TargetKbps: 2000, MaxRateKbps: 3000, PeakLimitKbps: 3000}, false, false},
	} {
		if c.v.PeakLimitBinds() != c.binds || c.v.AverageIsMax() != c.avgIsMax {
			t.Errorf("%s: binds %v, average is max %v", c.name, c.v.PeakLimitBinds(), c.v.AverageIsMax())
		}
	}
}

// Queue row (VP9 VBR cap, s16): a VBR max must reach ffmpeg as a ceiling. VP9 carries it as -b:v beside -crf
// (constrained quality); H.264 carries it as -maxrate, with -bufsize a fifth of the max (see vbrBufsizeKbps).
func TestVBRMaxReachesFFmpegAsACeiling(t *testing.T) {
	SetAvailableFormats(map[string]bool{"libx264": true, "libvpx-vp9": true})
	defer SetAvailableFormats(nil)
	s := DefaultSelectors(1)
	s.Format, s.FileType, s.Audio, s.Resolution, s.Speed = "vp9", "webm", "stereo_opus", "720", "slower"
	s.RateControl, s.VBRMaxKbps = "vbr", 4000
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	vp9 := strings.Join(s.Template().Compile(h264(1280, 720, 8000)).FFmpegArgs("in.mkv", "out.webm"), " ")
	if !strings.Contains(vp9, "-crf:v:0 33 -b:v 3200k") || strings.Contains(vp9, "-maxrate") {
		t.Errorf("VP9 VBR max 4000: want -crf with -b:v 3200k as the cap, no -maxrate: %s", vp9)
	}
	s.Format, s.FileType, s.Speed = "h264", "mkv", "normal"
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	h := strings.Join(s.Template().Compile(h264(1280, 720, 8000)).FFmpegArgs("in.mkv", "out.mkv"), " ")
	if !strings.Contains(h, "-maxrate:v:0 3200k -bufsize:v:0 800k") || strings.Contains(h, " -b:v:0 ") {
		t.Errorf("H.264 VBR max 4000: want -maxrate 3200k and -bufsize 800k, no -b:v: %s", h)
	}
}
