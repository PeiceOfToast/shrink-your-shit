// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
)

const sampleProbe = `{"streams":[{"codec_type":"audio","codec_name":"aac"},
{"codec_type":"video","codec_name":"h264","bit_rate":"6500000"}],
"format":{"format_name":"matroska,webm","duration":"120.5","size":"98000000","bit_rate":"6800000"}}`

func TestProbe(t *testing.T) {
	f := &FakeExec{Out: []byte(sampleProbe)}
	info, err := Probe(context.Background(), f, "ffprobe", "/x/a.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if info.VideoCodec != "h264" || info.BitrateKbps != 6500 || info.DurationSec != 120.5 || info.SizeBytes != 98000000 {
		t.Errorf("unexpected info %+v", info)
	}
	if f.Recorded()[0][0] != "ffprobe" || f.Recorded()[0][len(f.Recorded()[0])-1] != "/x/a.mkv" {
		t.Errorf("bad call %v", f.Recorded()[0])
	}
}

func TestProbeFallsBackToFormatBitrate(t *testing.T) {
	info, err := ParseProbe([]byte(`{"streams":[{"codec_type":"video","codec_name":"h264"}],"format":{"bit_rate":"5000000"}}`))
	if err != nil || info.BitrateKbps != 5000 {
		t.Errorf("got %+v %v", info, err)
	}
}

func TestProbeErrors(t *testing.T) {
	if _, err := ParseProbe([]byte(`{"streams":[{"codec_type":"audio"}],"format":{}}`)); err == nil {
		t.Error("want error for no video stream")
	}
	if _, err := ParseProbe([]byte(`nope`)); err == nil {
		t.Error("want error for bad json")
	}
	f := &FakeExec{Err: errors.New("boom")}
	if _, err := Probe(context.Background(), f, "ffprobe", "x"); err == nil {
		t.Error("want exec error")
	}
}

func TestParseProgress(t *testing.T) {
	if p, ok := ParseProgress("out_time_us=60250000", 120.5); !ok || p < 49.9 || p > 50.1 {
		t.Errorf("got %v %v", p, ok)
	}
	if p, _ := ParseProgress("out_time_us=999000000", 120); p != 100 {
		t.Errorf("should cap at 100, got %v", p)
	}
	for _, l := range []string{"frame=10", "out_time_us=N/A", "out_time_us=-5"} {
		if _, ok := ParseProgress(l, 100); ok {
			t.Errorf("%q should not parse", l)
		}
	}
	if _, ok := ParseProgress("out_time_us=5", 0); ok {
		t.Error("zero duration should not parse")
	}
}

func TestTranscodeReportsProgress(t *testing.T) {
	f := &FakeExec{Lines: []string{"out_time_us=30000000", "progress=continue", "out_time_us=60000000"}}
	var got []float64
	_, err := Transcode(context.Background(), f, "ffmpeg", []string{"-i", "x"}, 60, func(p Progress) { got = append(got, p.Percent) })
	if err != nil || len(got) != 2 || got[1] != 100 {
		t.Errorf("got %v %v", got, err)
	}
}

// A real -progress block (S5): fps, bitrate and speed come with the percent, and the ETA is the
// source time left at that speed; "N/A" reads as unknown.
func TestTranscodeProgressFields(t *testing.T) {
	f := &FakeExec{Lines: []string{"frame=750", "fps=50.0", "bitrate=4074.8kbits/s", "out_time_us=29000000", "speed=2.00x", "progress=continue",
		"fps=48.5", "bitrate=4100.2kbits/s", "out_time_us=30000000", "speed=2.00x", "progress=continue",
		"bitrate=N/A", "speed=N/A", "out_time_us=31000000"}}
	var got []Progress
	Transcode(context.Background(), f, "ffmpeg", nil, 60, func(p Progress) { got = append(got, p) })
	if len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	if g := got[0]; g.FPS != 50 || g.BitrateKbps != 4074.8 || g.Speed != 0 || g.ETASeconds != -1 { // speed comes after out_time in a block
		t.Errorf("first %+v", g)
	}
	if g := got[1]; g.Percent != 50 || g.FPS != 48.5 || g.Speed != 2 || g.ETASeconds != 15 {
		t.Errorf("second %+v", g)
	}
	if g := got[2]; g.BitrateKbps != 0 || g.Speed != 0 || g.ETASeconds != -1 {
		t.Errorf("N/A %+v", g)
	}
}

