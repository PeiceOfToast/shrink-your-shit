// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// Verdict is the answer of the result check. A "no" is never an error: it means keep the original.
type Verdict struct {
	OK         bool
	Code       string
	Message    string
	NextAction string
}

// Codes of the "no" verdicts, one per check.
const (
	CodeIncomplete      = "check.incomplete"
	CodeTooShort        = "check.too_short"
	CodeTooLong         = "check.too_long"
	CodeVideoMissing    = "check.video_missing"
	CodeAudioMissing    = "check.audio_missing"
	CodeAudioLanguage   = "check.audio_language"
	CodeAudioChannels   = "check.audio_channels"
	CodeSubtitleMissing = "check.subtitle_missing"

	// NextKeepOriginal is what every "no" tells the job.
	NextKeepOriginal = "keep_original"
)

// The duration rule. The plan names no number, so these are workshop's 11-27 choice: a result may differ from the
// source by the larger of one second and 0.5 %.
const (
	DurationFloor = time.Second
	DurationRatio = 0.005
)

// DefaultVerifyTimeout bounds the probe of checks 1-3 (L-SAFE-1: a check that hangs times out).
const DefaultVerifyTimeout = 2 * time.Minute

// Recorder writes each check's result to the job log as a decision line (Source "safety"). A nil Bus writes nothing,
// so a zero Recorder is valid. A line the bus refuses must not change the verdict, so the Emit error is dropped.
type Recorder struct {
	Bus   *events.Bus
	JobID int64
}

// log records one check and returns its verdict unchanged.
func (r Recorder) log(check string, v Verdict) Verdict {
	if r.Bus == nil {
		return v
	}
	e := events.Event{JobID: r.JobID, Kind: events.KindDecision, Source: "safety", Level: events.LevelInfo}
	if v.OK {
		e.Code = "check.pass"
		e.Message = check + ": pass"
	} else {
		e.Level = events.LevelWarn
		e.Code = v.Code
		e.Message = check + ": " + v.Message
		e.NextAction = v.NextAction
	}
	_ = r.Bus.Emit(e)
	return v
}

// Want is what the plan asked for. Flow fills it in; safety never imports encode.
type Want struct {
	// Audio has one entry per audio track the output must keep.
	Audio []WantedAudio
	// SubtitleLanguages has one entry per subtitle track the container can hold.
	SubtitleLanguages []string
}

// WantedAudio is one kept audio track: the language the output must still carry ("" for unknown)
// and the channels it must have there. A zero channel count means the source's count was unknown,
// so the output's is not checked.
type WantedAudio struct {
	Language string
	Channels int
}

// Verify runs checks 1-3 of backend-plan §7.1 in order and stops at the first "no". Checks 4-5 (playback decode,
// picture comparison, L-SAFE-2 and L-SAFE-3) come in stage 2.
//
// The error is for a check that could not run: a timeout, a cancelled context, or ffprobe missing or crashing. The
// worker treats that as may-pass. A file ffprobe cannot read is a "no", not an error.
func Verify(ctx context.Context, p media.Prober, rec Recorder, st Staged, src media.Info, want Want, timeout time.Duration) (Verdict, error) {
	if timeout <= 0 {
		timeout = DefaultVerifyTimeout
	}

	// 1. Complete: the size holds across a stat, an fsync and another stat, and the probe reads the file.
	if v, err := checkComplete(ctx, p, st, timeout); v != nil || err != nil {
		if err != nil {
			return Verdict{}, err
		}
		return rec.log("complete", *v), nil
	}
	out, err := probe(ctx, p, st.Path, timeout)
	if err != nil {
		return Verdict{}, err
	}
	if out == nil {
		return rec.log("complete", noVerdict(CodeIncomplete, "the file could not be read as media")), nil
	}
	rec.log("complete", Verdict{OK: true})

	// 2. Duration.
	if v := checkDuration(src, *out); v != nil {
		return rec.log("duration", *v), nil
	}
	rec.log("duration", Verdict{OK: true})

	// 3. Streams.
	if v := checkStreams(*out, want); v != nil {
		return rec.log("streams", *v), nil
	}
	rec.log("streams", Verdict{OK: true})

	return Verdict{OK: true}, nil
}

