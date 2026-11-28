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

// The probe's Run calls, in order: 0 makes the clip, 1 decodes it, 2 scales it, 3 encodes 10-bit, 4 decodes that,
// 5 makes the HDR10 source, 6 encodes it on the card.
const (
	runClip = iota
	runDecode
	runScale
	runEncode10
	runDecode10
	runHDRSource
	runHDREncode
)

// answerHDR makes the HDR step read h for every output in the test, and restores the reader when the test ends.
func answerHDR(t *testing.T, h media.HDR) {
	t.Helper()
	old := frameHDR
	frameHDR = func(context.Context, string) (media.HDR, error) { return h, nil }
	t.Cleanup(func() { frameHDR = old })
}

// probeRuns is the script for a probe where the named call fails with err (nil means every call passes).
func probeRuns(failAt int, err error) []fake.RunScript {
	runs := make([]fake.RunScript, 7)
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
			filepath.Join(dir, c.Encoder+".hdr.mkv"): {Video: []media.VideoTrack{{Codec: "hevc", Encoder: tag, Width: 1920, Height: 1080,
				HDR: media.HDR{MasteringDisplay: true, ContentLight: true}}}},
		},
	}
}

func TestProbePassesEveryStepOnAWorkingCard(t *testing.T) {
	answerHDR(t, media.HDR{MasteringDisplay: true, ContentLight: true})
	dir := t.TempDir()
	c := Candidate{Encoder: "hevc_vaapi", Family: encode.FamilyVAAPI, Codec: "hevc", Device: "/dev/dri/renderD128"}
	rec := Probe(context.Background(), probeMedia(dir, c, 240, "Lavc61.3.100 hevc_vaapi"), dir, c)
	if !rec.Decode || !rec.Scale || !rec.Encode10 || !rec.Decode10 || !rec.HDRSurvives {
		t.Fatalf("record = %+v; want every step passed", rec)
	}
	if len(rec.Reasons) != 0 {
		t.Errorf("reasons = %v; want none when every step passes", rec.Reasons)
	}
}

