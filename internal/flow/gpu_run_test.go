// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media/fake"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// The ladder in a real run (L-HW-5, L-HW-7, L-HW-9): a card rung that fails on the encoder's
// account hands the staged file to the next rung; the other failures end the run where they are;
// ifGpuFails: stop never reaches the processor; and every rung's output is checked the same way.

const qsvLine = "Encoding on Intel Quick Sync (hevc_qsv); the card decodes the picture too."

// fullRung is the card decoding too; hybridRung is the card encoding frames the processor decoded.
func fullRung() encode.Rung {
	return encode.Rung{Encoder: "hevc_qsv", Family: encode.FamilyQSV, Decode: encode.DecodeHardware}
}

func hybridRung() encode.Rung {
	return encode.Rung{Encoder: "hevc_qsv", Family: encode.FamilyQSV, Decode: encode.DecodeSoftware}
}

// pickRungs is a picker that answers with these card rungs.
func pickRungs(rungs ...encode.Rung) func(flow.Want) (flow.Attempt, error) {
	return func(flow.Want) (flow.Attempt, error) {
		return flow.Attempt{Rungs: rungs, Line: qsvLine}, nil
	}
}

// pickReason is a picker that offers no card and says why.
func pickReason(reason string) func(flow.Want) (flow.Attempt, error) {
	return func(flow.Want) (flow.Attempt, error) { return flow.Attempt{Reason: reason}, nil }
}

// failRun is an encode that ended with these key lines. `has no usable` etc. are left to the
// classifier; the error's own text is what the fatal message shows.
func failRun(lines ...string) fake.RunScript {
	return fake.RunScript{
		Result: media.Result{ExitCode: 1, KeyLines: lines},
		Err:    fmt.Errorf("run ffmpeg: ffmpeg exited with code 1: %s", strings.Join(lines, "\n")),
	}
}

// ladderRun runs the starter flow on one 1080p H.264 file with a picker and scripted encodes.
func ladderRun(t *testing.T, videoSettings string, pick func(flow.Want) (flow.Attempt, error), runs []fake.RunScript) (flow.Result, *fake.Media, *fakeSafety, *recorder) {
	t.Helper()
	root, src, info := libraryFile(t, "clip.mkv", "h264")
	want := filepath.Join(root, "clip-shrunk.mkv")
	m := &fake.Media{Probes: map[string]media.Info{src: info, want: info}, Runs: runs}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	bus := &recorder{}
	rev := starterFlow(t)
	if videoSettings != "" {
		rev.Steps[0].Settings = json.RawMessage(videoSettings)
	}
	env := newEnv(m, sf, bus, rev, &bytes.Buffer{})
	if pick != nil {
		env.Pick = pick
	}
	return flow.Run(context.Background(), runInput(t, root, src), env), m, sf, bus
}

// said is every log message the run wrote, of the kinds the ladder speaks in.
func said(bus *recorder, kinds ...events.Kind) []string {
	var out []string
	for _, e := range bus.all() {
		for _, k := range kinds {
			if e.Kind == k {
				out = append(out, e.Message)
			}
		}
	}
	return out
}

