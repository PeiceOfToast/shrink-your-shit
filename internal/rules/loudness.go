// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// The loudness target of "Even out loudness" (F5): one fixed target, not a setting. -24 LUFS is the integrated
// level, -2 dBTP keeps the true peak clear of clipping, and 7 LU is the loudness range the normaliser aims for.
const (
	LoudnessI   = -24.0
	LoudnessTP  = -2.0
	LoudnessLRA = 7.0
)

// Loudness is what the measuring pass read from one converted audio track (F5), and the sample rate the track
// keeps. The worker sets it on the track's TrackOp; a track without it gets no loudness filter.
type Loudness struct {
	Rate   int     `json:"rate"`   // the track's sample rate, kept on output (loudnorm would make 192 kHz)
	I      float64 `json:"i"`      // measured integrated loudness (LUFS)
	TP     float64 `json:"tp"`     // measured true peak (dBTP)
	LRA    float64 `json:"lra"`    // measured loudness range (LU)
	Thresh float64 `json:"thresh"` // the gating threshold the measure used
	Offset float64 `json:"offset"` // the gain the first pass says reaches the target
}

// ParseLoudnorm reads the JSON print of loudnorm's first pass out of ffmpeg's stderr. The print is the last
// {...} in the text, and words may sit around it. ok is false when there is no such print, or when a value is
// not a finite number: a silent track measures "-inf", and it has no loudness to even out.
func ParseLoudnorm(stderr string) (Loudness, bool) {
	start := strings.LastIndex(stderr, "{")
	if start < 0 {
		return Loudness{}, false
	}
	end := strings.Index(stderr[start:], "}")
	if end < 0 {
		return Loudness{}, false
	}
	var j struct {
		I      string `json:"input_i"`
		TP     string `json:"input_tp"`
		LRA    string `json:"input_lra"`
		Thresh string `json:"input_thresh"`
		Offset string `json:"target_offset"`
	}
	if err := json.Unmarshal([]byte(stderr[start:start+end+1]), &j); err != nil {
		return Loudness{}, false
	}
	var f [5]float64
	for k, s := range []string{j.I, j.TP, j.LRA, j.Thresh, j.Offset} {
		n, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
			return Loudness{}, false
		}
		f[k] = n
	}
	return Loudness{I: f[0], TP: f[1], LRA: f[2], Thresh: f[3], Offset: f[4]}, true
}

// foldLayouts are the channel layouts a downmix (TrackOp.Channels) can name for aformat.
var foldLayouts = map[int]string{1: "mono", 2: "stereo", 6: "5.1", 8: "7.1"}

// loudnessFold is the downmix a converted track gets, written as a filter so the measure and the encode see the
// same audio: the filter chain folds before loudnorm, not in ffmpeg's output stage after it. "" when the track
// keeps its channels.
func loudnessFold(op TrackOp) string {
	if op.Channels <= 0 {
		return ""
	}
	if l, ok := foldLayouts[op.Channels]; ok {
		return "aformat=channel_layouts=" + l
	}
	return ""
}

// LoudnessMeasureFilter is the first pass's audio filter for op: its track as it will be encoded (folded), measured
// against the target, with the JSON print on.
func LoudnessMeasureFilter(op TrackOp) string {
	return chainFilters(loudnessFold(op),
		fmt.Sprintf("loudnorm=I=%g:TP=%g:LRA=%g:print_format=json", LoudnessI, LoudnessTP, LoudnessLRA))
}

// LoudnessFilter is the audio filter of a converted track whose loudness was measured (F5): the fold, the second
// pass in linear mode with the measured values, and aresample back to the track's own rate. loudnorm would
// otherwise leave the track at 192 kHz.
func LoudnessFilter(op TrackOp) string {
	m := op.Loudness
	if m == nil {
		return ""
	}
	return chainFilters(loudnessFold(op),
		fmt.Sprintf("loudnorm=I=%g:TP=%g:LRA=%g:measured_I=%s:measured_TP=%s:measured_LRA=%s:measured_thresh=%s:offset=%s:linear=true",
			LoudnessI, LoudnessTP, LoudnessLRA,
			f2(m.I), f2(m.TP), f2(m.LRA), f2(m.Thresh), f2(m.Offset)),
		fmt.Sprintf("aresample=%d", m.Rate))
}

// f2 formats a measured value with two decimals, enough for a target 1 LU away.
func f2(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

// chainFilters joins the non-empty filters with commas.
func chainFilters(parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, ",")
}

// loudnessArgs are the -filter:a:<a> arguments for a converted or added track when the Plan asks to even out the
// loudness and the track was measured. None otherwise: a copied track is never touched.
func (p Plan) loudnessArgs(op TrackOp, a string) []string {
	if !p.EvenLoudness || op.Loudness == nil {
		return nil
	}
	return []string{"-filter:" + a, LoudnessFilter(op)}
}
