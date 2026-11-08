package worker

import (
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Review 32a307d (P3): the processing card's "converting to" names the format the plan makes, not
// always HEVC; the depth follows HDR or the source, as each encoder keeps the source's depth.
func TestConvertingToNamesTheFormat(t *testing.T) {
	src := func(depth int) media.Info {
		return media.Info{Container: "matroska,webm", Streams: []media.Stream{{Type: "video", Codec: "h264", BitDepth: depth}}}
	}
	for _, c := range []struct {
		video rules.VideoEncode
		depth int
		want  string
	}{
		{rules.VideoEncode{Codec: "libx265"}, 8, "HEVC · 8-bit · MKV"},
		{rules.VideoEncode{Codec: "libx264", Format: "h264"}, 8, "H.264 · 8-bit · MKV"},
		{rules.VideoEncode{Codec: "libsvtav1", Format: "av1"}, 10, "AV1 · 10-bit · MKV"},
		{rules.VideoEncode{Codec: "libvpx-vp9", Format: "vp9"}, 8, "VP9 · 8-bit · MKV"},
		{rules.VideoEncode{Codec: "libx265", Format: "hevc", HDR: &rules.HDR{}}, 8, "HEVC · 10-bit · MKV"},
		{rules.VideoEncode{Copy: true}, 8, "repackaged as MKV"},
	} {
		if got := convertingTo(rules.Plan{Video: c.video}, src(c.depth)); got != c.want {
			t.Errorf("%+v: %q, want %q", c.video, got, c.want)
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
		if got := convertingTo(rules.Plan{Container: c.container, Video: vp9}, c.src); got != c.want {
			t.Errorf("%q from %s%s: %q, want %q", c.container, c.src.Container, c.src.Ext, got, c.want)
		}
	}
}
