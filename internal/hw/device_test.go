// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package hw

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media/fake"
)

// The probe's Run calls, in order: 0 makes the clip, 1 decodes it, 2 scales it, 3 encodes 10-bit, 4 decodes that.
const (
	runClip = iota
	runDecode
	runScale
	runEncode10
	runDecode10
)

// probeRuns is the script for a probe where the named call fails with err (nil means every call passes).
func probeRuns(failAt int, err error) []fake.RunScript {
	runs := make([]fake.RunScript, 5)
	if failAt >= 0 {
		runs[failAt] = fake.RunScript{Err: err}
	}
	return runs
}

// probeMedia answers the probe's outputs: a 240p scaled file and a 10-bit file written by the card's own encoder.
func probeMedia(dir string, c Candidate, scaledHeight int, tag string) *fake.Media {
	return &fake.Media{
		Runs: probeRuns(-1, nil),
		Probes: map[string]media.Info{
			filepath.Join(dir, c.Encoder+".scale.mkv"): {Video: []media.VideoTrack{{Codec: "h264", Width: 426, Height: scaledHeight}}},
			filepath.Join(dir, c.Encoder+".10bit.mkv"): {Video: []media.VideoTrack{{Codec: "hevc", Encoder: tag, Width: 256, Height: 256}}},
		},
	}
}

func TestProbePassesEveryStepOnAWorkingCard(t *testing.T) {
	dir := t.TempDir()
	c := Candidate{Encoder: "hevc_vaapi", Family: encode.FamilyVAAPI, Codec: "hevc", Device: "/dev/dri/renderD128"}
	rec := Probe(context.Background(), probeMedia(dir, c, 240, "Lavc61.3.100 hevc_vaapi"), dir, c)
	if !rec.Decode || !rec.Scale || !rec.Encode10 || !rec.Decode10 {
		t.Fatalf("record = %+v; want every step passed", rec)
	}
	if len(rec.Reasons) != 0 {
		t.Errorf("reasons = %v; want none when every step passes", rec.Reasons)
	}
}

// A card whose decode fails cannot run the full pipeline: the later steps are recorded as not tried, with a reason.
func TestProbeDecodeFailureStopsTheRest(t *testing.T) {
	dir := t.TempDir()
	c := Candidate{Encoder: "h264_qsv", Family: encode.FamilyQSV, Codec: "h264"}
	m := probeMedia(dir, c, 240, "Lavc61.3.100 h264_qsv")
	m.Runs = probeRuns(runDecode, errString("driver refused"))
	rec := Probe(context.Background(), m, dir, c)
	if rec.Decode || rec.Scale || rec.Encode10 || rec.Decode10 {
		t.Fatalf("record = %+v; want nothing past a failed decode", rec)
	}
	if rec.Reasons["decode"] == "" || !strings.Contains(rec.Reasons["scale"], "not tried") {
		t.Errorf("reasons = %v; want a decode reason and not-tried scale", rec.Reasons)
	}
}

// A scale failure reads as "can't" on this card; the 10-bit steps still run and can pass.
func TestProbeScaleFailureIsCantNotACrash(t *testing.T) {
	dir := t.TempDir()
	c := Candidate{Encoder: "hevc_vaapi", Family: encode.FamilyVAAPI, Codec: "hevc", Device: "/dev/dri/renderD128"}
	m := probeMedia(dir, c, 240, "Lavc61.3.100 hevc_vaapi")
	m.Runs = probeRuns(runScale, errString("no scaler"))
	rec := Probe(context.Background(), m, dir, c)
	if rec.Scale {
		t.Fatal("scale passed after its run failed")
	}
	if !strings.HasPrefix(rec.Reasons["scale"], "can't scale") {
		t.Errorf("scale reason = %q; want it to start \"can't scale\"", rec.Reasons["scale"])
	}
	if !rec.Encode10 || !rec.Decode10 {
		t.Errorf("the 10-bit steps should still run after a scale failure: %+v", rec)
	}
}

// A scaled output that is not 240 high is not a scale.
func TestProbeScaleWrongHeightFails(t *testing.T) {
	dir := t.TempDir()
	c := Candidate{Encoder: "hevc_vaapi", Family: encode.FamilyVAAPI, Codec: "hevc", Device: "/dev/dri/renderD128"}
	rec := Probe(context.Background(), probeMedia(dir, c, 480, "Lavc61.3.100 hevc_vaapi"), dir, c)
	if rec.Scale || !strings.Contains(rec.Reasons["scale"], "not 240p") {
		t.Errorf("scale = %v, reason %q; want not 240p", rec.Scale, rec.Reasons["scale"])
	}
}

// The 10-bit encode counts only when its output carries the card's own ENCODER tag.
func TestProbeTenBitNeedsTheCardsTag(t *testing.T) {
	dir := t.TempDir()
	c := Candidate{Encoder: "hevc_vaapi", Family: encode.FamilyVAAPI, Codec: "hevc", Device: "/dev/dri/renderD128"}
	rec := Probe(context.Background(), probeMedia(dir, c, 240, "Lavc61.3.100 libx265"), dir, c)
	if rec.Encode10 || !strings.Contains(rec.Reasons["encode10"], "written by") {
		t.Errorf("encode10 = %v, reason %q; want a refusal naming the tag", rec.Encode10, rec.Reasons["encode10"])
	}
}

// A decode that runs past its own limit is recorded as too slow, not as a crash.
func TestProbeDecodeTimesOutOnItsOwnLimit(t *testing.T) {
	defer func(d time.Duration) { DecodeTimeout = d }(DecodeTimeout)
	DecodeTimeout = 50 * time.Millisecond
	dir := t.TempDir()
	c := Candidate{Encoder: "h264_vaapi", Family: encode.FamilyVAAPI, Codec: "h264", Device: "/dev/dri/renderD128"}
	m := probeMedia(dir, c, 240, "Lavc61.3.100 h264_vaapi")
	m.Runs = []fake.RunScript{{}, {Hang: true}}
	rec := Probe(context.Background(), m, dir, c)
	if rec.Decode || !strings.Contains(rec.Reasons["decode"], "did not finish in") {
		t.Errorf("decode = %v, reason %q; want a decode that did not finish", rec.Decode, rec.Reasons["decode"])
	}
}
