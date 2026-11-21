// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// mainVideoSource is a source with one video the table's plan encodes and one audio track it keeps.
func mainVideoSource(w, h, depth int) media.Info {
	return media.Info{Container: "matroska,webm", DurationSec: 100, Streams: []media.Stream{
		{Index: 0, Type: "video", Codec: "h264", Width: w, Height: h, BitDepth: depth, FrameRate: 24},
		{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Language: "eng"},
	}}
}

// mainVideoOutput is src as the encoder made it: its main video in codec at w x h and depth.
func mainVideoOutput(src media.Info, codec string, w, h, depth int) media.Info {
	out := src
	out.Streams = append([]media.Stream(nil), src.Streams...)
	out.Streams[0].Codec, out.Streams[0].Width, out.Streams[0].Height, out.Streams[0].BitDepth = codec, w, h, depth
	return out
}

// The check's problems, through the Expect the worker records (the rules filled in from the plan and the
// encoder that ran, as the worker fills them).
func mainVideoProblems(p Plan, src media.Info, gpu string, out media.Info) []string {
	e := p.Expect(src, 2500)
	e.MainVideo = p.MainVideoRules(src, gpu)
	return e.Compare(out, 2500)
}

// The proof contract of foundation slice 3: the main video is judged by the plan's format, its height, a
// width within ±2 px of the predicted one (and even), and the depth of the encoder that ran. Each row
// says whether the new picture passes and, when it fails, the words the refusal must carry.
func TestMainVideoIsJudgedByItsRules(t *testing.T) {
	const card = "hevc_qsv"
	scale := Plan{Video: VideoEncode{Codec: "libx265", Index: 0, Height: 480}} // 1920x1080 to 480 lines: 854 wide
	hevc10 := Plan{Video: VideoEncode{Codec: "libx265", TenBit: true}}
	h264Ten := Plan{Video: VideoEncode{Codec: "libx264", Format: "h264", TenBit: true}}
	hevc8 := Plan{Video: VideoEncode{Codec: "libx265"}}
	tests := []struct {
		name  string
		plan  Plan
		src   media.Info
		gpu   string
		out   media.Info
		pass  bool
		words []string
	}{
		// job 48: 1920x1080 to 480 lines on QSV. Both roundings of 853⅓ pass.
		{name: "job 48: 852x480 passes", src: mainVideoSource(1920, 1080, 8), plan: scale, gpu: card,
			out: mainVideoOutput(mainVideoSource(1920, 1080, 8), "hevc", 852, 480, 8), pass: true},
		{name: "job 48: 854x480 passes", src: mainVideoSource(1920, 1080, 8), plan: scale, gpu: card,
			out: mainVideoOutput(mainVideoSource(1920, 1080, 8), "hevc", 854, 480, 8), pass: true},
		{name: "640x480 fails when 854x480 was planned", src: mainVideoSource(1920, 1080, 8), plan: scale, gpu: card,
			out:   mainVideoOutput(mainVideoSource(1920, 1080, 8), "hevc", 640, 480, 8),
			words: []string{"the new picture is 640x480; the plan makes it 480 lines high with the original's shape (854x480, ±2)"}},
		{name: "855x480 is odd and fails", src: mainVideoSource(1920, 1080, 8), plan: scale, gpu: card,
			out:   mainVideoOutput(mainVideoSource(1920, 1080, 8), "hevc", 855, 480, 8),
			words: []string{"855x480", "an even width"}},
		{name: "854x482 has the wrong height and fails", src: mainVideoSource(1920, 1080, 8), plan: scale, gpu: card,
			out:   mainVideoOutput(mainVideoSource(1920, 1080, 8), "hevc", 854, 482, 8),
			words: []string{"the new picture is 854x482", "480 lines high"}},
		{name: "h264 for an HEVC plan fails", src: mainVideoSource(1920, 1080, 8), plan: hevc8, gpu: "",
			out:   mainVideoOutput(mainVideoSource(1920, 1080, 8), "h264", 1920, 1080, 8),
			words: []string{"the new picture is in h264; the plan makes hevc"}},

		// #498 and #507: 10-bit SDR source, HEVC on a card. The card makes main10; an 8-bit file is wrong.
		{name: "10-bit SDR on a card: 10-bit passes", src: mainVideoSource(1920, 1080, 10), plan: hevc10, gpu: card,
			out: mainVideoOutput(mainVideoSource(1920, 1080, 10), "hevc", 1920, 1080, 10), pass: true},
		{name: "10-bit SDR on a card: 8-bit fails", src: mainVideoSource(1920, 1080, 10), plan: hevc10, gpu: card,
			out:   mainVideoOutput(mainVideoSource(1920, 1080, 10), "hevc", 1920, 1080, 8),
			words: []string{"the new file is 8-bit; the encoder that ran (hevc_qsv) makes 10-bit for this file"}},

		// The same 10-bit source to H.264: the processor keeps its depth, the card makes 8 bits.
		{name: "10-bit source on the processor to H.264: 10-bit expected", src: mainVideoSource(1920, 1080, 10), plan: h264Ten, gpu: "",
			out: mainVideoOutput(mainVideoSource(1920, 1080, 10), "h264", 1920, 1080, 10), pass: true},
		{name: "10-bit source on the processor to H.264: 8-bit fails", src: mainVideoSource(1920, 1080, 10), plan: h264Ten, gpu: "",
			out:   mainVideoOutput(mainVideoSource(1920, 1080, 10), "h264", 1920, 1080, 8),
			words: []string{"the encoder that ran (the processor) makes 10-bit for this file"}},
		{name: "10-bit source to H.264 on a card: 8-bit is right", src: mainVideoSource(1920, 1080, 10), plan: h264Ten, gpu: "h264_qsv",
			out: mainVideoOutput(mainVideoSource(1920, 1080, 10), "h264", 1920, 1080, 8), pass: true},

		// An 8-bit source stays 8-bit whoever encodes it.
		{name: "8-bit source: 8-bit passes", src: mainVideoSource(1920, 1080, 8), plan: hevc8, gpu: "",
			out: mainVideoOutput(mainVideoSource(1920, 1080, 8), "hevc", 1920, 1080, 8), pass: true},
		{name: "8-bit source: 10-bit fails", src: mainVideoSource(1920, 1080, 8), plan: hevc8, gpu: card,
			out:   mainVideoOutput(mainVideoSource(1920, 1080, 8), "hevc", 1920, 1080, 10),
			words: []string{"the encoder that ran (hevc_qsv) makes 8-bit"}},

		// Unscaled: the source's own size, exactly (a 1088 picture is another picture).
		{name: "unscaled 1920x1080 passes", src: mainVideoSource(1920, 1080, 8), plan: hevc8, gpu: "",
			out: mainVideoOutput(mainVideoSource(1920, 1080, 8), "hevc", 1920, 1080, 8), pass: true},
		{name: "unscaled 1920x1080 made 1920x1088 fails", src: mainVideoSource(1920, 1080, 8), plan: hevc8, gpu: "",
			out:   mainVideoOutput(mainVideoSource(1920, 1080, 8), "hevc", 1920, 1088, 8),
			words: []string{"the new picture is 1920x1088; the plan makes it 1080 lines high"}},

		// A rotated phone video: ffmpeg encodes the upright picture, so 1920x1080 coded ±90° is 1080x1920.
		{name: "rotated phone video: the swapped size passes", src: rotatedSource(), plan: hevc8, gpu: "",
			out: mainVideoOutput(rotatedSource(), "hevc", 1080, 1920, 8), pass: true},
		{name: "rotated phone video: the coded size fails", src: rotatedSource(), plan: hevc8, gpu: "",
			out:   mainVideoOutput(rotatedSource(), "hevc", 1920, 1080, 8),
			words: []string{"the new picture is 1920x1080; the plan makes it 1920 lines high with the original's shape (1080x1920, ±2)"}},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			problems := mainVideoProblems(c.plan, c.src, c.gpu, c.out)
			if c.pass != (len(problems) == 0) {
				t.Fatalf("pass %v, problems %q", c.pass, problems)
			}
			for _, w := range c.words {
				if !strings.Contains(strings.Join(problems, "; "), w) {
					t.Errorf("no %q in %q", w, problems)
				}
			}
		})
	}
}

