// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) Info {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	info, err := ParseProbe(data)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return info
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// ---- real ffprobe output for files made with ffmpeg (testdata/real) ----

func TestRealPlainMP4(t *testing.T) {
	i := fixture(t, "real/plain.json")
	v := i.Video()
	if v == nil || v.Codec != "h264" || v.Width != 640 || v.Height != 360 || v.PixFmt != "yuv420p" ||
		v.BitDepth != 8 || v.FieldOrder != "progressive" || !near(v.FrameRate, 30, 0.01) || v.VFR {
		t.Errorf("video %+v", v)
	}
	if i.VideoCodec != "h264" || i.IsHDR() || i.HasBitmapSubs() || len(i.Audio()) != 0 || i.Chapters != 0 {
		t.Errorf("%+v", i)
	}
}

func TestRealMultiAudio(t *testing.T) {
	i := fixture(t, "real/multi_audio.json")
	a := i.Audio()
	if len(a) != 2 {
		t.Fatalf("audio streams: %d", len(a))
	}
	if a[0].Language != "eng" || a[0].Title != "Main" || a[0].Channels != 1 || a[0].ChannelLayout != "mono" || a[0].SampleRate != 44100 || a[0].Codec != "aac" {
		t.Errorf("first audio %+v", a[0])
	}
	if a[1].Language != "jpn" || a[1].Title != "" || a[1].Index != 2 {
		t.Errorf("second audio %+v", a[1])
	}
}

func TestRealSubtitlesTextAndForced(t *testing.T) {
	i := fixture(t, "real/subs.json")
	subs := i.Subtitles()
	if len(subs) != 2 {
		t.Fatalf("subtitle streams: %d", len(subs))
	}
	if subs[0].Codec != "subrip" || subs[0].SubKind != "text" || subs[0].Language != "eng" || subs[0].Forced {
		t.Errorf("srt %+v", subs[0])
	}
	if subs[1].Codec != "ass" || subs[1].SubKind != "text" || subs[1].Language != "fre" || !subs[1].Forced {
		t.Errorf("ass %+v", subs[1])
	}
	if i.HasBitmapSubs() {
		t.Error("text subtitles are not bitmap")
	}
}

func TestRealAttachment(t *testing.T) {
	i := fixture(t, "real/attach.json")
	at := i.Attachments()
	if len(at) != 1 || at[0].Filename != "f.ttf" || at[0].MimeType != "application/x-truetype-font" || at[0].Codec != "ttf" {
		t.Errorf("%+v", at)
	}
	for _, s := range i.Streams { // no language tags at all in this file
		if s.Language != "" {
			t.Errorf("stream %d invented a language %q", s.Index, s.Language)
		}
	}
}

func TestRealCoverArtIsNotTheVideo(t *testing.T) {
	i := fixture(t, "real/cover.json")
	if len(i.ByType("video")) != 2 {
		t.Fatalf("want main video + cover: %+v", i.Streams)
	}
	cover := i.Streams[2]
	if !cover.AttachedPic || cover.Codec != "mjpeg" || cover.VFR {
		t.Errorf("cover %+v", cover)
	}
	if v := i.Video(); v == nil || v.Codec != "h264" || v.AttachedPic {
		t.Errorf("Video() must skip cover art: %+v", v)
	}
}

func TestRealChapters(t *testing.T) {
	if n := fixture(t, "real/chapters.json").Chapters; n != 2 {
		t.Errorf("chapters %d, want 2", n)
	}
}

func TestRealTenBit(t *testing.T) {
	v := fixture(t, "real/tenbit.json").Video()
	if v == nil || v.Codec != "hevc" || v.PixFmt != "yuv420p10le" || v.BitDepth != 10 {
		t.Errorf("%+v", v)
	}
	if v.IsHDR() {
		t.Error("10-bit is not HDR by itself")
	}
}

