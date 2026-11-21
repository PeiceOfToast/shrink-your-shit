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

// VFR is marked from the probe's own rates, or from a report's short count whose frames do not follow the
// stream's nominal interval. A short count from frames that do keep it is a silent skip: loss, not a rate.
func TestSourceVFRWhenTheProbeMarkedItOrTheShortCountIsNotLoss(t *testing.T) {
	cfr := media.Info{DurationSec: 100, Streams: []media.Stream{{Index: 0, Type: "video", FrameRate: 25}}}
	vfrProbe := media.Info{DurationSec: 100, Streams: []media.Stream{{Index: 0, Type: "video", FrameRate: 25, VFR: true}}}
	for _, c := range []struct {
		name           string
		src            media.Info
		short, follows bool
		want           bool
	}{
		{"probe mark", vfrProbe, false, true, true},
		{"short count, frames off the nominal rate", cfr, true, false, true},
		{"short count, frames keep the nominal rate (a silent skip)", cfr, true, true, false},
		{"not short", cfr, false, true, false},
		{"no video", media.Info{}, false, true, false},
	} {
		if got := sourceVFR(c.src, c.follows, c.short); got != c.want {
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
	rep = damagedPictures(sourceDamageReport{}, media.StreamStats{Packets: 500, Frames: 470}, 30, 500, 470)
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

// A steady-rate original whose decode took fewer frames than it read did lose them: the report's packets and
// decoded counts are the evidence, so it is kept as damaged (a damaged .ts: 500 packets read, 470 decoded, no
// message). The damage share is of the frames the stream had, not of the decoded count (review-588 round 2).
func TestEncodeReportKeepsFramesTheStreamLost(t *testing.T) {
	s := newScenario(t)
	s.srcSpec.streams[0]["duration"] = "100.000000" // the stream states its own count: 100 s × 25 fps = 2500
	s.srcPackets, s.outPackets = 2500, 2450
	s.encodeStderr = inputReport(2500, 2450, 0)
	s.run(Replace)
	s.assertKept("the original file has errors")
	for _, want := range []string{
		"50 of 2500 frames were not decoded",
		"Errors: 50 damaged pictures of 2500 frames (2.00%); up to 1% are tolerated",
	} {
		if !s.logHas(1, "check", want) {
			t.Errorf("check log lacks %q:\n%s", want, logTextOf(s.store, s.id))
		}
	}
}

// An mkv whose header states a steady rate and a duration its timestamps do not keep: the probe's average and
// nominal rate agree (ffmpeg 8: they do), so the probe cannot mark it VFR; the stream read exactly what it
// decoded, so no packets were skipped. Only the frames' spacing says it: they do not sit at the declared
// interval, so the short count is its rate, not loss, and it converts (review-588 rounds 2 and 3).
func TestAnOriginalWhoseFramesDoNotFollowItsRateIsNotKept(t *testing.T) {
	s := newScenario(t)
	s.srcSpec.streams[0]["duration"] = "130.000000" // 130 s × 20 = 2600 guessed, 2000 decoded
	s.srcSpec.streams[0]["r_frame_rate"] = "20/1"
	s.srcSpec.streams[0]["avg_frame_rate"] = "20/1" // equal: the probe sees a steady rate
	s.srcPackets, s.outPackets = 2600, 2000
	s.encodeStderr = inputReport(2000, 2000, 0) // read 2000 packets, decoded 2000: nothing skipped
	s.srcPts = ptsLines(2000, 0.065)            // 0.05 s is the nominal interval; these frames are not at it
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s (error %q), want done: the frames do not follow the rate, so the short count is not loss", j.Status, j.Error)
	}
	if !s.logHas(0, "app", "variable frame rate") {
		t.Error("want the variable frame rate line in the job log")
	}
}

// A demuxer that silently skipped packets: the encode reads exactly what it decodes (2400 of the 2500 frames
// 100 s × 25 has), no error is printed, the output keeps the full length, and the probe's rates agree. Every
// count says "fine" — but the remaining frames still sit at the 25 fps interval, with gaps where the skipped
// ones were, so 100 frames are lost: 4%, over tolerance, and the original is kept (review-588 round 3).
func TestASilentlySkippedStreamIsKept(t *testing.T) {
	s := newScenario(t)
	s.srcSpec.streams[0]["duration"] = "100.000000" // 100 s × 25 = 2500
	s.srcPackets, s.outPackets = 2500, 2400
	s.encodeStderr = inputReport(2400, 2400, 0) // read 2400, decoded 2400: the 100 were never read
	s.srcPts = ptsSkipped(2400, 25, 0.04)       // the frames that remain keep the 25 fps interval
	s.run(Replace)
	s.assertKept("the original file has errors")
	for _, want := range []string{
		"100 of 2500 frames were not decoded",
		"Errors: 100 damaged pictures of 2500 frames (4.00%); up to 1% are tolerated",
	} {
		if !s.logHas(1, "check", want) {
			t.Errorf("check log lacks %q:\n%s", want, logTextOf(s.store, s.id))
		}
	}
}

// Five damaged pictures of the 500 frames the stream had, which read 500 packets and decoded 498: exactly 1%,
// so it converts. The lost frames are among the five, so the share is of the frames the stream had, not of the
// 498 decoded (review-588 round 2, F2: shrinking it counted the lost frames twice).
func TestLightlyDamagedOriginalWithLostFramesConverts(t *testing.T) {
	s := newScenario(t)
	s.srcSpec.streams[0]["duration"] = "20.000000" // 20 s × 25 = 500
	s.srcPackets, s.outPackets = 500, 498
	s.encodeStderr = inputReport(500, 498, 0) + strings.Repeat("[h264 @ 0x1] error while decoding MB 1 2\n", 5)
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s (error %q), want done: 5 pictures of 500 frames is 1%%", j.Status, j.Error)
	}
	if !s.logHas(0, "app", "5 damaged pictures of 500 frames (1.00%)") {
		t.Errorf("want the damage share of the 500 frames the stream had:\n%s", logTextOf(s.store, s.id))
	}
}

// A variable-rate original is not kept for the frames its rate × duration guesses: its report's decoded count
// is far short of that guess by design (a third), and with no decode errors that is no damage.
func TestAVFRSourceIsNotKeptForItsShortCount(t *testing.T) {
	s := newScenario(t)
	s.srcSpec.streams[0]["duration"] = "130.000000" // 130 s × 20 (the average rate) = 2600 guessed, 2000 decoded
	s.srcSpec.streams[0]["r_frame_rate"] = "30/1"
	s.srcSpec.streams[0]["avg_frame_rate"] = "20/1" // more than 1% apart: the probe marks it VFR
	s.srcPackets, s.outPackets = 2600, 2000
	s.encodeStderr = inputReport(2000, 2000, 0)
	s.srcPts = ptsLines(2000, 0.07) // the frames are not at the 30 fps interval either
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s (error %q), want done: a VFR original's short count is not damage", j.Status, j.Error)
	}
	if !s.logHas(0, "app", "variable frame rate") {
		t.Error("want the variable frame rate line in the job log")
	}
}

// A probe mismatch of 1% to 3% on its own does not explain a short count: a steady file can lose frames and be
// marked variable rate anyway, so its lost frames still keep it (review-588 round 1). On the first head this
// converted, because the mark alone dropped the evidence.
func TestASmallRateMismatchDoesNotDropLostFrames(t *testing.T) {
	s := newScenario(t)
	s.srcSpec.streams[0]["duration"] = "100.000000" // 100 s × 25.5 (the average rate) = 2550
	s.srcSpec.streams[0]["r_frame_rate"] = "25/1"
	s.srcSpec.streams[0]["avg_frame_rate"] = "255/10" // 2% apart: the probe marks it VFR
	s.srcPackets, s.outPackets = 2550, 2500
	s.encodeStderr = inputReport(2550, 2500, 0) // 50 packets read and not decoded, no error text
	s.run(Replace)
	s.assertKept("the original file has errors")
	for _, want := range []string{
		"50 of 2550 frames were not decoded",
		"Errors: 50 damaged pictures of 2550 frames (1.96%); up to 1% are tolerated",
	} {
		if !s.logHas(1, "check", want) {
			t.Errorf("check log lacks %q:\n%s", want, logTextOf(s.store, s.id))
		}
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