// checkComplete returns (verdict, nil) for a "no", (nil, err) for a check that could not run, and (nil, nil) for a
// pass.
func checkComplete(ctx context.Context, p media.Prober, st Staged, timeout time.Duration) (*Verdict, error) {
	before, err := os.Stat(st.Path)
	if err != nil {
		return nil, fmt.Errorf("verify: stat staged file: %w", err)
	}
	if err := syncPath(st.Path); err != nil {
		return nil, fmt.Errorf("verify: sync staged file: %w", err)
	}
	after, err := os.Stat(st.Path)
	if err != nil {
		return nil, fmt.Errorf("verify: stat staged file: %w", err)
	}
	if before.Size() != after.Size() {
		v := noVerdict(CodeIncomplete, "the file was still being written")
		return &v, nil
	}
	return nil, nil
}

// probe reads the staged file. It returns (nil, nil) for a file ffprobe calls not media, and an error for a check
// that could not run.
func probe(ctx context.Context, p media.Prober, path string, timeout time.Duration) (*media.Info, error) {
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	info, err := p.Probe(pctx, path)
	if err == nil {
		return &info, nil
	}
	if errors.Is(err, media.ErrNotMedia) && pctx.Err() == nil {
		return nil, nil
	}
	return nil, fmt.Errorf("verify: probe could not run: %w", err)
}

func checkDuration(src, out media.Info) *Verdict {
	want := src.Format.Duration
	got := out.Format.Duration
	tol := time.Duration(float64(want) * DurationRatio)
	if tol < DurationFloor {
		tol = DurationFloor
	}
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	if diff <= tol {
		return nil
	}
	if got < want {
		v := noVerdict(CodeTooShort, fmt.Sprintf("the result is %s, the source %s", got, want))
		return &v
	}
	v := noVerdict(CodeTooLong, fmt.Sprintf("the result is %s, the source %s", got, want))
	return &v
}

func checkStreams(out media.Info, want Want) *Verdict {
	if len(out.Video) == 0 {
		v := noVerdict(CodeVideoMissing, "the result has no video stream")
		return &v
	}
	if len(out.Audio) < len(want.Audio) {
		v := noVerdict(CodeAudioMissing, fmt.Sprintf("the result has %d audio tracks, %d were planned", len(out.Audio), len(want.Audio)))
		return &v
	}
	got := make([]string, 0, len(out.Audio))
	for _, a := range out.Audio {
		got = append(got, a.Language)
	}
	wantLanguages := make([]string, 0, len(want.Audio))
	var wantChannels []int
	for _, a := range want.Audio {
		wantLanguages = append(wantLanguages, a.Language)
		if a.Channels > 0 {
			wantChannels = append(wantChannels, a.Channels)
		}
	}
	if !sameMultiset(got, wantLanguages) {
		v := noVerdict(CodeAudioLanguage, fmt.Sprintf("audio languages are %v, planned %v", got, wantLanguages))
		return &v
	}
	gotChannels := make([]int, 0, len(out.Audio))
	for _, a := range out.Audio {
		if a.Channels > 0 {
			gotChannels = append(gotChannels, a.Channels)
		}
	}
	if !sameIntMultiset(gotChannels, wantChannels) {
		v := noVerdict(CodeAudioChannels, fmt.Sprintf("audio channels are %v, planned %v", gotChannels, wantChannels))
		return &v
	}
	subs := make([]string, 0, len(out.Subtitles))
	for _, s := range out.Subtitles {
		subs = append(subs, s.Language)
	}
	if !containsMultiset(subs, want.SubtitleLanguages) {
		v := noVerdict(CodeSubtitleMissing, fmt.Sprintf("subtitle languages are %v, planned %v", subs, want.SubtitleLanguages))
		return &v
	}
	return nil
}

func noVerdict(code, msg string) Verdict {
	return Verdict{OK: false, Code: code, Message: msg, NextAction: NextKeepOriginal}
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func sameMultiset(a, b []string) bool {
	x, y := sorted(a), sorted(b)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func sameIntMultiset(a, b []int) bool {
	x, y := append([]int(nil), a...), append([]int(nil), b...)
	sort.Ints(x)
	sort.Ints(y)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// containsMultiset reports whether every entry of want is in got, counting repeats.
func containsMultiset(got, want []string) bool {
	left := map[string]int{}
	for _, g := range got {
		left[g]++
	}
	for _, w := range want {
		if left[w] == 0 {
			return false
		}
		left[w]--
	}
	return true
}