func TestRealFrameRates(t *testing.T) {
	if v := fixture(t, "real/ntsc_mp4.json").Video(); !near(v.FrameRate, 29.97, 0.01) || v.VFR {
		t.Errorf("29.97 fps clip: %+v", v)
	}
	if v := fixture(t, "real/vfr_mp4.json").Video(); !v.VFR || !near(v.FrameRate, 20.15, 0.1) {
		t.Errorf("variable frame rate mp4 must be flagged: %+v", v)
	}
}

// A variable-frame-rate MKV reports the nominal rate as its average, so stream data alone cannot
// reveal it. This test pins that known limit so nobody assumes otherwise (see Stream.VFR).
func TestRealVFRInMKVIsNotDetectable(t *testing.T) {
	if v := fixture(t, "real/vfr_mkv.json").Video(); v.VFR {
		t.Errorf("if this starts passing detection, update Stream.VFR's comment: %+v", v)
	}
}

func TestRealInterlaced(t *testing.T) {
	if v := fixture(t, "real/inter_mp4.json").Video(); v.FieldOrder != "tt" {
		t.Errorf("field order %q", v.FieldOrder)
	}
}

// ---- hand-written JSON for what lavfi cannot make (testdata/crafted/README.md) ----

func TestCraftedDolbyVisionHDR10(t *testing.T) {
	i := fixture(t, "crafted/dv_hdr10_mkv.json")
	v := i.Video()
	if !v.DolbyVision || v.HDR10Plus || v.ColorTransfer != "smpte2084" || v.ColorPrimaries != "bt2020" || v.BitDepth != 10 {
		t.Errorf("%+v", v)
	}
	if !i.IsHDR() || !i.HasDolbyVision() || !i.HasBitmapSubs() || i.Chapters != 3 {
		t.Errorf("helpers: hdr=%v dv=%v bitmap=%v chapters=%d", i.IsHDR(), i.HasDolbyVision(), i.HasBitmapSubs(), i.Chapters)
	}
	if a := i.Audio(); len(a) != 1 || a[0].Channels != 6 || a[0].ChannelLayout != "5.1(side)" || a[0].BitrateKbps != 768 || a[0].Title == "" {
		t.Errorf("%+v", a)
	}
}

func TestCraftedHDR10Plus(t *testing.T) {
	v := fixture(t, "crafted/hdr10plus_mkv.json").Video()
	if !v.HDR10Plus || v.DolbyVision || !v.IsHDR() {
		t.Errorf("%+v", v)
	}
}

func TestCraftedHLG(t *testing.T) {
	i := fixture(t, "crafted/hlg_bt2020.json")
	if !i.IsHDR() || i.HasDolbyVision() || i.Video().ColorTransfer != "arib-std-b67" {
		t.Errorf("%+v", i.Video())
	}
}

func TestCraftedSDRIsNotHDR(t *testing.T) {
	i := fixture(t, "crafted/sdr_bt709_h264.json")
	if i.IsHDR() || i.HasDolbyVision() {
		t.Error("bt709 is not HDR")
	}
	if i.BitrateKbps != 9000 || i.VideoCodec != "h264" {
		t.Errorf("headline fields changed: %+v", i)
	}
}

func TestCraftedBitmapSubtitles(t *testing.T) {
	i := fixture(t, "crafted/bluray_remux_bitmap_subs.json")
	kinds := []string{}
	for _, s := range i.Subtitles() {
		kinds = append(kinds, s.SubKind)
	}
	if strings.Join(kinds, ",") != "bitmap,text,bitmap" || !i.HasBitmapSubs() {
		t.Errorf("kinds %v", kinds)
	}
	if a := i.Audio(); len(a) != 2 || a[0].Channels != 8 || a[0].ChannelLayout != "7.1" || a[0].BitrateKbps != 0 {
		t.Errorf("audio %+v", a)
	}
}