// TestTheLadderStepsDownFullThenHybridThenProcessor is L-HW-5's whole descent: both card rungs
// fail for the encoder, the processor converts the file, and the log says what happened at each
// step with L-HW-7's words.
func TestTheLadderStepsDownFullThenHybridThenProcessor(t *testing.T) {
	res, m, sf, bus := ladderRun(t, "", pickRungs(fullRung(), hybridRung()), []fake.RunScript{
		failRun("[hevc_qsv @ 0x55] Error creating a MFX session: -9."),
		failRun("[hevc_qsv @ 0x55] Error creating a MFX session: -9."),
		{},
	})
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("outcome = %q (%s: %s), want smaller", res.Outcome, res.Reason, res.Message)
	}
	argvs := m.Argvs()
	if len(argvs) != 3 {
		t.Fatalf("ran %d encodes, want full, hybrid and processor", len(argvs))
	}
	full, hybrid, processor := strings.Join(argvs[0], " "), strings.Join(argvs[1], " "), strings.Join(argvs[2], " ")
	if !strings.Contains(full, "-hwaccel qsv") || !strings.Contains(full, "hevc_qsv") {
		t.Errorf("first rung = %v, want the card decoding and encoding", argvs[0])
	}
	if strings.Contains(hybrid, "-hwaccel") || !strings.Contains(hybrid, "hevc_qsv") {
		t.Errorf("second rung = %v, want the hybrid (processor decodes, card encodes)", argvs[1])
	}
	if !strings.Contains(processor, "libx265") {
		t.Errorf("third rung = %v, want the processor", argvs[2])
	}
	want := []string{
		qsvLine,
		"The full graphics pipeline failed ([hevc_qsv @ 0x55] Error creating a MFX session: -9); the processor decodes for hevc_qsv.",
		"Intel Quick Sync (hevc_qsv) failed: [hevc_qsv @ 0x55] Error creating a MFX session: -9. Converted on the processor instead.",
	}
	if got := said(bus, events.KindNote, events.KindWarning); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the ladder's log lines =\n%q\nwant\n%q", got, want)
	}
	// #861: the result check runs on the processor's output with the plan's picture, exactly as it
	// would after a run that never touched a card.
	if !sf.gotWant.Video.Compare || sf.gotWant.Video.Width != 1920 || sf.gotWant.Video.Height != 1080 {
		t.Errorf("verify want = %+v, want the plan's 1920×1080 picture", sf.gotWant.Video)
	}
}

// TestTheLadderNeverGoesBackUp: once a rung failed, only the rungs below it run. The full pipeline
// failing does not make the ladder try it again after the processor.
func TestTheLadderNeverGoesBackUp(t *testing.T) {
	res, m, _, _ := ladderRun(t, "", pickRungs(fullRung(), hybridRung()), []fake.RunScript{
		failRun("Unknown encoder 'hevc_qsv'"),
		failRun("Unknown encoder 'hevc_qsv'"),
		failRun("some new failure"),
	})
	if res.Outcome != flow.OutcomeFatal || res.Reason != "encode.failed" {
		t.Fatalf("result = %+v, want the processor's own failure", res)
	}
	argvs := m.Argvs()
	if len(argvs) != 3 {
		t.Fatalf("ran %d encodes, want exactly three: full, hybrid, processor", len(argvs))
	}
	if !strings.Contains(strings.Join(argvs[2], " "), "libx265") {
		t.Errorf("third rung = %v, want the processor", argvs[2])
	}
}

// TestTheLadderTruncatesTheStagedFileBetweenRungs: the next rung writes into the same file,
// emptied, not into a new one (L-HW-5).
func TestTheLadderTruncatesTheStagedFileBetweenRungs(t *testing.T) {
	root, src, info := libraryFile(t, "clip.mkv", "h264")
	want := filepath.Join(root, "clip-shrunk.mkv")
	m := &writingMedia{
		Media: fake.Media{Probes: map[string]media.Info{src: info, want: info}, Runs: []fake.RunScript{
			failRun("Unknown encoder 'hevc_qsv'"),
			{},
		}},
		write: 300,
	}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	env := newEnv(m, sf, &recorder{}, starterFlow(t), &bytes.Buffer{})
	env.Pick = pickRungs(hybridRung())
	res := flow.Run(context.Background(), runInput(t, root, src), env)

	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("outcome = %q (%s: %s), want smaller", res.Outcome, res.Reason, res.Message)
	}
	if len(m.sizes) != 2 {
		t.Fatalf("saw %d rung starts, want two", len(m.sizes))
	}
	if m.sizes[0] == 0 {
		t.Errorf("the first rung saw an empty file; want the staged file Stage made")
	}
	if m.sizes[1] != 0 {
		t.Errorf("the second rung saw %d bytes, want the partial file truncated", m.sizes[1])
	}
}

