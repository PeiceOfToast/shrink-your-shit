// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// inputReport is the encode's end-of-run line for input 0's video stream, as ffmpeg 7 prints it at
// -loglevel level+verbose (slice 13).
func inputReport(packets, frames, decodeErrors int) string {
	return inputReportFor(0, packets, frames, decodeErrors)
}

func inputReportFor(stream, packets, frames, decodeErrors int) string {
	return fmt.Sprintf("[in#0/matroska,webm @ 0x1] [verbose]   Input stream #0:%d (video): %d packets read (1 bytes); %d frames decoded; %d decode errors;\n",
		stream, packets, frames, decodeErrors)
}

func videoInfo() media.Info {
	return media.Info{Streams: []media.Stream{{Index: 0, Type: "video"}, {Index: 1, Type: "audio"}}}
}

// The report is read for the main video stream's own line, and only when it has one.
func TestSourceReportFromTheEncodesLog(t *testing.T) {
	src := videoInfo()
	st, have := sourceReport(src, "[in#0/matroska,webm @ 0x1] [verbose]   Input stream #0:1 (audio): 5 packets read (1 bytes);\n"+
		inputReport(9, 7, 2))
	if !have || st.Packets != 9 || st.Frames != 7 || st.DecodeErrors != 2 {
		t.Errorf("report %+v (have %v), want 9 packets, 7 frames, 2 decode errors", st, have)
	}
	if _, have := sourceReport(src, "[error] something went wrong\n"); have {
		t.Error("a log with no report line must have no report")
	}
	if _, have := sourceReport(src, "[in#0/matroska,webm @ 0x1] [verbose]   Input stream #0:0 (video): 9 packets read (1 bytes);\n"); have {
		t.Error("a copied stream's line (no frames decoded) is not a report to use")
	}
	if _, have := sourceReport(media.Info{}, inputReport(1, 1, 0)); have {
		t.Error("a file with no video has no report to use")
	}
	// A cover picture before the film is not the main video: the report is the film's line.
	art := media.Info{Streams: []media.Stream{{Index: 0, Type: "video", AttachedPic: true}, {Index: 1, Type: "video"}}}
	if _, have := sourceReport(art, inputReportFor(1, 1, 1, 0)); !have {
		t.Error("the main video's line (index 1) should be read")
	}
}

// VFR is marked from the probe's own rates, or from a report that decoded more than 1% away from duration × rate.
func TestSourceVFROnlyWhenTheRatesOrTheCountDisagree(t *testing.T) {
	cfr := media.Info{DurationSec: 100, Streams: []media.Stream{{Index: 0, Type: "video", FrameRate: 25}}}
	vfrProbe := media.Info{DurationSec: 100, Streams: []media.Stream{{Index: 0, Type: "video", FrameRate: 25, VFR: true}}}
	for _, c := range []struct {
		name string
		src  media.Info
		st   media.StreamStats
		have bool
		want bool
	}{
		{"probe mark, no report", vfrProbe, media.StreamStats{}, false, true},
		{"report 10% short of duration × rate", cfr, media.StreamStats{Frames: 2250}, true, true},
		{"report 10% over duration × rate", cfr, media.StreamStats{Frames: 2750}, true, true},
		{"report within 1%", cfr, media.StreamStats{Frames: 2520}, true, false},
		{"steady rate, no report", cfr, media.StreamStats{}, false, false},
		{"no video", media.Info{}, media.StreamStats{Frames: 10}, true, false},
	} {
		if got := sourceVFR(c.src, c.st, c.have); got != c.want {
			t.Errorf("%s: VFR %v, want %v", c.name, got, c.want)
		}
	}
}

// The frame check uses the report's decoded count; a VFR original without a report is not checked against its
// duration × rate guess at all.
func TestCheckedFramesUsesTheReportAndSkipsAVFRGuess(t *testing.T) {
	for _, c := range []struct {
		name  string
		guess int64
		st    media.StreamStats
		have  bool
		vfr   bool
		want  int64
	}{
		{"the report's decoded count", 2500, media.StreamStats{Frames: 90}, true, false, 90},
		{"the report wins for a VFR original too", 271, media.StreamStats{Frames: 180}, true, true, 180},
		{"no report, steady rate: today's count", 2500, media.StreamStats{}, false, false, 2500},
		{"no report, VFR: skipped", 2000, media.StreamStats{}, false, true, 0},
		{"a report of no frames is not used", 2500, media.StreamStats{Frames: 0}, true, false, 2500},
	} {
		if got := checkedFrames(c.guess, c.st, c.have, c.vfr); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
}

// The report's decode errors are the damaged-picture count; frames the decode did not take stay evidence, and
// the text's own count is the floor.
func TestDamagedPicturesUsesTheReportsCount(t *testing.T) {
	st := media.StreamStats{Frames: 2500, DecodeErrors: 40}
	rep := damagedPictures(sourceDamageReport{}, st, 0, 0, 0)
	if rep.Pictures != 40 || len(rep.Lines) != 1 || !strings.Contains(rep.Lines[0], "40 decode errors") {
		t.Errorf("report case: %+v, want 40 pictures and one line saying so", rep)
	}
	// 30 frames of 500 lost with no message: still damage, with the old line.
	rep = damagedPictures(sourceDamageReport{}, media.StreamStats{Frames: 470}, 500, 500, 470)
	if rep.Pictures != 30 || !strings.Contains(strings.Join(rep.Lines, "\n"), "30 of 500 frames were not decoded") {
		t.Errorf("lost-frames case: %+v, want 30 pictures and the old lost-frames line", rep)
	}
	// The text's own count stays when it is larger (a picture line ffmpeg did not count as a decode error).
	rep = damagedPictures(sourceDamageReport{Pictures: 54}, st, 0, 0, 0)
	if rep.Pictures != 54 {
		t.Errorf("text floor: %d pictures, want 54", rep.Pictures)
	}
}

// The encode's own report is the exact count of the original's pictures: the new file is checked against what
// the encode decoded, not the duration × rate guess.
func TestEncodeReportIsTheFrameCount(t *testing.T) {
	s := newScenario(t)
	s.outPackets = 90 // the decode's count; the guess (sourceSpec: 100 s × 25 fps) is 2500
	s.encodeStderr = inputReport(90, 90, 0)
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s (error %q), want done: the report's 90 frames are what the new file must match", j.Status, j.Error)
	}
}

