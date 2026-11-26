// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package preview

import (
	"context"
	"io/fs"
	"math"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// fullDecodeMaxSec is the longest file the worker plays completely before it keeps the new file (the
// worker's auto check; longer files are only partly played). Kept in step with worker.DefaultFullDecodeMaxSec.
const fullDecodeMaxSec = 3 * 3600

// Line is one line of a sample: its kind (video, hdr, audio, subtitles or original) and its text.
type Line struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Track is one file's sample under a workflow's answers (the guide's "Try it on one file", S1): its lines
// by kind, the new file's size range, the checks the workflow runs before it keeps the new file, and how
// long this computer would take when it has a speed for the encoder. Nothing is converted.
type Track struct {
	Lines             []Line   `json:"lines"`
	ExpectedBytesLow  int64    `json:"expectedBytesLow"`
	ExpectedBytesHigh int64    `json:"expectedBytesHigh"`
	Checks            []string `json:"checks"`
	// EstimatedSeconds is left out when no speed is known for the chosen encoder yet.
	EstimatedSeconds *int64 `json:"estimatedSeconds,omitempty"`
	Skipped          bool   `json:"skipped"`
}

// Track says what the answers compiled to (t) would do with one file. speed gives the realtime multiple
// this computer last measured for an encoder (ffmpeg's name), or 0 when it has none; nil means no estimate.
func (p *Previewer) Track(ctx context.Context, root, path string, st fs.FileInfo, t rules.Template, speed func(encoder string) float64) Track {
	size := st.Size()
	info, plan := p.plan(ctx, root, path, st, t)
	if plan.Skip != "" {
		return Track{Lines: []Line{{Kind: "original", Text: "Left as it is: " + skipWhy(plan) + "."}},
			ExpectedBytesLow: size, ExpectedBytesHigh: size, Skipped: true}
	}
	low, high := Saving(t, plan, info, size)
	tr := Track{ExpectedBytesLow: size - high, ExpectedBytesHigh: size - low}
	tr.Lines = append(tr.Lines, Line{Kind: "video", Text: videoLine(plan, info)})
	if hdr := hdrLine(plan, info); hdr != "" {
		tr.Lines = append(tr.Lines, Line{Kind: "hdr", Text: hdr})
	}
	tr.Lines = append(tr.Lines, Line{Kind: "audio", Text: audioLine(plan, len(info.Audio()))})
	if line, _ := subtitleLine(t, plan, info); line != "" {
		tr.Lines = append(tr.Lines, Line{Kind: "subtitles", Text: line})
	}
	if plan.Output == "beside" {
		tr.Lines = append(tr.Lines, Line{Kind: "original", Text: "Original: stays as it is, next to the new file"})
	} else {
		tr.Lines = append(tr.Lines, Line{Kind: "original", Text: "Original: kept in a .syp-trash folder beside it"})
	}

	tr.Checks = []string{"Same length as the original"}
	if info.DurationSec <= fullDecodeMaxSec {
		tr.Checks = append(tr.Checks, "Plays to the end")
	}
	tr.Checks = append(tr.Checks, "Picture matches the original")
	if plan.KeepIfBigger {
		tr.Checks = append(tr.Checks, "Smaller than the original, or the original is kept")
	}

	if speed != nil && !plan.Video.Copy && info.DurationSec > 0 {
		if r := speed(encoderName(plan)); r > 0 {
			secs := int64(math.Ceil(info.DurationSec / r))
			tr.EstimatedSeconds = &secs
		}
	}
	return tr
}

// encoderName is the software encoder ffmpeg runs for the plan's video ("libx265" and so on), or "".
func encoderName(plan rules.Plan) string {
	args := plan.FFmpegArgs("in", "out")
	for i := 0; i+1 < len(args); i++ {
		if strings.HasPrefix(args[i], "-c:v") { // "-c:v:0" with its index
			return strings.TrimSpace(args[i+1])
		}
	}
	return ""
}