func TestCraftedOddAndMissingFields(t *testing.T) {
	i := fixture(t, "crafted/odd_missing_fields.json")
	if len(i.Streams) != 6 {
		t.Fatalf("streams %d", len(i.Streams))
	}
	v := i.Video()
	if v.Codec != "mpeg2video" || v.BitDepth != 0 || v.VFR || !near(v.FrameRate, 25, 0.001) {
		t.Errorf("avg 0/0 must fall back to the nominal rate: %+v", v)
	}
	if i.BitrateKbps != 6000 {
		t.Errorf("numeric (not string) bit_rate must still count: %d", i.BitrateKbps)
	}
	if a := i.Audio()[0]; a.Channels != 2 || a.SampleRate != 48000 || a.Language != "" {
		t.Errorf("string channels / numeric sample_rate: %+v", a)
	}
	sub := i.Subtitles()[0]
	if sub.Index != 7 || sub.Language != "ger" || sub.Title != "Deutsch" || sub.SubKind != "" {
		t.Errorf("tags are case-insensitive and unknown codecs stay unclassified: %+v", sub)
	}
	if at := i.Attachments()[0]; at.Filename != "a.ttf" || at.MimeType != "font/ttf" {
		t.Errorf("%+v", at)
	}
	if got := i.ByType("somethingnew"); len(got) != 1 {
		t.Errorf("unknown stream types are kept as ffprobe names them: %+v", got)
	}
	if i.Streams[3].Index != 3 { // no "index" field: position is used
		t.Errorf("index fallback: %d", i.Streams[3].Index)
	}
}

func TestCoverArtListedFirst(t *testing.T) {
	i := fixture(t, "crafted/cover_art_first.json")
	if v := i.Video(); v == nil || v.Codec != "h264" {
		t.Errorf("Video() must skip a leading cover: %+v", v)
	}
	// The headline field keeps its old meaning ("first video stream"): this slice changes no behaviour.
	if i.VideoCodec != "mjpeg" {
		t.Errorf("VideoCodec semantics changed: %q", i.VideoCodec)
	}
}

func TestNoVideoStillAnError(t *testing.T) {
	data, _ := os.ReadFile("testdata/crafted/no_video_audio_only.json")
	if _, err := ParseProbe(data); err == nil {
		t.Error("an audio-only file has no video stream")
	}
}

// ---- robustness ----

func TestGarbageNeverPanics(t *testing.T) {
	inputs := []string{
		"", "null", "[]", "{}", `{"streams":null}`, `{"streams":[null,1,"x",[]]}`,
		`{"streams":[{"codec_type":"video","codec_name":5,"width":"wide","disposition":3,"tags":"no","side_data_list":"no"}]}`,
		`{"streams":[{"codec_type":"video","tags":{"language":7},"side_data_list":[1,null,{"side_data_type":9}]}]}`,
		`{"streams":[{"codec_type":"video","r_frame_rate":"1/0","avg_frame_rate":"x/y","bits_per_raw_sample":"-3"}]}`,
		strings.Repeat("[", 5000), strings.Repeat(`{"a":`, 5000), "\x00\x01\x02garbage",
	}
	for _, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic on %.40q: %v", in, r)
				}
			}()
			ParseProbe([]byte(in)) // an error or a result are both fine; a panic is not
		}()
	}
	// Truncate real output at every length: always an error or a result, never a panic.
	data, _ := os.ReadFile("testdata/real/subs.json")
	for n := 0; n < len(data); n += 7 {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic on truncation at %d: %v", n, r)
				}
			}()
			ParseProbe(data[:n])
		}()
	}
}

func TestOversizeOutputIsRejected(t *testing.T) {
	big := []byte(`{"streams":[{"codec_type":"video"}],"x":"` + strings.Repeat("a", maxProbeBytes) + `"}`)
	if _, err := ParseProbe(big); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("want a too-large error, got %v", err)
	}
}

