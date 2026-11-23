// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// loudnessTotalLimit caps the whole measuring step (F5): one audio-only pass per converted track, all together. A
// variable so a test can make it short. Ten minutes: audio decodes far faster than real time, so the tracks of a
// two-hour film are measured well inside it. Past it the file is converted with its audio as it is, rather than a
// conversion waiting on a measure.
var loudnessTotalLimit = 10 * time.Minute

// hasConvertedAudio is whether the plan converts or adds an audio track: the only tracks loudness can touch.
func hasConvertedAudio(p rules.Plan) bool {
	for _, op := range p.Tracks {
		if op.Type == "audio" && !op.Drop && op.Encoder != "" {
			return true
		}
	}
	return false
}

// measureLoudness fills in the loudness of each converted or added audio track of the plan (F5): one audio-only
// pass per track, over the file the encode reads, with the track folded as the encode folds it. A track that
// cannot be measured is left as it is, with a line in the job log, and the job goes on. A stop (ctx cancelled)
// returns ctx's error: the job ends, with no line about the measure and no encode.
func (w *Worker) measureLoudness(ctx context.Context, job jobs.Job, src media.Info, srcPath string, p rules.Plan) (rules.Plan, error) {
	tctx, cancel := context.WithTimeout(ctx, loudnessTotalLimit)
	defer cancel()
	ops := append([]rules.TrackOp(nil), p.Tracks...)
	type measured struct {
		index int
		level rules.Loudness
	}
	var done []measured
	capped := false
	for k := range ops {
		op := &ops[k]
		if op.Type != "audio" || op.Drop || op.Encoder == "" {
			continue
		}
		m, err := w.measureTrack(tctx, srcPath, src, *op)
		if err := ctx.Err(); err != nil { // stopped: the job ends with no line about the measure
			return p, err
		}
		if tctx.Err() != nil { // the step ran past its cap: no track in this file is evened out
			w.jobNote(job.ID, secEncode, jobs.LevelWarning, "(%s): the loudness was not evened out: measuring took more than %s, so no track in this file is evened out", job.Path, loudnessTotalLimit)
			capped = true
			break
		}
		if err != nil {
			w.jobNote(job.ID, secEncode, jobs.LevelWarning, "(%s): the loudness of audio track %d could not be measured (%s), so it is left as it is", job.Path, op.Index, err)
			continue
		}
		op.Loudness = &m
		done = append(done, measured{op.Index, m})
	}
	if capped { // a file is evened out all at once or not at all, so a track measured before the cap goes back too
		for k := range ops {
			ops[k].Loudness = nil
		}
	}
	if !capped { // the line only once the track really is evened out
		for _, d := range done {
			w.jobNote(job.ID, secEncode, jobs.LevelInfo, "(%s): audio track %d measures %.1f LUFS; it is evened out to %g LUFS", job.Path, d.index, d.level.I, rules.LoudnessI)
		}
	}
	p.Tracks = ops
	return p, nil
}

// measureTrack runs loudnorm's first pass over one track: the audio only, as the encode will make it, with the
// JSON print read back. It needs the track's sample rate, which the output keeps; a track without one cannot be
// measured.
func (w *Worker) measureTrack(ctx context.Context, srcPath string, src media.Info, op rules.TrackOp) (rules.Loudness, error) {
	rate := streamRate(src, op.Index)
	if rate <= 0 {
		return rules.Loudness{}, errors.New("its sample rate is not known")
	}
	// -nostats: the progress lines of a long track would push the print past what Capture keeps of stderr.
	_, stderr, err := w.Exec.Capture(ctx, w.FFmpeg, "-hide_banner", "-nostdin", "-nostats", "-v", "info",
		"-i", srcPath, "-map", "0:"+strconv.Itoa(op.Index), "-vn", "-sn",
		"-af", rules.LoudnessMeasureFilter(op), "-f", "null", "-")
	if err != nil {
		return rules.Loudness{}, errors.New("the measuring run failed")
	}
	m, ok := rules.ParseLoudnorm(string(stderr))
	if !ok {
		return rules.Loudness{}, errors.New("no readable loudness, or a silent track")
	}
	m.Rate = rate
	return m, nil
}

// streamRate is the sample rate of the source's stream with that index, 0 when there is none.
func streamRate(src media.Info, index int) int {
	for _, s := range src.Streams {
		if s.Index == index {
			return s.SampleRate
		}
	}
	return 0
}
