// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media/fake"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// loudnessFlow is the starter with the audio step converting to stereo AAC and evening out
// loudness: the settings a measured pass needs.
func loudnessFlow(t *testing.T) flow.Flow {
	t.Helper()
	rev := starterFlow(t)
	rev.Steps[1].Settings = json.RawMessage(`{"format":"aac","channels":"stereo","loudnorm":true}`)
	return rev
}

// loudnessSource is one 5.1 E-AC-3 track with a known rate: the measurement needs the rate to put
// the track back after loudnorm.
func loudnessSource() []media.AudioTrack {
	return []media.AudioTrack{{Index: 1, Codec: "eac3", Language: "eng", Channels: 6, SampleRate: 48000, BitRate: 640_000}}
}

func loudnessReport(i, tp, lra, thresh, offset string) []string {
	return []string{
		"[Parsed_loudnorm_0 @ 0x55] ",
		"{",
		`	"input_i" : "` + i + `",`,
		`	"input_tp" : "` + tp + `",`,
		`	"input_lra" : "` + lra + `",`,
		`	"input_thresh" : "` + thresh + `",`,
		`	"target_offset" : "` + offset + `"`,
		"}",
	}
}

// TestRunMeasuresLoudnessThenEncodes is L-AUD-15's wiring: the analysis runs on the track first,
// and the numbers it read reach the encode's loudnorm filter.
func TestRunMeasuresLoudnessThenEncodes(t *testing.T) {
	root, src, info := libraryFile(t, "clip.mkv", "h264")
	info.Audio = loudnessSource()
	want := filepath.Join(root, "clip-shrunk.mkv")
	m := &fake.Media{
		Probes: map[string]media.Info{src: info, want: info},
		Runs: []fake.RunScript{
			{Result: media.Result{KeyLines: loudnessReport("-23.45", "-2.10", "7.30", "-33.46", "0.02")}},
			{},
		},
	}
	bus := &recorder{}
	res := flow.Run(context.Background(), runInput(t, root, src), newEnv(m, &fakeSafety{verify: safety.Verdict{OK: true}}, bus, loudnessFlow(t), new(bytes.Buffer)))
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("outcome = %q (%s: %s), want smaller", res.Outcome, res.Reason, res.Message)
	}

	argvs := m.Argvs()
	if len(argvs) != 2 {
		t.Fatalf("ffmpeg calls = %d, want the measurement and the encode", len(argvs))
	}
	if got, want := strings.Join(argvs[0], " "), strings.Join(encode.LoudnessArgs(src, 1), " "); got != want {
		t.Errorf("first call = %q, want %q", got, want)
	}
	filter, has := flagValue(argvs[1], "-filter:a:0")
	if !has {
		t.Fatalf("encode argv = %v, want the measured loudnorm filter", argvs[1])
	}
	for _, part := range []string{"measured_I=-23.45", "measured_TP=-2.10", "measured_LRA=7.30", "linear=true"} {
		if !strings.Contains(filter, part) {
			t.Errorf("filter = %q, want %q", filter, part)
		}
	}
}

// TestRunRefusesSilentLoudness: -inf is silence's loudness, and a linear pass over it would fail,
// so the track is left as it is and the log says why.
func TestRunRefusesSilentLoudness(t *testing.T) {
	root, src, info := libraryFile(t, "clip.mkv", "h264")
	info.Audio = loudnessSource()
	want := filepath.Join(root, "clip-shrunk.mkv")
	m := &fake.Media{
		Probes: map[string]media.Info{src: info, want: info},
		Runs: []fake.RunScript{
			{Result: media.Result{KeyLines: loudnessReport("-inf", "-inf", "0.00", "-inf", "-inf")}},
			{},
		},
	}
	bus := &recorder{}
	res := flow.Run(context.Background(), runInput(t, root, src), newEnv(m, &fakeSafety{verify: safety.Verdict{OK: true}}, bus, loudnessFlow(t), new(bytes.Buffer)))
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("outcome = %q (%s: %s), want smaller", res.Outcome, res.Reason, res.Message)
	}
	argvs := m.Argvs()
	if len(argvs) != 2 {
		t.Fatalf("ffmpeg calls = %d, want the measurement and the encode", len(argvs))
	}
	if filter, has := flagValue(argvs[1], "-filter:a:0"); has && strings.Contains(filter, "loudnorm") {
		t.Errorf("filter = %q, want no loudness pass over a silent track", filter)
	}
	if !warned(bus.all(), "A track's loudness could not be measured, so it is kept as it is.") {
		t.Errorf("events = %v, want the could-not-be-measured warning", frameKinds(bus.all()))
	}
}

// TestRunSkipsLoudnessWhenTheTimeIsUp: the ten-minute cap is checked before each track, so a run
// that is already past it does not measure at all, logs once, and encodes without a loudness pass.
func TestRunSkipsLoudnessWhenTheTimeIsUp(t *testing.T) {
	root, src, info := libraryFile(t, "clip.mkv", "h264")
	info.Audio = loudnessSource()
	want := filepath.Join(root, "clip-shrunk.mkv")
	m := &fake.Media{
		Probes: map[string]media.Info{src: info, want: info},
		Runs:   []fake.RunScript{{}, {}},
	}
	bus := &recorder{}
	env := newEnv(m, &fakeSafety{verify: safety.Verdict{OK: true}}, bus, loudnessFlow(t), new(bytes.Buffer))
	// The clock moves past the ten-minute budget between two reads, so the cap is already exceeded
	// when the measuring pass starts.
	n := 0
	base := time.Unix(0, 0)
	env.Now = func() time.Time {
		n++
		return base.Add(time.Duration(n) * (loudnessBudgetForTest + time.Minute))
	}
	res := flow.Run(context.Background(), runInput(t, root, src), env)
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("outcome = %q (%s: %s), want smaller", res.Outcome, res.Reason, res.Message)
	}
	argvs := m.Argvs()
	if len(argvs) != 1 {
		t.Fatalf("ffmpeg calls = %d, want only the encode: the analysis was skipped", len(argvs))
	}
	if filter, has := flagValue(argvs[0], "-filter:a:0"); has && strings.Contains(filter, "loudnorm") {
		t.Errorf("filter = %q, want no loudness pass past the budget", filter)
	}
	if !warned(bus.all(), "Evening out loudness took too long, so no track's loudness is changed.") {
		t.Errorf("events = %v, want the took-too-long warning", frameKinds(bus.all()))
	}
}

// loudnessBudgetForTest repeats the engine's ten-minute cap, so the test's clock outruns it.
const loudnessBudgetForTest = 10 * time.Minute

// flagValue is the value that follows flag in argv, if both are there.
func flagValue(argv []string, flag string) (string, bool) {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag {
			return argv[i+1], true
		}
	}
	return "", false
}

// warned reports whether a warning with exactly this sentence was logged.
func warned(evs []events.Event, message string) bool {
	for _, e := range evs {
		if e.Kind == events.KindWarning && e.Level == events.LevelWarn && e.Message == message {
			return true
		}
	}
	return false
}
