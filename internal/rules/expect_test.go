// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

func probeFile(t *testing.T, rel string) media.Info {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "media", "testdata", rel))
	if err != nil {
		t.Fatal(err)
	}
	info, err := media.ParseProbe(data)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestDefaultExpectFromARealProbe(t *testing.T) {
	e := DefaultExpect(probeFile(t, "real/subs.json"), 150)
	if len(e.Streams) != 4 {
		t.Fatalf("%+v", e.Streams)
	}
	v := e.Streams[0]
	if v.Type != "video" || v.Codec != "hevc" || v.Width != 640 || v.Height != 360 || v.BitDepth != 8 {
		t.Errorf("the first video stream becomes HEVC at the same size: %+v", v)
	}
	if a := e.Streams[1]; a.Type != "audio" || a.Codec != "aac" || a.Channels != 1 {
		t.Errorf("audio is copied unchanged: %+v", a)
	}
	if s := e.Streams[3]; s.Type != "subtitle" || s.Codec != "ass" || s.Language != "fre" || !s.Forced || s.SubKind != "text" {
		t.Errorf("subtitles are copied unchanged: %+v", s)
	}
	if e.VideoPackets != 150 || e.DurationSec <= 0 || e.DurationTol != 0.02 {
		t.Errorf("%+v", e)
	}
}

func TestDefaultExpectDropsDataStreamsOnly(t *testing.T) {
	e := DefaultExpect(probeFile(t, "crafted/odd_missing_fields.json"), 0)
	for _, s := range e.Streams {
		if s.Type == "data" {
			t.Error("data streams are dropped on purpose and must not be expected")
		}
	}
	if len(e.Streams) != 5 { // video, audio, subtitle, attachment, "somethingnew"
		t.Errorf("%d streams: %+v", len(e.Streams), e.Streams)
	}
}

func TestDefaultExpectKeepsTenBit(t *testing.T) {
	e := DefaultExpect(probeFile(t, "real/tenbit.json"), 0)
	if e.Streams[0].BitDepth != 10 {
		t.Errorf("a 10-bit source must come out 10-bit: %+v", e.Streams[0])
	}
}

// A file always matches its own default expectation, once its first video stream is HEVC.
func TestCompareAcceptsAFaithfulConversion(t *testing.T) {
	src := probeFile(t, "real/subs.json")
	out := src
	out.Streams = append([]media.Stream(nil), src.Streams...)
	out.Streams[0].Codec = "hevc"
	if p := DefaultExpect(src, 100).Compare(out, 100); len(p) != 0 {
		t.Errorf("a faithful conversion must produce no problems, got %v", p)
	}
	// frame counts within 1% are fine, beyond are not
	if p := DefaultExpect(src, 1000).Compare(out, 995); len(p) != 0 {
		t.Errorf("0.5%% fewer frames is within tolerance: %v", p)
	}
	if p := DefaultExpect(src, 1000).Compare(out, 900); len(p) != 1 || !strings.Contains(p[0], "video frames") {
		t.Errorf("10%% fewer frames must be reported: %v", p)
	}
	if p := DefaultExpect(src, 0).Compare(out, 5); len(p) != 0 {
		t.Errorf("an unknown source frame count is not checked: %v", p)
	}
}

func TestCompareExplainsEverythingInPlainWords(t *testing.T) {
	src := probeFile(t, "crafted/bluray_remux_bitmap_subs.json")
	out := src
	out.Streams = []media.Stream{src.Streams[0], src.Streams[2]} // lost truehd, a subtitle...
	out.Streams[0].Codec = "hevc"
	p := strings.Join(DefaultExpect(src, 0).Compare(out, -1), " | ")
	for _, want := range []string{
		"the original has 2 audio streams but the new file has 1 audio stream",
		"missing: audio #2 (eng, 6 channels)",
		"the original has 3 subtitle streams but the new file has 0 subtitle streams",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("message %q should contain %q", p, want)
		}
	}
	if strings.Contains(p, "%!") {
		t.Errorf("formatting slip: %q", p)
	}
}

func TestHumanBytesAndClock(t *testing.T) {
	for in, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 1536: "1.5 KB", 5 << 20: "5.0 MB", 3 << 30: "3.0 GB"} {
		if got := HumanBytes(in); got != want {
			t.Errorf("HumanBytes(%d)=%q want %q", in, got, want)
		}
	}
	if Clock(0) != "0:00:00" || Clock(3725) != "1:02:05" || Clock(59.6) != "0:01:00" {
		t.Errorf("%s %s %s", Clock(0), Clock(3725), Clock(59.6))
	}
}

// "und" (undetermined, as MP4 writes it) and no language are the same; a real language is not.
func TestSameLanguage(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"und", "", true}, {"", "UND", true}, {"eng", "ENG", true}, {"eng", "", false}, {"eng", "und", false}, {"fre", "eng", false}} {
		if sameLanguage(c.a, c.b) != c.want {
			t.Errorf("%q %q", c.a, c.b)
		}
	}
}