// The report's decode errors are the damage the job judges: forty of 2500 frames (1.6%) is kept, where the same
// stderr with no report line has no damage text and converts.
func TestEncodeReportIsTheDamage(t *testing.T) {
	s := newScenario(t)
	s.encodeStderr = inputReport(2500, 2500, 40)
	s.run(Replace)
	s.assertKept("the original file has errors")
	for _, want := range []string{
		"Errors: 40 damaged pictures of 2500 frames (1.60%); up to 1% are tolerated",
		"40 decode errors in the original's video, as ffmpeg counted them",
	} {
		if !s.logHas(1, "check", want) {
			t.Errorf("check log lacks %q", want)
		}
	}
}

// A log with no report line keeps today's check: the probe's count is what the new file must match.
func TestNoReportKeepsTheOldFrameCheck(t *testing.T) {
	s := newScenario(t)
	s.srcPackets, s.outPackets = 2500, 2400 // no report line, no damage text: today's count decides
	s.run(Replace)
	s.assertKept("the new file has 2400 video frames but the original has 2500")
}

// A steady-rate original whose decode took fewer frames than its rate × duration did lose them: the report's
// count is the evidence, so it is kept as damaged (a damaged .ts: 470 of 500 decoded, no message).
func TestEncodeReportKeepsFramesTheStreamLost(t *testing.T) {
	s := newScenario(t)
	s.srcSpec.streams[0]["duration"] = "100.000000" // the stream states its own count: 100 s × 25 fps = 2500
	s.srcPackets, s.outPackets = 2500, 2450
	s.encodeStderr = inputReport(2450, 2450, 0)
	s.run(Replace)
	s.assertKept("the original file has errors")
	for _, want := range []string{
		"50 of 2500 frames were not decoded",
		"Errors: 50 damaged pictures of 2450 frames (2.04%); up to 1% are tolerated",
	} {
		if !s.logHas(1, "check", want) {
			t.Errorf("check log lacks %q:\n%s", want, logTextOf(s.store, s.id))
		}
	}
}

// A variable-rate original is not kept for the frames its rate × duration guesses: its report's decoded count
// is short of that guess by design, and with no decode errors that is no damage.
func TestAVFRSourceIsNotKeptForItsShortCount(t *testing.T) {
	s := newScenario(t)
	s.srcSpec.streams[0]["duration"] = "110.000000" // 110 s × 20 (the average rate) = 2200 guessed, 2000 decoded
	s.srcSpec.streams[0]["r_frame_rate"] = "30/1"
	s.srcSpec.streams[0]["avg_frame_rate"] = "20/1" // more than 1% apart: the probe marks it VFR
	s.srcPackets, s.outPackets = 2200, 2000
	s.encodeStderr = inputReport(2000, 2000, 0)
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s (error %q), want done: a VFR original's short count is not damage", j.Status, j.Error)
	}
	if !s.logHas(0, "app", "variable frame rate") {
		t.Error("want the variable frame rate line in the job log")
	}
}

// A VFR original without a report has no count to check: the frame check is skipped with a line, not run
// against the wrong duration × rate guess.
func TestAVFRSourceWithoutAReportSkipsTheFrameCheck(t *testing.T) {
	s := newScenario(t)
	s.srcSpec.streams[0]["r_frame_rate"] = "30/1"
	s.srcSpec.streams[0]["avg_frame_rate"] = "20/1" // more than 1% apart: the probe marks it VFR
	s.srcPackets, s.outPackets = 2500, 2000         // the old check would judge 2000 against 2500
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s (error %q), want done: a VFR original's frame check is skipped without a report", j.Status, j.Error)
	}
	if !s.logHas(0, "app", "variable frame rate and the conversion did not report its frame count, so the frame count is not checked") {
		t.Error("want the skip line in the job log")
	}
}