// FFmpeg 7.1's ffprobe prints "75," (an empty side-data field) for a stream with side data (HDR, a phone
// video's rotation): such a file was refused as "counting the new file's video frames could not be done".
func TestCountVideoPacketsWithSideData(t *testing.T) {
	for out, want := range map[string]int64{"75,\n": 75, "75\n": 75, "75,,\n": 75, " 1800 \n": 1800} {
		x := &FakeExec{Out: []byte(out)}
		if n, err := CountVideoPackets(context.Background(), x, "ffprobe", "/x.mkv", 0); err != nil || n != want {
			t.Errorf("%q: %d %v", out, n, err)
		}
	}
	if _, err := CountVideoPackets(context.Background(), &FakeExec{Out: []byte(",75\n")}, "ffprobe", "/x.mkv", 0); err == nil {
		t.Error("no count in the first field is an error")
	}
}

// A phone video's Display Matrix (#81): its rotation is read; ±90/270 is a quarter turn.
func TestDisplayMatrixRotation(t *testing.T) {
	i, err := ParseProbe([]byte(`{"streams":[{"codec_type":"video","codec_name":"h264","width":640,"height":360,
	 "side_data_list":[{"side_data_type":"Display Matrix","displaymatrix":"...","rotation":-90}]}],"format":{}}`))
	if err != nil || i.Video().Rotation != -90 || !i.Video().Quarter() {
		t.Fatalf("%+v %v", i.Video(), err)
	}
	for r, q := range map[int]bool{90: true, 270: true, -270: true, 180: false, 0: false} {
		if (Stream{Rotation: r}).Quarter() != q {
			t.Errorf("%d", r)
		}
	}
}

// The encoders ffmpeg lists (S6: which video formats Standard offers).
func TestEncoders(t *testing.T) {
	out := "Encoders:\n V..... = Video\n ------\n V....D libx264              libx264 H.264 / AVC\n V....D libsvtav1            SVT-AV1\n A....D aac                  AAC (Advanced Audio Coding)\n"
	got := Encoders(context.Background(), &FakeExec{Out: []byte(out)}, "ffmpeg")
	if !got["libx264"] || !got["libsvtav1"] || !got["aac"] || got["="] || got["Video"] || len(got) != 3 {
		t.Errorf("%v", got)
	}
}

// Research 3.4: the time left is smoothed over the last 5 points (the raw one jumps on the first GOP and
// with each change of speed), so the card's "about N min left" doesn't swing.
func TestTranscodeSmoothsTimeLeft(t *testing.T) {
	var lines []string
	speeds := []string{"4x", "0.5x", "4x", "0.5x", "4x", "0.5x", "4x", "0.5x"} // a speed that swings
	for i, sp := range speeds {
		lines = append(lines, "speed="+sp, fmt.Sprintf("out_time_us=%d", (i+1)*10_000_000))
	}
	f := &FakeExec{Lines: lines}
	var raw, got []float64
	_, err := Transcode(context.Background(), f, "ffmpeg", nil, 1000, func(p Progress) { got = append(got, p.ETASeconds) })
	if err != nil {
		t.Fatal(err)
	}
	for i := range speeds {
		sp := []float64{4, 0.5}[i%2]
		raw = append(raw, (1000-float64(i+1)*10)/sp)
	}
	for i := range got {
		lo := max(0, i-4)
		var sum float64
		for _, r := range raw[lo : i+1] {
			sum += r
		}
		if want := sum / float64(i+1-lo); math.Abs(got[i]-want) > 0.01 {
			t.Errorf("point %d: %.1f, want the 5-point average %.1f (raw %.1f)", i, got[i], want, raw[i])
		}
	}
	// the swing between two points is far smaller than the raw one
	if math.Abs(got[7]-got[6]) > math.Abs(raw[7]-raw[6])/3 {
		t.Errorf("still jumps: %.0f -> %.0f (raw %.0f -> %.0f)", got[6], got[7], raw[6], raw[7])
	}
}
