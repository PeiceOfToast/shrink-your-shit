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
