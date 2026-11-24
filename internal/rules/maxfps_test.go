// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"strings"
	"testing"
)

// filterOf is the value of the video filter chain in args ("" when there is none).
func filterOf(args []string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-filter:v:0" {
			return args[i+1]
		}
	}
	return ""
}

// The Convert step's frame-rate choice: keep (and absent) is today's plan, byte for byte.
func TestMaxFrameRateKeepIsTodaysPlan(t *testing.T) {
	src := probe("h264", 1920, 1080, 60, 12000)
	def := standard(func(*Selectors) {}).Compile(src)
	if strings.Contains(bitDepthPlanJSON(t, def), "maxFPS") {
		t.Fatalf("a plan that keeps the rate writes a maxFPS key")
	}
	for _, choice := range []string{"", "keep"} {
		p := standard(func(s *Selectors) { s.MaxFrameRate = choice }).Compile(src)
		if bitDepthPlanJSON(t, p) != bitDepthPlanJSON(t, def) || strings.Join(planArgs(p), " ") != strings.Join(planArgs(def), " ") {
			t.Errorf("frame rate %q changes the plan", choice)
		}
	}
}

// A video above the limit gets fps before the scale, so the scale works on the lower rate's frames.
func TestMaxFrameRateLowersBeforeTheScale(t *testing.T) {
	src := probe("h264", 1920, 1080, 60, 12000)
	p := standard(func(s *Selectors) { s.MaxFrameRate = "30" }).Compile(src)
	if p.Video.MaxFPS != 30 {
		t.Fatalf("a 60 fps source with the limit 30: MaxFPS %d", p.Video.MaxFPS)
	}
	if got := filterOf(planArgs(p)); got != "fps=30" {
		t.Errorf("processor filter: %q, want fps=30", got)
	}
	g := gpuPlan(22, func(v *VideoEncode) { v.MaxFPS, v.Height = 30, 720 })
	if got := filterOf(planArgs(g)); got != "fps=30,scale=-2:720" {
		t.Errorf("processor filter with a scale: %q, want fps=30,scale=-2:720", got)
	}
}

// At or under the limit nothing changes: no filter, no note.
func TestMaxFrameRateAtOrUnderTheLimitAddsNoFilter(t *testing.T) {
	for _, c := range []struct {
		rate float64
		cap  string
	}{{24, "30"}, {30, "30"}, {25, "25"}, {23.976, "24"}} {
		src := probe("h264", 1920, 1080, c.rate, 12000)
		p := standard(func(s *Selectors) { s.MaxFrameRate = c.cap }).Compile(src)
		if p.Video.MaxFPS != 0 || p.FrameRateNote != "" || strings.Contains(filterOf(planArgs(p)), "fps=") {
			t.Errorf("%v fps with the limit %s: MaxFPS %d, note %q, filter %q",
				c.rate, c.cap, p.Video.MaxFPS, p.FrameRateNote, filterOf(planArgs(p)))
		}
	}
}

// A source whose rate is unknown keeps its own rate, and the job is told why.
func TestMaxFrameRateUnknownRateKeepsItsOwn(t *testing.T) {
	src := probe("h264", 1920, 1080, 0, 12000)
	p := standard(func(s *Selectors) { s.MaxFrameRate = "30" }).Compile(src)
	if p.Video.MaxFPS != 0 || strings.Contains(filterOf(planArgs(p)), "fps=") {
		t.Errorf("unknown rate: MaxFPS %d, filter %q", p.Video.MaxFPS, filterOf(planArgs(p)))
	}
	if !strings.Contains(p.FrameRateNote, "not known") {
		t.Errorf("unknown rate: no job note: %q", p.FrameRateNote)
	}
}

// A card that decodes on the processor gets the limit in the processor chain before its upload; the full GPU
// pipeline is not used while the limit applies.
func TestMaxFrameRateCardPaths(t *testing.T) {
	p := gpuPlan(22, func(v *VideoEncode) { v.MaxFPS, v.Height = 30, 720 })
	for _, enc := range []string{"hevc_qsv", "hevc_vaapi", "hevc_nvenc"} {
		if p.HasFullPipeline(enc) {
			t.Errorf("%s: the full pipeline is used with a frame-rate limit", enc)
		}
		args, full := p.FFmpegArgsFullGPU("in.mkv", "out.mkv", enc)
		if full || !strings.HasPrefix(filterOf(args), "fps=30,scale=-2:720") {
			t.Errorf("%s: full %v, filter %q; want the processor chain starting fps=30,scale=-2:720", enc, full, filterOf(args))
		}
	}
}

