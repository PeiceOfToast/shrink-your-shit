// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// Try on one file (flow-model.md): one bounded request, no job. The engine runs on a sample and
// stops after the result check, so nothing is placed and the staging is removed on every path.
const (
	// TrySample is how much of a long file a try encodes.
	TrySample = 60 * time.Second
	// TryWhole is the length at or under which a try uses the whole file: cutting a sample out of
	// so short a file costs more than it saves and makes the estimate noisier.
	TryWhole = 3 * time.Minute
)

// TryLine is one line of the pages' Try card: the step it belongs to and the sentence.
type TryLine struct {
	StepID string `json:"stepId"`
	Text   string `json:"text"`
}

// TryResult is one try's answer: a line per step, then the size line.
type TryResult struct {
	Lines []TryLine
}

// TryError is a try the page cannot run. Code is the route's error code; Message is the sentence
// the page shows as it is.
type TryError struct {
	Code    string
	Message string
}

func (e *TryError) Error() string { return e.Message }

// The size and result-check lines a try answers with, ui-shop's words. "about" is on the number
// scaled from the sample and on no other.
const (
	trySizeSample   = "Size: about %s for the whole file (now %s), estimated from a one-minute sample."
	trySizeWhole    = "Size: %s (now %s)."
	tryBiggerSample = "Size: about %s, bigger than the original %s, so the original would be kept."
	tryBiggerWhole  = "Size: %s, bigger than the original %s, so the original would be kept."
	tryCheckFailed  = "The result check failed: %s. A real run would keep the original."
)

// Try runs f on one file and answers the lines the pages show. It never asks for a saved revision
// and never writes beside the file: the engine stops after the result check, and its staging goes
// with the run, whether it passed, failed or was stopped. A sample is cut in scratch, a folder the
// app made for this (never the system temp, which can be a small memory-backed folder).
func Try(ctx context.Context, f Flow, file string, env Env, scratch string) (TryResult, error) {
	if err := CheckShape(f); err != nil {
		return TryResult{}, &TryError{Code: "invalid_flow", Message: err.Error()}
	}
	if env.Media == nil || env.Safety == nil {
		return TryResult{}, errors.New("flow: a try needs media and safety")
	}
	info, err := env.Media.Probe(ctx, file)
	if err != nil {
		if ctx.Err() != nil {
			return TryResult{}, ctx.Err()
		}
		if errors.Is(err, media.ErrNotMedia) {
			return TryResult{}, &TryError{Code: "invalid_file", Message: "The file could not be read as media."}
		}
		return TryResult{}, fmt.Errorf("try %s: probe: %w", file, err)
	}
	if len(info.Video) == 0 {
		return TryResult{}, &TryError{Code: "invalid_file", Message: "The file has no video, so there is nothing to try."}
	}
	if info.Format.Duration <= 0 {
		return TryResult{}, &TryError{Code: "invalid_file", Message: "The file's length could not be read."}
	}
	st, err := os.Stat(file)
	if err != nil {
		return TryResult{}, &TryError{Code: "invalid_file", Message: "The file could not be read."}
	}
	whole := info.Format.Duration
	origSize := st.Size()

	// A long file is tried on a sample from a third of the way in: past the titles and the opening
	// credits, and long enough that the encoder's first frames are not the whole estimate. The
	// sample is cut out with a stream copy into a folder of its own, so the engine runs on a real
	// file with nothing to seek and nothing is ever written beside the original.
	source := file
	sample := time.Duration(0)
	if whole > TryWhole {
		dir, path, err := cutSample(ctx, file, whole, env, scratch)
		if err != nil {
			return TryResult{}, err
		}
		defer os.RemoveAll(dir)
		source = path
		sampleInfo, err := env.Media.Probe(ctx, source)
		if err != nil {
			if ctx.Err() != nil {
				return TryResult{}, ctx.Err()
			}
			return TryResult{}, fmt.Errorf("try %s: probe the sample: %w", file, err)
		}
		sample = sampleInfo.Format.Duration
	}
	st, err = os.Stat(source)
	if err != nil {
		return TryResult{}, &TryError{Code: "invalid_file", Message: "The file could not be read."}
	}

	lines := &tryRun{}
	env.Bus = lines
	// A try is not a job, so nothing goes to the job log.
	env.SafetyLog = safety.Recorder{}
	res := Run(ctx, RunInput{
		Try: true, Flow: &f,
		LibraryRoot: filepath.Dir(source), Source: source,
		Size: st.Size(), MTime: st.ModTime(),
	}, env)
	if ctx.Err() != nil {
		return TryResult{}, ctx.Err()
	}

	switch res.Outcome {
	case OutcomeSmaller:
		return TryResult{Lines: append(lines.all(), TryLine{StepID: "save", Text: sizeLine(res.SizeAfter, origSize, whole, sample)})}, nil
	case OutcomeBiggerKept:
		// The engine's bigger_kept is this try's result check saying no: the size rule never runs
		// for a try, so this outcome is always the check.
		return TryResult{Lines: lines.replace("save", fmt.Sprintf(tryCheckFailed, res.Message))}, nil
	case OutcomeMayPass:
		return TryResult{Lines: lines.replace("save", res.Message)}, nil
	case OutcomeSkip:
		return TryResult{Lines: lines.all()}, nil
	case OutcomeCancelled:
		return TryResult{}, context.Canceled
	case OutcomeFatal:
		return TryResult{}, &TryError{Code: "try_failed", Message: res.Message}
	default:
		return TryResult{}, fmt.Errorf("flow: a try ended %q", res.Outcome)
	}
}

