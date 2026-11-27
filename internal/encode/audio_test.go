// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package encode

import (
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

func audioInfo(tracks ...media.AudioTrack) media.Info {
	info := baseInfo()
	info.Audio = tracks
	return info
}

func convertPlan(entry AudioPlan) Plan {
	plan := basePlan(CodecHEVC)
	plan.Audio = []AudioPlan{entry}
	return plan
}

// TestAudioNeverUpmix is L-AUD-9/K3: a mono source asked for stereo stays mono, and no -ac is
// written because the output's channel count does not change.
func TestAudioNeverUpmix(t *testing.T) {
	info := audioInfo(media.AudioTrack{Index: 1, Codec: "aac", Channels: 1})
	plan := convertPlan(AudioPlan{Index: 1, Action: StreamConvert, Codec: "aac", Channels: 2})

	streams, _, err := AudioOutputs(plan, info)
	if err != nil {
		t.Fatalf("outputs: %v", err)
	}
	if len(streams) != 1 || streams[0].Channels != 1 {
		t.Fatalf("streams = %+v, want one mono track", streams)
	}
	res := buildOK(t, plan, info, cpuRung(CodecHEVC))
	if _, has := flagValue(res.Argv, "-ac:a:0"); has {
		t.Errorf("argv = %v, want no -ac for a mono source", res.Argv)
	}
	if v, _ := flagValue(res.Argv, "-b:a:0"); v != "64k" {
		t.Errorf("bitrate = %q, want 64k for mono AAC", v)
	}
}

// TestAudioFoldsSevenOneToFiveOne is L-AUD-6: AC-3 and E-AC-3 hold six channels, so a 7.1 track
// becomes 5.1 rather than failing.
func TestAudioFoldsSevenOneToFiveOne(t *testing.T) {
	info := audioInfo(media.AudioTrack{Index: 1, Codec: "truehd", Channels: 8})
	plan := convertPlan(AudioPlan{Index: 1, Action: StreamConvert, Codec: "eac3"})

	streams, _, err := AudioOutputs(plan, info)
	if err != nil {
		t.Fatalf("outputs: %v", err)
	}
	if len(streams) != 1 || streams[0].Channels != 6 || streams[0].Codec != "eac3" {
		t.Fatalf("streams = %+v, want 5.1 E-AC-3", streams)
	}
	res := buildOK(t, plan, info, cpuRung(CodecHEVC))
	if v, _ := flagValue(res.Argv, "-ac:a:0"); v != "6" {
		t.Errorf("channels = %q, want 6", v)
	}
}

// TestAudioAutoBitrate is L-AUD-7: per channel, 640 kbps at most, stereo when the count is
// unknown.
func TestAudioAutoBitrate(t *testing.T) {
	cases := []struct {
		codec    string
		channels int
		want     int
	}{
		{"aac", 2, 128},
		{"aac", 6, 384},
		{"aac", 0, 128},
		{"aac", 16, 640},
		{"opus", 2, 96},
		{"eac3", 6, 576},
		{"ac3", 2, 192},
		{"flac", 6, 0},
	}
	for _, c := range cases {
		if got := AutoBitrate(c.codec, c.channels); got != c.want {
			t.Errorf("AutoBitrate(%s, %d) = %d, want %d", c.codec, c.channels, got, c.want)
		}
	}
}

// TestAudioTypedBitrate is page 07's bitrate: per channel unless the track is chosen, and a whole
// number the person typed.
func TestAudioTypedBitrate(t *testing.T) {
	info := audioInfo(media.AudioTrack{Index: 1, Codec: "ac3", Channels: 6})
	plan := convertPlan(AudioPlan{Index: 1, Action: StreamConvert, Codec: "aac", BitrateKbps: 96})
	res := buildOK(t, plan, info, cpuRung(CodecHEVC))
	if v, _ := flagValue(res.Argv, "-b:a:0"); v != "576k" {
		t.Errorf("per-channel bitrate = %q, want 576k", v)
	}

	plan = convertPlan(AudioPlan{Index: 1, Action: StreamConvert, Codec: "aac", BitrateKbps: 448, BitratePer: "track"})
	res = buildOK(t, plan, info, cpuRung(CodecHEVC))
	if v, _ := flagValue(res.Argv, "-b:a:0"); v != "448k" {
		t.Errorf("whole-track bitrate = %q, want 448k", v)
	}
}

// TestAudioDownmixModes is K4: the plain mix is ffmpeg's own, "clear speech" is a pan that raises
// the centre channel, and Pro Logic II is ffmpeg's matrix.
func TestAudioDownmixModes(t *testing.T) {
	info := audioInfo(media.AudioTrack{Index: 1, Codec: "eac3", Channels: 6})
	cases := map[string]string{
		"":       "",
		"plain":  "",
		"speech": "aformat=channel_layouts=5.1,pan=stereo",
		"dpl2":   "aresample=matrix_encoding=dplii",
	}
	for mode, want := range cases {
		plan := convertPlan(AudioPlan{Index: 1, Action: StreamConvert, Codec: "aac", Channels: 2, Downmix: mode})
		res := buildOK(t, plan, info, cpuRung(CodecHEVC))
		got, has := flagValue(res.Argv, "-filter:a:0")
		if want == "" {
			if has {
				t.Errorf("%q: filter = %q, want none", mode, got)
			}
			continue
		}
		if !has || !strings.HasPrefix(got, want) {
			t.Errorf("%q: filter = %q, want it to start %q", mode, got, want)
		}
	}
}

// TestAudioLoudnormFilter is L-AUD-15: the measured numbers are applied linearly and the track
// goes back to its own rate.
func TestAudioLoudnormFilter(t *testing.T) {
	info := audioInfo(media.AudioTrack{Index: 1, Codec: "eac3", Channels: 6, SampleRate: 48000})
	plan := convertPlan(AudioPlan{
		Index: 1, Action: StreamConvert, Codec: "aac", Channels: 2, Loudnorm: true,
		Loudness: &Loudness{I: -21.5, TP: -3.2, LRA: 6.1, Threshold: -31.7, Offset: 0.02},
	})
	res := buildOK(t, plan, info, cpuRung(CodecHEVC))
	filter, has := flagValue(res.Argv, "-filter:a:0")
	if !has {
		t.Fatalf("argv = %v, want a filter", res.Argv)
	}
	for _, part := range []string{
		"aformat=channel_layouts=stereo",
		"loudnorm=I=-24:TP=-2:LRA=7",
		"measured_I=-21.50",
		"measured_TP=-3.20",
		"linear=true",
		"aresample=48000",
	} {
		if !strings.Contains(filter, part) {
			t.Errorf("filter = %q, want %q", filter, part)
		}
	}
	if _, has := flagValue(res.Argv, "-ar:a:0"); has {
		t.Errorf("argv = %v, want no -ar beside the filter's aresample", res.Argv)
	}
}

// TestAudioSampleRateArg is the sample-rate choice when no filter is needed.
func TestAudioSampleRateArg(t *testing.T) {
	info := audioInfo(media.AudioTrack{Index: 1, Codec: "eac3", Channels: 2, SampleRate: 48000})
	plan := convertPlan(AudioPlan{Index: 1, Action: StreamConvert, Codec: "aac", SampleRate: 44100})
	res := buildOK(t, plan, info, cpuRung(CodecHEVC))
	if v, _ := flagValue(res.Argv, "-ar:a:0"); v != "44100" {
		t.Errorf("sample rate = %q, want 44100", v)
	}
}

// TestAudioDefaultDisposition is L-AUD-12: the chosen track is marked default and the rest are
// cleared; nil leaves the file's own flags.
func TestAudioDefaultDisposition(t *testing.T) {
	yes, no := true, false
	info := audioInfo(
		media.AudioTrack{Index: 1, Codec: "aac", Channels: 2, Default: true},
		media.AudioTrack{Index: 2, Codec: "aac", Channels: 2},
	)
	plan := basePlan(CodecHEVC)
	plan.Audio = []AudioPlan{{Index: 1, Default: &no}, {Index: 2, Default: &yes}}
	res := buildOK(t, plan, info, cpuRung(CodecHEVC))
	if v, _ := flagValue(res.Argv, "-disposition:a:0"); v != "0" {
		t.Errorf("first disposition = %q, want 0", v)
	}
	if v, _ := flagValue(res.Argv, "-disposition:a:1"); v != "default" {
		t.Errorf("second disposition = %q, want default", v)
	}

	plan = basePlan(CodecHEVC)
	res = buildOK(t, plan, info, cpuRung(CodecHEVC))
	if hasFlag(res.Argv, "-disposition:a:0") {
		t.Errorf("argv = %v, want the file's own default flags", res.Argv)
	}
}

// TestParseLoudness reads loudnorm's report and refuses a number that is not finite: -inf is
// silence's loudness, and a linear pass over it would fail.
func TestParseLoudness(t *testing.T) {
	lines := []string{
		"[Parsed_loudnorm_0 @ 0x55] ",
		"{",
		`	"input_i" : "-23.45",`,
		`	"input_tp" : "-2.10",`,
		`	"input_lra" : "7.30",`,
		`	"input_thresh" : "-33.46",`,
		`	"output_i" : "-24.02",`,
		`	"target_offset" : "0.02"`,
		"}",
	}
	l, err := ParseLoudness(lines)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if l.I != -23.45 || l.TP != -2.10 || l.LRA != 7.30 || l.Threshold != -33.46 || l.Offset != 0.02 {
		t.Errorf("loudness = %+v", l)
	}

	silent := []string{
		"{",
		`	"input_i" : "-inf",`,
		`	"input_tp" : "-inf",`,
		`	"input_lra" : "0.00",`,
		`	"input_thresh" : "-inf",`,
		`	"target_offset" : "-inf"`,
		"}",
	}
	if _, err := ParseLoudness(silent); err == nil {
		t.Error("a -inf measurement must be refused")
	}
	if _, err := ParseLoudness([]string{"no report here"}); err == nil {
		t.Error("a missing report must be refused")
	}
}

// TestAudioConvertedCodecTheContainerCannotHoldFallsBack is L-AUD-10 on the convert branch: the
// step can ask for a codec the file type cannot hold (the step does not see the container), so
// Build writes the fallback codec instead and the note says which and why.
func TestAudioConvertedCodecTheContainerCannotHoldFallsBack(t *testing.T) {
	info := audioInfo(media.AudioTrack{Index: 1, Codec: "eac3", Channels: 6})
	plan := convertPlan(AudioPlan{Index: 1, Action: StreamConvert, Codec: "opus"})
	plan.Container = ContainerMP4
	plan.Output = "out.mp4"

	streams, notes, err := AudioOutputs(plan, info)
	if err != nil {
		t.Fatalf("outputs: %v", err)
	}
	if len(streams) != 1 || streams[0].Codec != "eac3" || streams[0].Channels != 6 {
		t.Fatalf("streams = %+v, want one 5.1 E-AC-3 track", streams)
	}
	want := "audio stream #1 (opus) is written as eac3: MP4 cannot hold Opus"
	if len(notes) != 1 || notes[0] != want {
		t.Fatalf("notes = %v, want %q", notes, want)
	}

	res := buildOK(t, plan, info, cpuRung(CodecHEVC))
	if v, _ := flagValue(res.Argv, "-c:a:0"); v != "eac3" {
		t.Errorf("codec = %q, want eac3", v)
	}
	if v, _ := flagValue(res.Argv, "-b:a:0"); v != "576k" {
		t.Errorf("bitrate = %q, want 576k for 5.1 E-AC-3", v)
	}
	// The fallback keeps the source's channel count, so no -ac is written.
	if hasFlag(res.Argv, "-ac:a:0") {
		t.Errorf("argv = %v, want no -ac when the fallback keeps the channels", res.Argv)
	}
}

// TestAudioCopyEntriesWriteNothing: a plan that names every stream as a copy writes exactly the
// command the audio step wrote before conversion existed (the golden table's rows).
func TestAudioCopyEntriesWriteNothing(t *testing.T) {
	info := audioInfo(media.AudioTrack{Index: 1, Codec: "eac3", Channels: 6})
	plan := basePlan(CodecHEVC)
	plan.Audio = []AudioPlan{{Index: 1}}

	withEntry := buildOK(t, plan, info, cpuRung(CodecHEVC))
	plain := buildOK(t, basePlan(CodecHEVC), info, cpuRung(CodecHEVC))
	if strings.Join(withEntry.Argv, " ") != strings.Join(plain.Argv, " ") {
		t.Errorf("argv with a copy entry = %v, want %v", withEntry.Argv, plain.Argv)
	}
}
