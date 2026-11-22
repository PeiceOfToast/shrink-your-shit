// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// loudExec answers each Capture (one per measured track) with reply, and keeps the arguments of every call.
type loudExec struct {
	calls [][]string
	reply func(n int, ctx context.Context) (string, error)
}

func (x *loudExec) Output(context.Context, string, ...string) ([]byte, error) { return nil, nil }

func (x *loudExec) Stream(context.Context, func(string), string, ...string) (string, error) {
	return "", nil
}

func (x *loudExec) Capture(ctx context.Context, _ string, args ...string) ([]byte, []byte, error) {
	x.calls = append(x.calls, args)
	s, err := x.reply(len(x.calls)-1, ctx)
	return nil, []byte(s), err
}

// logQueue keeps the job-log lines the worker writes; the other Queue methods are never called here.
type logQueue struct {
	Queue
	lines []string
}

func (q *logQueue) AppendLogLine(_ int64, _ int, _, _, _, text string) error {
	q.lines = append(q.lines, text)
	return nil
}

// loudPrint is loudnorm's first-pass print for a track at about -31 LUFS.
const loudPrint = `[Parsed_loudnorm_0 @ 0x1]
{
	"input_i" : "-31.42",
	"input_tp" : "-6.03",
	"input_lra" : "4.10",
	"input_thresh" : "-41.65",
	"target_offset" : "0.07"
}
`

// twoAudioSource is a file with a video stream and two audio streams, at 48 and 44.1 kHz.
func twoAudioSource() media.Info {
	return media.Info{Streams: []media.Stream{
		{Index: 0, Type: "video"},
		{Index: 1, Type: "audio", SampleRate: 48000},
		{Index: 2, Type: "audio", SampleRate: 44100},
	}}
}

// twoConverted is a plan that converts both audio tracks.
func twoConverted() rules.Plan {
	return rules.Plan{EvenLoudness: true, Tracks: []rules.TrackOp{
		{Index: 1, Type: "audio", Encoder: "aac", Kbps: 128, Channels: 2},
		{Index: 2, Type: "audio", Encoder: "aac", Kbps: 128, Channels: 2},
	}}
}

func lines(q *logQueue, sub string) int {
	n := 0
	for _, l := range q.lines {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

// Each converted track is measured once, as its own audio-only pass, with the filter the encode will use, and the
// measured values and the source rate are set on it.
func TestMeasureLoudnessMeasuresEachConvertedTrack(t *testing.T) {
	x := &loudExec{reply: func(int, context.Context) (string, error) { return loudPrint, nil }}
	q := &logQueue{}
	w := &Worker{Exec: x, FFmpeg: "ffmpeg", Queue: q}
	got, err := w.measureLoudness(context.Background(), jobs.Job{ID: 7, Path: "m.mkv"}, twoAudioSource(), "/src/m.mkv", twoConverted())
	if err != nil {
		t.Fatal(err)
	}
	if len(x.calls) != 2 {
		t.Fatalf("%d measuring runs, want one per converted track", len(x.calls))
	}
	for k, want := range []struct {
		idx  string
		rate int
	}{{"0:1", 48000}, {"0:2", 44100}} {
		args := strings.Join(x.calls[k], " ")
		for _, must := range []string{"-map " + want.idx, "-vn", "-sn", "-nostats", "loudnorm=I=-24:TP=-2:LRA=7:print_format=json"} {
			if !strings.Contains(args, must) {
				t.Errorf("run %d: %q not in %s", k, must, args)
			}
		}
		m := got.Tracks[k].Loudness
		if m == nil || m.Rate != want.rate || m.I != -31.42 {
			t.Errorf("track %d: loudness %+v, want rate %d and -31.42 LUFS", k, m, want.rate)
		}
	}
	if lines(q, "measures") != 2 {
		t.Errorf("log %q: want a line per measured track", q.lines)
	}
}

// A track whose measure fails stays as it is, with a line saying so, and the next track is still measured.
func TestMeasureLoudnessFailureLeavesTheTrackAndGoesOn(t *testing.T) {
	x := &loudExec{reply: func(n int, _ context.Context) (string, error) {
		if n == 0 {
			return "ffmpeg: decode error", errors.New("exit status 1")
		}
		return loudPrint, nil
	}}
	q := &logQueue{}
	w := &Worker{Exec: x, FFmpeg: "ffmpeg", Queue: q}
	got, err := w.measureLoudness(context.Background(), jobs.Job{ID: 7, Path: "m.mkv"}, twoAudioSource(), "/src/m.mkv", twoConverted())
	if err != nil {
		t.Fatal(err)
	}
	if got.Tracks[0].Loudness != nil {
		t.Error("a failed track was measured")
	}
	if got.Tracks[1].Loudness == nil {
		t.Error("the next track was not measured after a failure")
	}
	if lines(q, "could not be measured") != 1 {
		t.Errorf("log %q: want one could-not-be-measured line", q.lines)
	}
}

// A silent track prints "-inf": no normalisation, and a line for the job log.
func TestMeasureLoudnessSilentTrackIsNotNormalised(t *testing.T) {
	silent := strings.Replace(loudPrint, `"-31.42"`, `"-inf"`, 1)
	x := &loudExec{reply: func(int, context.Context) (string, error) { return silent, nil }}
	q := &logQueue{}
	w := &Worker{Exec: x, FFmpeg: "ffmpeg", Queue: q}
	got, err := w.measureLoudness(context.Background(), jobs.Job{ID: 7, Path: "m.mkv"}, twoAudioSource(), "/src/m.mkv", twoConverted())
	if err != nil {
		t.Fatal(err)
	}
	if got.Tracks[0].Loudness != nil || got.Tracks[1].Loudness != nil {
		t.Error("a silent track was normalised")
	}
	if lines(q, "no readable loudness") != 2 {
		t.Errorf("log %q", q.lines)
	}
}

// A track with no known sample rate is not measured at all: the output could not keep its rate.
func TestMeasureLoudnessNeedsTheSourceRate(t *testing.T) {
	x := &loudExec{reply: func(int, context.Context) (string, error) { return loudPrint, nil }}
	w := &Worker{Exec: x, FFmpeg: "ffmpeg", Queue: &logQueue{}}
	src := media.Info{Streams: []media.Stream{{Index: 1, Type: "audio"}}}
	plan := rules.Plan{EvenLoudness: true, Tracks: []rules.TrackOp{{Index: 1, Type: "audio", Encoder: "aac", Kbps: 128, Channels: 2}}}
	got, _ := w.measureLoudness(context.Background(), jobs.Job{ID: 7, Path: "m.mkv"}, src, "/src/m.mkv", plan)
	if len(x.calls) != 0 || got.Tracks[0].Loudness != nil {
		t.Errorf("measured a track with no known rate: %d runs", len(x.calls))
	}
}

// A stop ends the step with ctx's error and writes no line about the measure.
func TestMeasureLoudnessStopEndsTheJobWithNoLine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	x := &loudExec{reply: func(_ int, c context.Context) (string, error) { return "", c.Err() }}
	q := &logQueue{}
	w := &Worker{Exec: x, FFmpeg: "ffmpeg", Queue: q}
	_, err := w.measureLoudness(ctx, jobs.Job{ID: 7, Path: "m.mkv"}, twoAudioSource(), "/src/m.mkv", twoConverted())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v, want the stop", err)
	}
	if len(q.lines) != 0 {
		t.Errorf("a stop wrote lines: %q", q.lines)
	}
}