// The packet estimate is the output's frame count: the source's count at the limit's rate, one frame either way.
func TestMaxFrameRatePacketEstimateIsOutputRate(t *testing.T) {
	src := probe("h264", 1920, 1080, 60, 12000) // 60 s
	const packets = 3600
	e := standard(func(s *Selectors) { s.MaxFrameRate = "30" }).Compile(src).Expect(src, packets)
	if e.VideoPackets != 1800 || e.OutputFPS != 30 {
		t.Fatalf("60 fps to 30: VideoPackets %d, OutputFPS %d; want 1800 and 30", e.VideoPackets, e.OutputFPS)
	}
	tol := e.PacketsTol * float64(e.VideoPackets)
	if d := 1801 - float64(e.VideoPackets); d > tol || -d > tol {
		t.Errorf("one frame over the estimate is refused: tolerance %.3f frames", tol)
	}
	if d := 1802 - float64(e.VideoPackets); d <= tol {
		t.Errorf("two frames over the estimate is accepted: tolerance %.3f frames", tol)
	}
	keep := standard(func(*Selectors) {}).Compile(src).Expect(src, packets)
	if keep.VideoPackets != packets || keep.OutputFPS != 0 {
		t.Errorf("keep: VideoPackets %d, OutputFPS %d; want %d and 0", keep.VideoPackets, keep.OutputFPS, packets)
	}
}

// Only 24, 25, 30, 60 and keep are valid; anything else is refused by Validate.
func TestMaxFrameRateValidate(t *testing.T) {
	for choice, ok := range map[string]bool{"": true, "keep": true, "24": true, "25": true, "30": true, "60": true,
		"50": false, "30fps": false, "Keep": false} {
		s := DefaultSelectors(3)
		s.MaxFrameRate = choice
		if err := s.Validate(); (err == nil) != ok {
			t.Errorf("frame rate %q: Validate = %v, want ok %v", choice, err, ok)
		}
	}
}

// P3 (#689 review): an HDR source is 10-bit whatever the 8-bit choice, through the compiler.
func TestBitDepthEightOnHDRProbeStaysTenBit(t *testing.T) {
	src := bitDepthSource(10)
	src.Streams[0].ColorTransfer, src.Streams[0].ColorPrimaries, src.Streams[0].ColorSpace = "smpte2084", "bt2020", "bt2020nc"
	p := standard(func(s *Selectors) { s.BitDepth = "8" }).Compile(src)
	if p.Video.HDR == nil || p.Video.BitDepth != 0 || !p.Video.TenBit {
		t.Fatalf("8 on an HDR probe: HDR %v, BitDepth %d, TenBit %v", p.Video.HDR, p.Video.BitDepth, p.Video.TenBit)
	}
	if OutputDepth(p, &src, "") != 10 || OutputDepth(p, &src, "hevc_qsv") != 10 {
		t.Errorf("8 on an HDR probe: processor depth %d, card depth %d, want 10",
			OutputDepth(p, &src, ""), OutputDepth(p, &src, "hevc_qsv"))
	}
}

// P3 (#689 review): VAAPI with no scale names the card's frames in its format-only filter.
func TestBitDepthVAAPINoScaleFullPipeline(t *testing.T) {
	eight := gpuPlan(22, func(v *VideoEncode) { v.BitDepth = 8 })
	if args, ok := eight.FFmpegArgsFullGPU("in.mkv", "out.mkv", "hevc_vaapi"); !ok || !has(args, "-filter:v:0", "scale_vaapi=format=nv12") {
		t.Errorf("8 on a 10-bit SDR source, VAAPI full pipeline, no scale: want scale_vaapi=format=nv12: ok %v, %v", ok, args)
	}
	ten := gpuPlan(22, func(v *VideoEncode) { v.BitDepth = 10; v.TenBit = true })
	if args, ok := ten.FFmpegArgsFullGPU("in.mkv", "out.mkv", "hevc_vaapi"); !ok || !has(args, "-filter:v:0", "scale_vaapi=format=p010") {
		t.Errorf("10 on an 8-bit source, VAAPI full pipeline, no scale: want scale_vaapi=format=p010: ok %v, %v", ok, args)
	}
}
