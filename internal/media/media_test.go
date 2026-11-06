// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"context"
	"errors"
	"strings"
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
	if f.Calls[0][0] != "ffprobe" || f.Calls[0][len(f.Calls[0])-1] != "/x/a.mkv" {
		t.Errorf("bad call %v", f.Calls[0])
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

func TestHEVCArgs(t *testing.T) {
	a := strings.Join(HEVCArgs("in.mkv", "out.mkv", 26, "medium"), " ")
	for _, want := range []string{"-i in.mkv", "-c:v:0 libx265", "-crf 26", "-preset medium", "-progress pipe:1"} {
		if !strings.Contains(a, want) {
			t.Errorf("args %q missing %q", a, want)
		}
	}
	if !strings.HasSuffix(a, "out.mkv") {
		t.Errorf("output must be last: %q", a)
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
	err := Transcode(context.Background(), f, "ffmpeg", []string{"-i", "x"}, 60, func(p float64) { got = append(got, p) })
	if err != nil || len(got) != 2 || got[1] != 100 {
		t.Errorf("got %v %v", got, err)
	}
}
