// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"regexp"
	"strconv"
	"strings"
)

// StreamStats is what ffmpeg's end-of-run report says about one input stream: how much it read from
// it and what the decoder made of it. The report is printed at -v verbose only, so a run at the
// usual -v error has none.
type StreamStats struct {
	Packets      int64 // packets read from the input (one per picture for video)
	Frames       int64 // pictures decoded; -1 when the report has no such field (a copied stream)
	DecodeErrors int64
}

var (
	inputStreamRe   = regexp.MustCompile(`Input stream #(\d+):(\d+) \([a-z]+\): (\d+) packets read \(\d+ bytes\)`)
	framesDecodedRe = regexp.MustCompile(`(\d+) frames decoded`)
	decodeErrorsRe  = regexp.MustCompile(`(\d+) decode errors`)
)

// InputStreamStats reads ffmpeg's end-of-run report for input in's stream stream out of its log
// (stderr):
//
//	Input stream #0:0 (video): 75 packets read (264876 bytes); 75 frames decoded; 0 decode errors;
//
// A copied stream's line stops after the bytes: Frames is then -1. The damage check compares
// Packets and Frames: a stream from which ffmpeg read every packet but decoded fewer pictures lost
// pictures without saying so.
func InputStreamStats(log string, in, stream int) (StreamStats, bool) {
	for _, line := range strings.Split(log, "\n") {
		m := inputStreamRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		i, _ := strconv.Atoi(m[1])
		s, _ := strconv.Atoi(m[2])
		if i != in || s != stream {
			continue
		}
		st := StreamStats{Frames: -1}
		st.Packets, _ = strconv.ParseInt(m[3], 10, 64)
		if f := framesDecodedRe.FindStringSubmatch(line); f != nil {
			st.Frames, _ = strconv.ParseInt(f[1], 10, 64)
		}
		if d := decodeErrorsRe.FindStringSubmatch(line); d != nil {
			st.DecodeErrors, _ = strconv.ParseInt(d[1], 10, 64)
		}
		return st, true
	}
	return StreamStats{}, false
}