func rotatedSource() media.Info {
	s := mainVideoSource(1920, 1080, 8)
	s.Streams[0].Rotation = 90
	return s
}

// A copied video keeps the exact comparison: no main-video rules, and a picture of another size is
// refused against the original's.
func TestCopiedVideoKeepsTheExactCheck(t *testing.T) {
	src := mainVideoSource(1920, 1080, 8)
	p := Plan{Video: VideoEncode{Codec: "libx265", Copy: true}}
	if m := p.MainVideoRules(src, "hevc_qsv"); m != nil {
		t.Fatalf("a copied video got rules: %+v", m)
	}
	if problems := mainVideoProblems(p, src, "", mainVideoOutput(src, "h264", 1920, 1080, 8)); len(problems) != 0 {
		t.Errorf("the copy was refused: %q", problems)
	}
	problems := mainVideoProblems(p, src, "", mainVideoOutput(src, "h264", 1920, 1088, 8))
	if len(problems) != 1 || !strings.Contains(problems[0], "the picture size is 1920x1080 in the original but 1920x1088 in the new file") {
		t.Errorf("the exact check is gone: %q", problems)
	}
}

// The encoder a reported ffmpeg command ran for the main video: the card's name, or "" for the
// processor (its software encoder is not a card, and a copy has none).
func TestGraphicsEncoderIn(t *testing.T) {
	for _, c := range []struct {
		args  []string
		index int
		want  string
	}{
		{[]string{"-c:v:0", "hevc_qsv", "-i", "in"}, 0, "hevc_qsv"},
		{[]string{"-c:v:1", "h264_nvenc"}, 1, "h264_nvenc"},
		{[]string{"-c:v:0", "libx265"}, 0, ""},
		{[]string{"-c:v:0", "libx264"}, 0, ""},
		{[]string{"-c", "copy"}, 0, ""},
		{[]string{"-c:v:0", "hevc_qsv", "-c:v:1", "h264_qsv"}, 1, "h264_qsv"},
	} {
		if got := GraphicsEncoderIn(c.args, c.index); got != c.want {
			t.Errorf("%v [%d]: %q, want %q", c.args, c.index, got, c.want)
		}
	}
}