func TestLimitedBufferStopsRunaways(t *testing.T) {
	var l limitedBuffer
	if _, err := l.Write(make([]byte, maxProbeBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write([]byte("x")); err == nil || !l.over {
		t.Error("writing past the limit must fail")
	}
}

func TestBitDepth(t *testing.T) {
	for _, c := range []struct {
		raw, pix string
		want     int
	}{
		{"10", "yuv420p10le", 10}, {"", "yuv420p10le", 10}, {"", "yuv420p", 8}, {"", "nv12", 8},
		{"", "p010le", 10}, {"", "yuv444p12be", 12}, {"", "gray16le", 16}, {"", "", 0},
		{"junk", "yuv420p", 8}, {"0", "yuv420p10le", 10}, {"99", "yuv420p", 8},
		{"", "y210le", 0}, {"", "rgb48le", 0},
	} {
		if got := bitDepth(c.raw, c.pix); got != c.want {
			t.Errorf("bitDepth(%q,%q)=%d want %d", c.raw, c.pix, got, c.want)
		}
	}
}

func TestFraction(t *testing.T) {
	for in, want := range map[string]float64{"30/1": 30, "30000/1001": 29.97002997, "0/0": 0, "25": 25, "": 0, "a/b": 0, "1/0": 0, " 24 / 1 ": 24} {
		if got := fraction(in); !near(got, want, 1e-6) {
			t.Errorf("fraction(%q)=%v want %v", in, got, want)
		}
	}
}

// ---- the real ffprobe, on files generated here (skipped when ffmpeg is not installed) ----

func needFFmpeg(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("SYP_REQUIRE_FFMPEG") == "1" { // CI's Linux job: these tests must run, never skip
				t.Fatalf("SYP_REQUIRE_FFMPEG=1 but %s is not installed", tool)
			}
			t.Skipf("%s not installed", tool)
		}
	}
}

