// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"context"
	"errors"
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
	_, err := Transcode(context.Background(), f, "ffmpeg", []string{"-i", "x"}, 60, func(p float64) { got = append(got, p) })
	if err != nil || len(got) != 2 || got[1] != 100 {
		t.Errorf("got %v %v", got, err)
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