// cutSample copies one minute out of a long file, a third of the way in, into a folder of its
// own. A stream copy starts at the keyframe before the seek point, so the video and audio stay
// aligned; seeking while re-encoding is the one arrangement that does not (Mkv kept the video
// about eight seconds late, and the result check said so). The pre-roll a keyframe adds is part
// of the sample and of its measured length, so the estimate is not skewed by it.
func cutSample(ctx context.Context, file string, whole time.Duration, env Env, scratch string) (dir, path string, err error) {
	if scratch == "" {
		return "", "", errors.New("flow: a try needs a folder to cut its sample in")
	}
	dir, err = os.MkdirTemp(scratch, "try-*")
	if err != nil {
		return "", "", fmt.Errorf("try: make a sample folder: %w", err)
	}
	ext := filepath.Ext(file)
	if ext == "" {
		ext = ".mkv"
	}
	path = filepath.Join(dir, "sample"+ext)
	argv := []string{
		"ffmpeg", "-y", "-nostdin", "-loglevel", "error",
		"-ss", ffmpegSeconds(whole / 3), "-t", ffmpegSeconds(TrySample), "-i", file,
		"-map", "0:v?", "-map", "0:a?", "-map", "0:s?", "-c", "copy",
		"-avoid_negative_ts", "make_zero", path,
	}
	if _, err := env.Media.Run(ctx, argv, nil); err != nil {
		os.RemoveAll(dir)
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		return "", "", &TryError{Code: "invalid_file", Message: "A sample could not be read from this file."}
	}
	return dir, path, nil
}

// ffmpegSeconds writes a duration for an ffmpeg option: milliseconds, never exponent notation.
func ffmpegSeconds(d time.Duration) string {
	return fmt.Sprintf("%.3f", d.Seconds())
}

// sizeLine is the line under a try's results: the sample's size scaled to the whole file, or the
// whole file's own size, and whether a real run would keep the original.
func sizeLine(after, original int64, whole, sample time.Duration) string {
	shown := after
	if sample > 0 {
		shown = int64(math.Round(float64(after) * float64(whole) / float64(sample)))
	}
	if shown >= original {
		if sample > 0 {
			return fmt.Sprintf(tryBiggerSample, humanBytes(shown), humanBytes(original))
		}
		return fmt.Sprintf(tryBiggerWhole, humanBytes(shown), humanBytes(original))
	}
	if sample > 0 {
		return fmt.Sprintf(trySizeSample, humanBytes(shown), humanBytes(original))
	}
	return fmt.Sprintf(trySizeWhole, humanBytes(shown), humanBytes(original))
}

// tryRun collects one line per step_end, in the order the plan wrote them. It is the run's only
// emitter: a try keeps no log.
type tryRun struct {
	mu    sync.Mutex
	lines []TryLine
}

func (r *tryRun) Emit(ev events.Event) error {
	if ev.Kind != events.KindStepEnd {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, TryLine{StepID: ev.StepID, Text: ev.Message})
	return nil
}

func (r *tryRun) all() []TryLine {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]TryLine(nil), r.lines...)
}

// replace puts text where a step's own line was. A step that wrote no line (a flow the engine
// refused before it got there) still gets the sentence, so it is never lost.
func (r *tryRun) replace(stepID, text string) []TryLine {
	lines := r.all()
	for i := range lines {
		if lines[i].StepID == stepID {
			lines[i].Text = text
			return lines
		}
	}
	return append(lines, TryLine{StepID: stepID, Text: text})
}