// A card whose decode fails cannot run the full pipeline: the later steps are recorded as not tried, with a reason.
func TestProbeDecodeFailureStopsTheRest(t *testing.T) {
	answerHDR(t, media.HDR{MasteringDisplay: true, ContentLight: true})
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
	answerHDR(t, media.HDR{MasteringDisplay: true, ContentLight: true})
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

// A scale run that succeeded but whose output cannot be probed fails the scale step with the plain
// reason (N3, #880): a missing or unreadable output is not a working scale.
func TestProbeScaleProbeErrorIsCant(t *testing.T) {
	answerHDR(t, media.HDR{MasteringDisplay: true, ContentLight: true})
	dir := t.TempDir()
	c := Candidate{Encoder: "hevc_vaapi", Family: encode.FamilyVAAPI, Codec: "hevc", Device: "/dev/dri/renderD128"}
	m := probeMedia(dir, c, 240, "Lavc61.3.100 hevc_vaapi")
	m.ProbeErrors = map[string]error{
		filepath.Join(dir, c.Encoder+".scale.mkv"): errString("the scaled file was not written"),
	}
	rec := Probe(context.Background(), m, dir, c)
	if rec.Scale {
		t.Fatal("scale passed after its output could not be read")
	}
	want := "can't scale on this card: the driver refused: the scaled file was not written"
	if rec.Reasons["scale"] != want {
		t.Errorf("scale reason = %q, want %q", rec.Reasons["scale"], want)
	}
}

// The same for the 10-bit encode's output: unreadable is a failed step with its own prefix, and the
// HDR step still waits on a 10-bit encode that passed (N3, #880).
func TestProbeTenBitProbeErrorSaysItCouldNotBeRead(t *testing.T) {
	answerHDR(t, media.HDR{MasteringDisplay: true, ContentLight: true})
	dir := t.TempDir()
	c := Candidate{Encoder: "hevc_vaapi", Family: encode.FamilyVAAPI, Codec: "hevc", Device: "/dev/dri/renderD128"}
	m := probeMedia(dir, c, 240, "Lavc61.3.100 hevc_vaapi")
	m.ProbeErrors = map[string]error{
		filepath.Join(dir, c.Encoder+".10bit.mkv"): errString("the 10-bit file was not written"),
	}
	rec := Probe(context.Background(), m, dir, c)
	if rec.Encode10 || rec.Decode10 || rec.HDRSurvives {
		t.Errorf("record = %+v; want nothing past the unreadable 10-bit output", rec)
	}
	want := "the 10-bit output could not be read: the driver refused: the 10-bit file was not written"
	if rec.Reasons["encode10"] != want {
		t.Errorf("encode10 reason = %q, want %q", rec.Reasons["encode10"], want)
	}
	if !strings.Contains(rec.Reasons["decode10"], "not tried") || !strings.Contains(rec.Reasons["hdr"], "not tried") {
		t.Errorf("later reasons = %q / %q; want not tried", rec.Reasons["decode10"], rec.Reasons["hdr"])
	}
}

// A scaled output that is not 240 high is not a scale.
func TestProbeScaleWrongHeightFails(t *testing.T) {
	answerHDR(t, media.HDR{MasteringDisplay: true, ContentLight: true})
	dir := t.TempDir()
	c := Candidate{Encoder: "hevc_vaapi", Family: encode.FamilyVAAPI, Codec: "hevc", Device: "/dev/dri/renderD128"}
	rec := Probe(context.Background(), probeMedia(dir, c, 480, "Lavc61.3.100 hevc_vaapi"), dir, c)
	if rec.Scale || !strings.Contains(rec.Reasons["scale"], "not 240p") {
		t.Errorf("scale = %v, reason %q; want not 240p", rec.Scale, rec.Reasons["scale"])
	}
}

// The 10-bit encode counts only when its output carries the card's own ENCODER tag.
func TestProbeTenBitNeedsTheCardsTag(t *testing.T) {
	answerHDR(t, media.HDR{MasteringDisplay: true, ContentLight: true})
	dir := t.TempDir()
	c := Candidate{Encoder: "hevc_vaapi", Family: encode.FamilyVAAPI, Codec: "hevc", Device: "/dev/dri/renderD128"}
	rec := Probe(context.Background(), probeMedia(dir, c, 240, "Lavc61.3.100 libx265"), dir, c)
	if rec.Encode10 || !strings.Contains(rec.Reasons["encode10"], "written by") {
		t.Errorf("encode10 = %v, reason %q; want a refusal naming the tag", rec.Encode10, rec.Reasons["encode10"])
	}
}

// A decode that runs past its own limit is recorded as too slow, not as a crash.
func TestProbeDecodeTimesOutOnItsOwnLimit(t *testing.T) {
	answerHDR(t, media.HDR{MasteringDisplay: true, ContentLight: true})
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

// The whole probe has one budget: a card whose steps run past it is recorded as out of time, with the reason.
func TestProbeStopsAtTheBudget(t *testing.T) {
	answerHDR(t, media.HDR{MasteringDisplay: true, ContentLight: true})
	defer func(d time.Duration) { ProbeBudget = d }(ProbeBudget)
	defer func(d time.Duration) { DecodeTimeout = d }(DecodeTimeout)
	ProbeBudget = 80 * time.Millisecond
	DecodeTimeout = 10 * time.Second
	dir := t.TempDir()
	c := Candidate{Encoder: "h264_vaapi", Family: encode.FamilyVAAPI, Codec: "h264", Device: "/dev/dri/renderD128"}
	m := probeMedia(dir, c, 240, "Lavc61.3.100 h264_vaapi")
	m.Runs = []fake.RunScript{{}, {Hang: true}}
	rec := Probe(context.Background(), m, dir, c)
	if rec.Decode || !strings.Contains(rec.Reasons["decode"], "ran out of its") {
		t.Errorf("decode = %v, reason %q; want the probe to report it ran out of its budget", rec.Decode, rec.Reasons["decode"])
	}
}

// HDR metadata that the card's encode drops is not carried: the record says so, and the reason is plain.
func TestProbeHDRMetadataLostIsNotCarried(t *testing.T) {
	dir := t.TempDir()
	c := Candidate{Encoder: "hevc_vaapi", Family: encode.FamilyVAAPI, Codec: "hevc", Device: "/dev/dri/renderD128"}
	answerHDR(t, media.HDR{MasteringDisplay: true, ContentLight: false})
	m := probeMedia(dir, c, 240, "Lavc61.3.100 hevc_vaapi")
	rec := Probe(context.Background(), m, dir, c)
	if rec.HDRSurvives || !strings.Contains(rec.Reasons["hdr"], "did not survive") {
		t.Errorf("hdr = %v, reason %q; want the lost metadata refused", rec.HDRSurvives, rec.Reasons["hdr"])
	}
}

// N2: NVENC's scale runs on the processor and the record says so; QSV and VAAPI scale on the card.
func TestScaleRecordSaysWhereItRan(t *testing.T) {
	for _, tc := range []struct {
		c    Candidate
		want string
	}{
		{Candidate{Encoder: "hevc_nvenc", Family: encode.FamilyNVENC, Codec: "hevc"}, "processor"},
		{Candidate{Encoder: "hevc_vaapi", Family: encode.FamilyVAAPI, Codec: "hevc", Device: "/dev/dri/renderD128"}, "card"},
	} {
		dir := t.TempDir()
		answerHDR(t, media.HDR{MasteringDisplay: true, ContentLight: true})
		rec := Probe(context.Background(), probeMedia(dir, tc.c, 240, "Lavc61.3.100 "+tc.c.Encoder), dir, tc.c)
		if !rec.Scale || rec.ScaleOn != tc.want {
			t.Errorf("%s: scale = %v on %q; want scaled on %q", tc.c.Encoder, rec.Scale, rec.ScaleOn, tc.want)
		}
	}
}
