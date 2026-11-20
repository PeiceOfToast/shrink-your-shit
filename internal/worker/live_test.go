package worker

import (
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The Dashboard's saving estimate: a running encode's projected output size is the bytes written so far
// and how far in the encode is, averaged over its last samples so it doesn't jump; nothing before 3%,
// nothing while the new file is under 16 MiB (the muxer's lag would inflate the saving, QA #459), and
// nothing when ffmpeg doesn't say the size (a remote worker's report has none).
func TestLiveProjectsTheNewFilesSize(t *testing.T) {
	const id = int64(12345)
	SetLiveForTest(id, LiveJob{})
	defer DropLiveForTest(id)
	projected := func() int64 { j, _ := Live(id); return j.ProjectedSize }
	updateLive(id, media.Progress{Percent: 1, TotalSize: 1 << 20})
	if p := projected(); p != 0 {
		t.Errorf("1%% in: projected %d, want none", p)
	}
	updateLive(id, media.Progress{Percent: 20, TotalSize: 10 << 20}) // under 16 MiB: the muxer lags
	if p := projected(); p != 0 {
		t.Errorf("a small new file: projected %d, want none", p)
	}
	updateLive(id, media.Progress{Percent: 20, TotalSize: 20 << 20}) // 20 MiB at 20%: 100 MiB
	if p := projected(); p != 100<<20 {
		t.Errorf("20%%: projected %d, want %d", p, int64(100<<20))
	}
	updateLive(id, media.Progress{Percent: 25, TotalSize: 30 << 20}) // 30 MiB at 25%: 120 MiB; the average of the two is 110
	if p := projected(); p != 110<<20 {
		t.Errorf("25%%: projected %d, want %d (smoothed)", p, int64(110<<20))
	}
	updateLive(id, media.Progress{Percent: 30, TotalSize: 0}) // total_size=N/A: the last answer stands
	if p := projected(); p != 110<<20 {
		t.Errorf("no size: projected %d, want the last answer", p)
	}
	updateLive(id, media.Progress{Percent: 0, TotalSize: 0})
	if j, _ := Live(id); j.ProjectedSize != 110<<20 || j.Progress.Percent != 0 {
		t.Errorf("a report with no percent leaves the estimate: %+v", j)
	}
}

// Review 32a307d (P3): the processing card's "converting to" names the format the plan makes, not
// always HEVC; the depth is the one the encoder that runs makes (review2 507): the processor keeps the
// source's depth on every target, a card only where it has a 10-bit one.
func TestConvertingToNamesTheFormat(t *testing.T) {
	src := func(depth int) media.Info {
		return media.Info{Container: "matroska,webm", Streams: []media.Stream{{Type: "video", Codec: "h264", BitDepth: depth}}}
	}
	for _, c := range []struct {
		video rules.VideoEncode
		depth int
		gpu   string // the graphics encoder that runs; "" is the processor
		want  string
	}{
		{rules.VideoEncode{Codec: "libx265"}, 8, "", "HEVC · 8-bit · MKV"},
		{rules.VideoEncode{Codec: "libx264", Format: "h264"}, 8, "", "H.264 · 8-bit · MKV"},
		{rules.VideoEncode{Codec: "libsvtav1", Format: "av1", TenBit: true}, 10, "", "AV1 · 10-bit · MKV"},
		{rules.VideoEncode{Codec: "libx264", Format: "h264", TenBit: true}, 10, "", "H.264 · 10-bit · MKV"}, // the processor keeps 10 bits
		{rules.VideoEncode{Codec: "libx265", Format: "hevc", TenBit: true}, 10, "hevc_nvenc", "HEVC · 10-bit · MKV"},
		{rules.VideoEncode{Codec: "libx264", Format: "h264", TenBit: true}, 10, "h264_nvenc", "H.264 · 8-bit · MKV"}, // no 10-bit H.264 on the card
		{rules.VideoEncode{Codec: "libvpx-vp9", Format: "vp9"}, 8, "", "VP9 · 8-bit · MKV"},
		{rules.VideoEncode{Codec: "libx265", Format: "hevc", HDR: &rules.HDR{}}, 8, "", "HEVC · 10-bit · MKV"},
		{rules.VideoEncode{Copy: true}, 8, "", "repackaged as MKV"},
	} {
		if got := convertingTo(rules.Plan{Video: c.video}, src(c.depth), c.gpu); got != c.want {
			t.Errorf("%+v on %q: %q, want %q", c.video, c.gpu, got, c.want)
		}
	}
}

// S5 proposal, from #99's check: the encoder line names the encoder in words, not ffmpeg's name.
func TestEncoderWords(t *testing.T) {
	for _, c := range []struct {
		video rules.VideoEncode
		want  string
	}{
		{rules.VideoEncode{Codec: "libx265"}, "x265 (processor)"},
		{rules.VideoEncode{Codec: "libx264"}, "x264 (processor)"},
		{rules.VideoEncode{Codec: "libsvtav1"}, "SVT-AV1 (processor)"},
		{rules.VideoEncode{Codec: "libvpx-vp9"}, "VP9 (processor)"},
		{rules.VideoEncode{Copy: true}, "copied, not encoded"},
	} {
		if got := encoderWords(rules.Plan{Video: c.video}); got != c.want {
			t.Errorf("%s: %q, want %q", c.video.Codec, got, c.want)
		}
	}
}

// #97 QA (P3): the processing card's "converting to" ends in the new file's type: WebM and MP4 too,
// including a .webm kept as its own type (ffprobe names it matroska).
func TestConvertingToNamesTheFileType(t *testing.T) {
	vp9 := rules.VideoEncode{Codec: "libvpx-vp9", Format: "vp9"}
	mkv := media.Info{Container: "matroska,webm", Streams: []media.Stream{{Type: "video", Codec: "h264", BitDepth: 8}}}
	webm, mp4 := mkv, mkv
	webm.Ext = ".webm"
	mp4.Container, mp4.Ext = "mov,mp4,m4a,3gp,3g2,mj2", ".mp4"
	for _, c := range []struct {
		container string
		src       media.Info
		want      string
	}{
		{"webm", mkv, "VP9 · 8-bit · WebM"},
		{"mp4", mkv, "VP9 · 8-bit · MP4"},
		{"mkv", webm, "VP9 · 8-bit · MKV"},
		{"", webm, "VP9 · 8-bit · WebM"},
		{"", mp4, "VP9 · 8-bit · MP4"},
		{"", mkv, "VP9 · 8-bit · MKV"},
	} {
		if got := convertingTo(rules.Plan{Container: c.container, Video: vp9}, c.src, ""); got != c.want {
			t.Errorf("%q from %s%s: %q, want %q", c.container, c.src.Container, c.src.Ext, got, c.want)
		}
	}
}