// TestTheLadderNeverStepsDownForTheProcessorsOwnFailures is L-HW-5's never list, on the hybrid
// rung: these failures end the run where they are, with no processor attempt after them.
func TestTheLadderNeverStepsDownForTheProcessorsOwnFailures(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
	}{
		{"a full disk", []string{"av_interleaved_write_frame(): No space left on device"}},
		{"refused settings", []string{"[out#0/mp4] Could not write header (incorrect codec parameters ?): Invalid argument",
			"Stream #0:1: not supported for mp4"}},
		{"an unwritable output", []string{"[out#0/mkv] Error opening output out.mkv: Permission denied"}},
		{"a missing source", []string{"[in#0 @ 0x55] Error opening input: No such file or directory"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, m, _, _ := ladderRun(t, "", pickRungs(hybridRung()), []fake.RunScript{
				failRun(c.lines...),
				{},
			})
			if res.Outcome != flow.OutcomeFatal || res.Reason != "encode.failed" {
				t.Fatalf("result = %+v, want the card rung's own failure", res)
			}
			if n := len(m.Argvs()); n != 1 {
				t.Errorf("ran %d encodes, want one: the processor would hit this too", n)
			}
		})
	}
}

// TestTheFullRungStepsDownForAnyFailureButTheProcessorsOwn: the card decodes there, so a decode
// error can be the card's. A damaged source still steps down; a full disk, a refused write, a
// missing source or refused settings does not.
func TestTheFullRungStepsDownForAnyFailureButTheProcessorsOwn(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		steps bool
	}{
		{"the card cannot decode the file", []string{"[h264_qsv @ 0x55] Error while decoding stream #0:0: Invalid argument"}, true},
		{"a damaged source", []string{"[h264 @ 0x55] decode_slice_header error"}, true},
		{"a full disk", []string{"No space left on device"}, false},
		{"refused settings", []string{"Quality-based encoding not supported"}, false},
		{"an unwritable output", []string{"Permission denied"}, false},
		{"a missing source", []string{"No such file or directory"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, m, _, _ := ladderRun(t, "", pickRungs(fullRung()), []fake.RunScript{
				failRun(c.lines...),
				{},
			})
			if c.steps {
				if res.Outcome != flow.OutcomeSmaller {
					t.Fatalf("outcome = %q (%s: %s), want the next rung to convert it", res.Outcome, res.Reason, res.Message)
				}
				if n := len(m.Argvs()); n != 2 {
					t.Errorf("ran %d encodes, want the full rung then the processor", n)
				}
				return
			}
			if res.Outcome != flow.OutcomeFatal {
				t.Fatalf("outcome = %q, want a failure the ladder does not move past", res.Outcome)
			}
			if n := len(m.Argvs()); n != 1 {
				t.Errorf("ran %d encodes, want one: the processor would hit this too", n)
			}
		})
	}
}

// TestTheHybridRungStepsDownOnlyForEncoderFailures: off the full rung only an encoder or ffmpeg
// failure steps down; a damaged source is not blamed on the card.
func TestTheHybridRungStepsDownOnlyForEncoderFailures(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		steps bool
	}{
		{"an encoder that cannot run", []string{"Unknown encoder 'hevc_qsv'"}, true},
		{"an encode that failed for no coded reason", []string{"some new failure"}, true},
		{"a damaged source", []string{"Invalid data found when processing input"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, m, _, _ := ladderRun(t, "", pickRungs(hybridRung()), []fake.RunScript{failRun(c.lines...), {}})
			if c.steps {
				if res.Outcome != flow.OutcomeSmaller {
					t.Fatalf("outcome = %q (%s: %s), want the processor to convert it", res.Outcome, res.Reason, res.Message)
				}
				if n := len(m.Argvs()); n != 2 {
					t.Errorf("ran %d encodes, want the hybrid then the processor", n)
				}
				return
			}
			if res.Outcome != flow.OutcomeFatal {
				t.Fatalf("outcome = %q, want the damaged-source failure", res.Outcome)
			}
			if n := len(m.Argvs()); n != 1 {
				t.Errorf("ran %d encodes, want one: the card is not blamed for a damaged source", n)
			}
		})
	}
}

