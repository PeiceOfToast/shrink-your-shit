// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// The print loudnorm's first pass writes, with the log's words around it, as ffmpeg's stderr has them.
const loudnormPrint = `[Parsed_loudnorm_0 @ 0x55d0c8a4f2c0]
{
	"input_i" : "-31.42",
	"input_tp" : "-6.03",
	"input_lra" : "4.10",
	"input_thresh" : "-41.65",
	"output_i" : "-24.07",
	"target_offset" : "0.07"
}
`

func TestParseLoudnormReadsThePrintAmongJunk(t *testing.T) {
	stderr := "ffmpeg version 7.1.5 Copyright\nInput #0, matroska\nSome progress words\n" + loudnormPrint + "trailing line\n"
	got, ok := ParseLoudnorm(stderr)
	if !ok {
		t.Fatal("no reading from a print with junk around it")
	}
	want := Loudness{I: -31.42, TP: -6.03, LRA: 4.10, Thresh: -41.65, Offset: 0.07}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// A silent track measures "-inf": it has no loudness to even out, so it is not a reading. Neither is a missing
// print or one that is not JSON.
func TestParseLoudnormRefusesSilentAndUnreadable(t *testing.T) {
	silent := strings.Replace(loudnormPrint, `"-31.42"`, `"-inf"`, 1)
	cases := map[string]string{
		"silent track":   silent,
		"no print":       "Input #0 ... nothing printed here\n",
		"not json":       "{ input_i: -31 }",
		"missing values": `{"input_i" : "-31.42"}`,
		"nan value":      strings.Replace(loudnormPrint, `"-6.03"`, `"nan"`, 1),
	}
	for name, s := range cases {
		if _, ok := ParseLoudnorm(s); ok {
			t.Errorf("%s: read as a loudness", name)
		}
	}
}

// Filter order with a fold (F5): the downmix comes first, so the measure and the encode see the same audio; the
// second pass follows; the track goes back to its own rate last.
func TestLoudnessFilterFoldsBeforeLoudnormAndKeepsTheRate(t *testing.T) {
	op := TrackOp{Index: 1, Type: "audio", Encoder: "aac", Channels: 2,
		Loudness: &Loudness{Rate: 48000, I: -31.42, TP: -6.03, LRA: 4.10, Thresh: -41.65, Offset: 0.07}}
	got := LoudnessFilter(op)
	if !strings.HasPrefix(got, "aformat=channel_layouts=stereo,loudnorm=I=-24:TP=-2:LRA=7:measured_I=-31.42:measured_TP=-6.03:measured_LRA=4.10:measured_thresh=-41.65:offset=0.07:linear=true,") {
		t.Errorf("filter %q: fold and second pass out of order or values missing", got)
	}
	if !strings.HasSuffix(got, ",aresample=48000") {
		t.Errorf("filter %q: the track does not go back to its rate", got)
	}
	if m := LoudnessMeasureFilter(op); m != "aformat=channel_layouts=stereo,loudnorm=I=-24:TP=-2:LRA=7:print_format=json" {
		t.Errorf("measure filter %q", m)
	}
}

// The sample rate the track keeps is the source's: loudnorm's output is 192 kHz, which must not reach the file.
func TestLoudnessFilterKeepsTheSourceSampleRate(t *testing.T) {
	for _, rate := range []int{44100, 48000} {
		op := TrackOp{Index: 1, Type: "audio", Encoder: "eac3", Channels: 6,
			Loudness: &Loudness{Rate: rate, I: -30, TP: -3, LRA: 6, Thresh: -40, Offset: 1}}
		got := LoudnessFilter(op)
		if !strings.HasSuffix(got, ",aresample="+strconv.Itoa(rate)) {
			t.Errorf("rate %d: filter %q", rate, got)
		}
		if strings.Contains(got, "192000") {
			t.Errorf("rate %d: filter %q names 192 kHz", rate, got)
		}
	}
}

// A converted track that was not measured has no loudness filter; nor does one when the Plan is not asked to even
// out loudness.
func TestLoudnessFilterOnlyForMeasuredTracks(t *testing.T) {
	if got := LoudnessFilter(TrackOp{Index: 1, Encoder: "aac"}); got != "" {
		t.Errorf("unmeasured track: %q", got)
	}
}

// A converted track gets its filter when the Plan asks; a copied track never does, and the filter is absent when
// the Plan does not ask.
func TestLoudnessFilterGoesOnConvertedTracksOnly(t *testing.T) {
	meas := &Loudness{Rate: 48000, I: -31, TP: -6, LRA: 4, Thresh: -41, Offset: 0.5}
	plan := Plan{EvenLoudness: true, AudioOrder: []int{1, 2}, Tracks: []TrackOp{
		{Index: 1, Type: "audio", Encoder: "aac", Kbps: 128, Channels: 2, Loudness: meas},
	}}
	args := strings.Join(plan.trackArgs(), " ")
	if !strings.Contains(args, "-filter:a:0 ") {
		t.Errorf("converted track has no loudness filter: %s", args)
	}
	if strings.Contains(args, "-filter:a:1") {
		t.Errorf("copied track was filtered: %s", args)
	}
	plan.EvenLoudness = false
	if args := strings.Join(plan.trackArgs(), " "); strings.Contains(args, "-filter") {
		t.Errorf("setting off still filters: %s", args)
	}
}

// An added track (S6's stereo copy) is converted too, so it gets the filter; the original it is made from is copied
// and does not.
func TestLoudnessFilterGoesOnAnAddedTrack(t *testing.T) {
	meas := &Loudness{Rate: 48000, I: -31, TP: -6, LRA: 4, Thresh: -41, Offset: 0.5}
	plan := Plan{EvenLoudness: true, AudioOrder: []int{1}, Tracks: []TrackOp{
		{Index: 1, Type: "audio", Encoder: "aac", Kbps: 192, Channels: 2, Add: true, Loudness: meas},
	}}
	args := strings.Join(plan.trackArgs(), " ")
	if !strings.Contains(args, "-filter:a:1 ") {
		t.Errorf("added track has no loudness filter: %s", args)
	}
	if strings.Contains(args, "-filter:a:0") {
		t.Errorf("the copied original was filtered: %s", args)
	}
}

// The measured numbers and the rate are not settings: they leave the fingerprint, so measuring does not change
// which settings a file was converted with. The setting itself is in it, when on.
func TestLoudnessFingerprintIgnoresMeasurementButNotTheSetting(t *testing.T) {
	base := func(on bool, m *Loudness) Snapshot {
		return Snapshot{Plan: Plan{EvenLoudness: on, Kind: "x", Tracks: []TrackOp{
			{Index: 1, Type: "audio", Encoder: "aac", Kbps: 128, Channels: 2, Loudness: m},
		}}}
	}
	off := base(false, nil).Fingerprint()
	if off != base(false, &Loudness{Rate: 48000, I: -31}).Fingerprint() {
		t.Error("a measurement changed the fingerprint with the setting off")
	}
	on := base(true, nil).Fingerprint()
	if on != base(true, &Loudness{Rate: 48000, I: -31}).Fingerprint() {
		t.Error("a measurement changed the fingerprint with the setting on")
	}
	if on == off {
		t.Error("the setting does not change the fingerprint")
	}
}

// Off by default and off in every template a person has not turned it on in: an unset selector stays out of the JSON.
func TestEvenLoudnessIsOffByDefault(t *testing.T) {
	if DefaultSelectors(3).EvenLoudness {
		t.Error("even loudness is on by default")
	}
}

// The setting reaches the Plan from the selectors, so the worker and the fingerprint see it.
func TestEvenLoudnessReachesThePlan(t *testing.T) {
	if !compileSelectors(Plan{}, Selectors{EvenLoudness: true}, media.Info{}, nil, false).EvenLoudness {
		t.Error("the selector's setting does not reach the plan")
	}
	if compileSelectors(Plan{}, Selectors{}, media.Info{}, nil, false).EvenLoudness {
		t.Error("an unset selector turns the setting on")
	}
}
