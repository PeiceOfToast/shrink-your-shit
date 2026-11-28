// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps_test

import (
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// audioWork is whether the audio step has work for a file with one audio track of this codec and bitrate.
func audioWork(t *testing.T, audioCodec string, bitRate int64) bool {
	t.Helper()
	f := setStep(t, starter(t), "audio.tracks", "codec", audioCodec)
	info := media.Info{
		Format: media.Format{Names: []string{"matroska", "webm"}, Duration: time.Hour, Size: 900_000_000},
		Video:  []media.VideoTrack{{Codec: "hevc", Profile: "Main", Width: 1920, Height: 1080, FrameRate: media.Rational{Num: 25, Den: 1}, PixelFormat: "yuv420p", BitDepth: 8}},
		Audio:  []media.AudioTrack{{Codec: "aac", Channels: 2, SampleRate: 48000, Language: "eng", BitRate: bitRate}},
	}
	d, _, err := flow.PlanFile(flow.Revision{WorkflowID: 1, Rev: 1, Flow: f}, info, flow.Env{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range d.Steps {
		if s.ID == "audio" {
			return s.Decision.Work
		}
	}
	t.Fatal("no audio step decision")
	return false
}

// The converts line states the keep rule, and the run agrees with it on both sides: a lossy track at a low bitrate stays
// as it is, and one above the target is converted.
func TestConvertsLineStatesTheAudioKeepRule(t *testing.T) {
	c, _, _ := sentencesOf(t, setStep(t, starter(t), "audio.tracks", "codec", "opus"), 7)
	if !strings.Contains(c, "except tracks already in Opus and lossy tracks already at or below the target bitrate") {
		t.Errorf("converts = %q, want the keep rule stated", c)
	}
	if audioWork(t, "opus", 32_000) {
		t.Error("a 32 kbps AAC track is converted, but the line says lossy tracks at or below the target stay")
	}
	if !audioWork(t, "opus", 512_000) {
		t.Error("a 512 kbps AAC track is kept, but the line says it converts")
	}
}

// Loudness evening converts a track whatever its bitrate, so the line leaves the keep clause out, and the run agrees: a
// 32 kbps AAC track that would otherwise stay is converted.
func TestLoudnessLeavesTheKeepClauseOut(t *testing.T) {
	f := setStep(t, setStep(t, starter(t), "audio.tracks", "codec", "opus"), "audio.tracks", "loudnorm", true)
	c, _, _ := sentencesOf(t, f, 7)
	if strings.Contains(c, "except") || !strings.Contains(c, "Converts audio to Opus.") {
		t.Errorf("converts = %q, want no keep clause when loudness forces the conversion", c)
	}
	if !audioWorkWith(t, f, 32_000) {
		t.Error("loudness is on but a 32 kbps AAC track is kept")
	}
}

// audioWorkWith is audioWork for a given flow.
func audioWorkWith(t *testing.T, f flow.Flow, bitRate int64) bool {
	t.Helper()
	info := media.Info{
		Format: media.Format{Names: []string{"matroska", "webm"}, Duration: time.Hour, Size: 900_000_000},
		Video:  []media.VideoTrack{{Codec: "hevc", Profile: "Main", Width: 1920, Height: 1080, FrameRate: media.Rational{Num: 25, Den: 1}, PixelFormat: "yuv420p", BitDepth: 8}},
		Audio:  []media.AudioTrack{{Codec: "aac", Channels: 2, SampleRate: 48000, Language: "eng", BitRate: bitRate}},
	}
	d, _, err := flow.PlanFile(flow.Revision{WorkflowID: 1, Rev: 1, Flow: f}, info, flow.Env{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range d.Steps {
		if s.ID == "audio" {
			return s.Decision.Work
		}
	}
	t.Fatal("no audio step decision")
	return false
}

// Each branch of the converts line matches the run. A channel layout that matches a stereo track keeps it (the clause
// applies); a layout that differs converts it; loudness converts every track, and its line has no clause.
func TestChannelBranchesMatchTheRun(t *testing.T) {
	stereo := setStep(t, setStep(t, starter(t), "audio.tracks", "codec", "opus"), "audio.tracks", "channels", "stereo")
	c, _, _ := sentencesOf(t, stereo, 7)
	if !strings.Contains(c, "except tracks already in Opus") || !strings.Contains(c, "a channel layout or sample rate that differs from a track converts that track") {
		t.Errorf("stereo layout: converts = %q", c)
	}
	if audioWorkWith(t, stereo, 32_000) {
		t.Error("a stereo layout keeps a low-bitrate stereo AAC track, and the line's clause says it stays")
	}
	mono := setStep(t, setStep(t, starter(t), "audio.tracks", "codec", "opus"), "audio.tracks", "channels", "mono")
	if !audioWorkWith(t, mono, 32_000) {
		t.Error("a mono layout keeps a stereo track: the line's condition says a differing layout converts it")
	}
}

// The HDR wording is the video's alone: a file whose video is HDR with HDR set to skip gets no video work, and the run
// still plans its audio.
func TestHDRLineClaimsOnlyTheVideo(t *testing.T) {
	f := setStep(t, starter(t), "video.convert", "hdr", "skip")
	_, leave, _ := sentencesOf(t, f, 7)
	if !strings.Contains(leave, "Leaves the video in HDR files alone.") {
		t.Errorf("leavesAlone = %q, want the video-only HDR claim", leave)
	}
	hdr := media.Info{
		Format: media.Format{Names: []string{"matroska", "webm"}, Duration: time.Hour, Size: 900_000_000},
		Video:  []media.VideoTrack{{Codec: "hevc", Profile: "Main10", Width: 3840, Height: 2160, FrameRate: media.Rational{Num: 24, Den: 1}, PixelFormat: "yuv420p10le", BitDepth: 10, ColorTransfer: "smpte2084"}},
		Audio:  []media.AudioTrack{{Codec: "aac", Channels: 2, SampleRate: 48000, Language: "eng", BitRate: 32_000}},
	}
	d, _, err := flow.PlanFile(flow.Revision{WorkflowID: 1, Rev: 1, Flow: f}, hdr, flow.Env{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range d.Steps {
		if s.ID == "video" && s.Decision.Work {
			t.Errorf("an HDR video with skip gets video work: %s", s.Decision.Reason)
		}
	}
}