// Past the cap the step stops: the track it was on and the rest keep their audio, a line says so, and the job goes
// on (no error).
func TestMeasureLoudnessPastTheCapLeavesTheRest(t *testing.T) {
	old := loudnessTotalLimit
	loudnessTotalLimit = 20 * time.Millisecond
	defer func() { loudnessTotalLimit = old }()
	x := &loudExec{reply: func(_ int, c context.Context) (string, error) {
		<-c.Done() // a measure that runs past the cap
		return "", c.Err()
	}}
	q := &logQueue{}
	w := &Worker{Exec: x, FFmpeg: "ffmpeg", Queue: q}
	got, err := w.measureLoudness(context.Background(), jobs.Job{ID: 7, Path: "m.mkv"}, twoAudioSource(), "/src/m.mkv", twoConverted())
	if err != nil {
		t.Fatalf("the cap ended the job: %v", err)
	}
	if got.Tracks[0].Loudness != nil || got.Tracks[1].Loudness != nil {
		t.Error("a track was normalised past the cap")
	}
	if len(x.calls) != 1 {
		t.Errorf("%d runs after the cap, want none further", len(x.calls)-1)
	}
	if lines(q, "measuring took more than") != 1 {
		t.Errorf("log %q: want one cap line", q.lines)
	}
}

// Only a converted or added audio track can be measured: a plan that copies everything has none.
func TestHasConvertedAudioIsOnlyConvertedOrAdded(t *testing.T) {
	if hasConvertedAudio(rules.Plan{EvenLoudness: true}) {
		t.Error("a plan with no tracks reports converted audio")
	}
	copyOnly := rules.Plan{EvenLoudness: true, Tracks: []rules.TrackOp{{Index: 1, Type: "audio", Drop: true}, {Index: 2, Type: "subtitle", Encoder: "mov_text"}}}
	if hasConvertedAudio(copyOnly) {
		t.Error("a dropped track or a subtitle counts as converted audio")
	}
	added := rules.Plan{EvenLoudness: true, Tracks: []rules.TrackOp{{Index: 1, Type: "audio", Encoder: "aac", Add: true}}}
	if !hasConvertedAudio(added) {
		t.Error("an added track does not count")
	}
}

// Subtitles and dropped tracks are never measured, even when they carry an encoder: only audio is. The subtitle is
// given a rate, so only the type check (not the missing-rate check) can keep it out.
func TestMeasureLoudnessSkipsSubtitlesAndDroppedTracks(t *testing.T) {
	x := &loudExec{reply: func(int, context.Context) (string, error) { return loudPrint, nil }}
	w := &Worker{Exec: x, FFmpeg: "ffmpeg", Queue: &logQueue{}}
	src := media.Info{Streams: []media.Stream{{Index: 1, Type: "audio", SampleRate: 48000}, {Index: 2, Type: "subtitle", SampleRate: 48000}}}
	plan := rules.Plan{EvenLoudness: true, Tracks: []rules.TrackOp{
		{Index: 1, Type: "audio", Drop: true, Encoder: "aac"},
		{Index: 2, Type: "subtitle", Encoder: "mov_text"},
	}}
	if _, err := w.measureLoudness(context.Background(), jobs.Job{ID: 7, Path: "m.mkv"}, src, "/src/m.mkv", plan); err != nil {
		t.Fatal(err)
	}
	if len(x.calls) != 0 {
		t.Errorf("%d measuring runs on a subtitle or a dropped track", len(x.calls))
	}
}