func ffmpeg(t *testing.T, args ...string) {
	t.Helper()
	out, err := exec.Command("ffmpeg", append([]string{"-v", "error", "-y"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
	}
}

func TestRealFFprobeOnGeneratedFiles(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	srt := filepath.Join(dir, "s.srt")
	os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:01,000\nhi\n"), 0o644)
	ass := filepath.Join(dir, "s.ass")
	os.WriteFile(ass, []byte("[Script Info]\nScriptType: v4.00+\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize\nStyle: Default,Arial,20\n[Events]\nFormat: Layer, Start, End, Style, Text\nDialogue: 0,0:00:00.00,0:00:01.00,Default,hi\n"), 0o644)
	chap := filepath.Join(dir, "ch.txt")
	os.WriteFile(chap, []byte(";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=1000\ntitle=A\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=1000\nEND=2000\ntitle=B\n"), 0o644)
	font := filepath.Join(dir, "f.ttf")
	os.WriteFile(font, []byte("not really a font, ffmpeg only stores the bytes"), 0o644)

	out := filepath.Join(dir, "odd.mkv")
	ffmpeg(t,
		"-f", "lavfi", "-i", "testsrc2=duration=2:size=320x180:rate=25",
		"-f", "lavfi", "-i", "sine=duration=2", "-f", "lavfi", "-i", "sine=frequency=880:duration=2",
		"-i", srt, "-i", ass, "-i", chap,
		"-map", "0", "-map", "1", "-map", "2", "-map", "3", "-map", "4", "-map_metadata", "5",
		"-c:v", "libx264", "-pix_fmt", "yuv420p10le", "-c:a", "aac", "-c:s:0", "srt", "-c:s:1", "ass",
		"-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=jpn", "-metadata:s:a:1", "title=Commentary",
		"-metadata:s:s:1", "language=fre", "-disposition:s:1", "forced",
		"-attach", font, "-metadata:s:t", "mimetype=application/x-truetype-font", out)

	info, err := Probe(context.Background(), OSExec{}, "ffprobe", out)
	if err != nil {
		t.Fatal(err)
	}
	v := info.Video()
	if v == nil || v.Codec != "h264" || v.Width != 320 || v.Height != 180 || v.BitDepth != 10 || !near(v.FrameRate, 25, 0.01) {
		t.Errorf("video %+v", v)
	}
	a := info.Audio()
	if len(a) != 2 || a[0].Language != "eng" || a[1].Language != "jpn" || a[1].Title != "Commentary" {
		t.Errorf("audio %+v", a)
	}
	s := info.Subtitles()
	if len(s) != 2 || s[0].SubKind != "text" || s[1].Codec != "ass" || !s[1].Forced || s[1].Language != "fre" {
		t.Errorf("subs %+v", s)
	}
	if at := info.Attachments(); len(at) != 1 || at[0].Filename != "f.ttf" {
		t.Errorf("attachments %+v", at)
	}
	if info.Chapters != 2 {
		t.Errorf("chapters %d", info.Chapters)
	}
	if info.HasBitmapSubs() || info.IsHDR() {
		t.Error("neither bitmap subtitles nor HDR here")
	}
	if info.VideoCodec != "h264" || info.DurationSec < 1.5 {
		t.Errorf("headline fields %+v", info)
	}
}

func TestRealFFprobeOnNonVideoAndMissingFile(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk.mkv")
	os.WriteFile(junk, []byte("this is not a video"), 0o644)
	if _, err := Probe(context.Background(), OSExec{}, "ffprobe", junk); err == nil {
		t.Error("garbage file must be an error")
	}
	if _, err := Probe(context.Background(), OSExec{}, "ffprobe", filepath.Join(dir, "missing.mkv")); err == nil {
		t.Error("missing file must be an error")
	}
	audio := filepath.Join(dir, "a.mka")
	ffmpeg(t, "-f", "lavfi", "-i", "sine=duration=1", audio)
	if _, err := Probe(context.Background(), OSExec{}, "ffprobe", audio); err == nil || !strings.Contains(err.Error(), "no video") {
		t.Errorf("audio-only: %v", err)
	}
}

// legacyHeadline is the probe parser as it was before the per-stream model (copied from slice 8's
// media.go). The new parser must give identical Container/VideoCodec/BitrateKbps/DurationSec/SizeBytes
// for every file, so that nothing converted today changes.
func legacyHeadline(data []byte) (Info, bool) {
	var p struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			BitRate   string `json:"bit_rate"`
		} `json:"streams"`
		Format struct {
			FormatName string `json:"format_name"`
			Duration   string `json:"duration"`
			Size       string `json:"size"`
			BitRate    string `json:"bit_rate"`
		} `json:"format"`
	}
	if json.Unmarshal(data, &p) != nil {
		return Info{}, false
	}
	info := Info{Container: p.Format.FormatName}
	found := false
	streamRate := 0
	for _, s := range p.Streams {
		if s.CodecType == "video" {
			info.VideoCodec = s.CodecName
			streamRate, _ = strconv.Atoi(s.BitRate)
			found = true
			break
		}
	}
	if !found {
		return Info{}, false
	}
	info.DurationSec, _ = strconv.ParseFloat(p.Format.Duration, 64)
	info.SizeBytes, _ = strconv.ParseInt(p.Format.Size, 10, 64)
	rate := streamRate
	if rate == 0 {
		rate, _ = strconv.Atoi(p.Format.BitRate)
	}
	info.BitrateKbps = rate / 1000
	return info, true
}

func TestHeadlineFieldsUnchangedOnEveryFixture(t *testing.T) {
	files, _ := filepath.Glob("testdata/*/*.json")
	if len(files) < 15 {
		t.Fatalf("expected the fixture set, found %d files", len(files))
	}
	for _, f := range files {
		data, _ := os.ReadFile(f)
		old, oldOK := legacyHeadline(data)
		got, err := ParseProbe(data)
		if oldOK != (err == nil) {
			// the new parser may accept a file the old one could not read (numeric bit_rate), never the reverse
			if oldOK && err != nil {
				t.Errorf("%s: new parser rejects what the old one accepted: %v", f, err)
			}
			continue
		}
		if !oldOK {
			continue
		}
		if got.Container != old.Container || got.VideoCodec != old.VideoCodec || got.BitrateKbps != old.BitrateKbps ||
			got.DurationSec != old.DurationSec || got.SizeBytes != old.SizeBytes {
			t.Errorf("%s: headline fields changed\n old %+v\n new %+v", f, old, got)
		}
	}
}