// TestStopModeNeverReachesTheProcessor: ifGpuFails: stop ends the run with plain words and the
// original untouched (L-HW-5).
func TestStopModeNeverReachesTheProcessor(t *testing.T) {
	stop := `{"format":"hevc","quality":3,"useGpu":true,"ifGpuFails":"stop"}`
	res, m, sf, _ := ladderRun(t, stop, pickRungs(hybridRung()), []fake.RunScript{
		failRun("[hevc_qsv @ 0x55] Error creating a MFX session: -9."),
		{},
	})
	if res.Outcome != flow.OutcomeFatal || res.Reason != "encode.gpu_stopped" {
		t.Fatalf("result = %+v, want the stop", res)
	}
	want := "Intel Quick Sync (hevc_qsv) failed: [hevc_qsv @ 0x55] Error creating a MFX session: -9. " +
		"This step stops instead of using the processor, so the original was left as it is."
	if res.Message != want {
		t.Errorf("message = %q, want %q", res.Message, want)
	}
	if n := len(m.Argvs()); n != 1 {
		t.Errorf("ran %d encodes, want one: stop never reaches the processor", n)
	}
	if sf.placedTo != "" {
		t.Errorf("placed the new file at %q; want the original left as it is", sf.placedTo)
	}
}

// TestStopModeStillStepsDownOnTheCard: stopping means never using the processor, not giving up
// after the first card rung — the hybrid rung is still the same card (L-HW-5).
func TestStopModeStillStepsDownOnTheCard(t *testing.T) {
	stop := `{"format":"hevc","quality":3,"useGpu":true,"ifGpuFails":"stop"}`
	res, m, _, bus := ladderRun(t, stop, pickRungs(fullRung(), hybridRung()), []fake.RunScript{
		failRun("Unknown encoder 'hevc_qsv'"),
		{},
	})
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("outcome = %q (%s: %s), want the hybrid rung to convert it", res.Outcome, res.Reason, res.Message)
	}
	if n := len(m.Argvs()); n != 2 {
		t.Errorf("ran %d encodes, want the full rung then the hybrid", n)
	}
	for _, line := range said(bus, events.KindWarning) {
		if strings.Contains(line, "processor instead") {
			t.Errorf("the log says %q; the hybrid rung is still the card, not the processor", line)
		}
	}
}

// TestStopModeWithNoCardLeavesTheOriginalAlone: a step set to stop does not convert on the
// processor when no card can take the file, and says why (L-HW-9).
func TestStopModeWithNoCardLeavesTheOriginalAlone(t *testing.T) {
	stop := `{"format":"hevc","quality":3,"useGpu":true,"ifGpuFails":"stop"}`
	res, m, sf, _ := ladderRun(t, stop, pickReason("Intel Quick Sync (hevc_qsv) did not pass the 10-bit test"), nil)
	if res.Outcome != flow.OutcomeFatal || res.Reason != "encode.gpu_unavailable" {
		t.Fatalf("result = %+v, want the no-card stop", res)
	}
	want := "No graphics card can convert this file: Intel Quick Sync (hevc_qsv) did not pass the 10-bit test. " +
		"This step stops instead of using the processor, so the original was left as it is."
	if res.Message != want {
		t.Errorf("message = %q, want %q", res.Message, want)
	}
	if n := len(m.Argvs()); n != 0 {
		t.Errorf("ran %d encodes, want none", n)
	}
	if sf.placedTo != "" {
		t.Errorf("placed the new file at %q; want nothing placed", sf.placedTo)
	}
}

// TestTheProcessorReasonIsInTheLog is L-HW-9: a job the card cannot take converts on the
// processor, and the job log says why.
func TestTheProcessorReasonIsInTheLog(t *testing.T) {
	res, m, _, bus := ladderRun(t, "", pickReason("the graphics card has not been tested yet"), []fake.RunScript{{}})
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("outcome = %q (%s: %s), want smaller", res.Outcome, res.Reason, res.Message)
	}
	if n := len(m.Argvs()); n != 1 || !strings.Contains(strings.Join(m.Argvs()[0], " "), "libx265") {
		t.Fatalf("ran %v, want one encode on the processor", m.Argvs())
	}
	want := "Converting on the processor: the graphics card has not been tested yet."
	if got := said(bus, events.KindWarning); len(got) != 1 || got[0] != want {
		t.Errorf("warnings = %q, want %q", got, want)
	}
}