// OutputDepth: a card makes 10 bits only when its arguments ask for them; the processor makes the job's
// depth on every target (NeedsTenBit: the plan's 10 bits, or a 10-bit source the plan left unset).
func TestOutputDepth(t *testing.T) {
	src10 := mainVideoSource(1920, 1080, 10)
	for _, c := range []struct {
		plan Plan
		src  *media.Info
		gpu  string
		want int
	}{
		{plan: Plan{Video: VideoEncode{Codec: "libx265", TenBit: true}}, gpu: "hevc_qsv", want: 10},
		{plan: Plan{Video: VideoEncode{Codec: "libx264", TenBit: true}}, gpu: "h264_qsv", want: 8},
		{plan: Plan{Video: VideoEncode{Codec: "libx264", TenBit: true}}, want: 10},
		{plan: Plan{Video: VideoEncode{Codec: "libx265"}}, gpu: "hevc_qsv", want: 8},
		{plan: Plan{Video: VideoEncode{Codec: "libx265"}}, want: 8},
		{plan: Plan{Video: VideoEncode{Codec: "libx265", HDR: &HDR{Transfer: "smpte2084", Primaries: "bt2020", Matrix: "bt2020nc"}}}, gpu: "hevc_vaapi", want: 10},
		// The fold onto slice 6's rule: a 10-bit source the plan did not mark keeps its depth on the
		// processor, and the card's arguments still decide on a card.
		{plan: Plan{Video: VideoEncode{Codec: "libx265"}}, src: &src10, want: 10},
		{plan: Plan{Video: VideoEncode{Codec: "libx265"}}, src: &src10, gpu: "hevc_qsv", want: 8},
	} {
		if got := OutputDepth(c.plan, c.src, c.gpu); got != c.want {
			t.Errorf("%s on %q: %d, want %d", c.plan.Video.Codec, c.gpu, got, c.want)
		}
	}
}