// TestTheResultCheckRunsOnACardOutput: the #861 check reads a card's output exactly as it reads
// the processor's — the same picture, audio and subtitles wanted from it.
func TestTheResultCheckRunsOnACardOutput(t *testing.T) {
	res, m, sf, _ := ladderRun(t, "", pickRungs(hybridRung()), []fake.RunScript{{}})
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("outcome = %q (%s: %s), want smaller", res.Outcome, res.Reason, res.Message)
	}
	if n := len(m.Argvs()); n != 1 || !strings.Contains(strings.Join(m.Argvs()[0], " "), "hevc_qsv") {
		t.Fatalf("ran %v, want one encode on the card", m.Argvs())
	}
	if !sf.gotWant.Video.Compare || sf.gotWant.Video.Width != 1920 || sf.gotWant.Video.Height != 1080 {
		t.Errorf("verify want = %+v, want the plan's 1920×1080 picture", sf.gotWant.Video)
	}
	if len(sf.gotWant.Audio) != 2 || sf.gotWant.Audio[0].Language != "eng" || sf.gotWant.Audio[1].Language != "ger" {
		t.Errorf("verify want audio = %+v, want eng and ger", sf.gotWant.Audio)
	}
	if len(sf.gotWant.SubtitleLanguages) != 1 || sf.gotWant.SubtitleLanguages[0] != "eng" {
		t.Errorf("verify want subtitles = %v, want eng", sf.gotWant.SubtitleLanguages)
	}
}

// TestUseGpuOffSkipsThePicker: a step whose graphics card choice is off never asks the picker, and
// the job runs on the processor.
func TestUseGpuOffSkipsThePicker(t *testing.T) {
	off := `{"format":"hevc","quality":3,"useGpu":false,"ifGpuFails":"cpu"}`
	calls := 0
	pick := func(flow.Want) (flow.Attempt, error) {
		calls++
		return flow.Attempt{Rungs: []encode.Rung{hybridRung()}, Line: qsvLine}, nil
	}
	res, m, _, _ := ladderRun(t, off, pick, []fake.RunScript{{}})
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("outcome = %q (%s: %s), want smaller", res.Outcome, res.Reason, res.Message)
	}
	if calls != 0 {
		t.Errorf("the picker was asked %d times; want never with useGpu off", calls)
	}
	if n := len(m.Argvs()); n != 1 || !strings.Contains(strings.Join(m.Argvs()[0], " "), "libx265") {
		t.Errorf("ran %v, want one encode on the processor", m.Argvs())
	}
}

// TestACardRungBuildRefusesIsSkipped: a rung Build cannot make (the plan needs the processor's
// frames) is passed over with a note, and the next rung still converts the file (L-HW-11).
func TestACardRungBuildRefusesIsSkipped(t *testing.T) {
	// A frame-rate change needs the processor's frames, so Build refuses the full pipeline.
	withFPS := `{"format":"hevc","quality":3,"useGpu":true,"ifGpuFails":"cpu","fps":30}`
	res, m, _, bus := ladderRun(t, withFPS, pickRungs(fullRung(), hybridRung()), []fake.RunScript{{}})
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("outcome = %q (%s: %s), want the hybrid rung to convert it", res.Outcome, res.Reason, res.Message)
	}
	if n := len(m.Argvs()); n != 1 {
		t.Errorf("ran %d encodes, want one: the full rung was skipped", n)
	}
	if got := strings.Join(said(bus, events.KindWarning), "\n"); !strings.Contains(got, "cannot run this file") {
		t.Errorf("warnings = %q, want the note about the skipped rung", got)
	}
}

// writingMedia is fake.Media that also leaves bytes in the file the command writes, the way a real
// encoder leaves a partial file behind; it records each rung's starting size.
type writingMedia struct {
	fake.Media
	write int
	sizes []int64
}

func (m *writingMedia) Run(ctx context.Context, argv []string, onProgress func(media.Progress)) (media.Result, error) {
	out := argv[len(argv)-1]
	if st, err := os.Stat(out); err == nil {
		m.sizes = append(m.sizes, st.Size())
	}
	if err := os.WriteFile(out, bytes.Repeat([]byte("x"), m.write), 0o600); err != nil {
		return media.Result{}, err
	}
	return m.Media.Run(ctx, argv, onProgress)
}
