// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"fmt"
	"math"
	"strings"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// The sentences a job's log shows for its events. The Job log is what the person reads at stop 1, so each event has
// one plain line, built here from the facts the engine already has.

// stepSentence is a step's line: its number on the pages and its name, as the Start page counts them.
func stepSentence(index int, name string) string {
	return fmt.Sprintf("%d. %s", index, name)
}

// probeSentence summarises a file: its first movie video track's codec and size, then its duration in seconds.
// Cover art is not the movie, so it is skipped.
func probeSentence(info media.Info) string {
	var parts []string
	for _, v := range info.Video {
		if v.AttachedPicture {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %d×%d", codecName(v.Codec), v.Width, v.Height))
		break
	}
	if d := info.Format.Duration; d > 0 {
		parts = append(parts, fmt.Sprintf("%s s", trimFloat(d.Seconds())))
	}
	return strings.Join(parts, ", ")
}

// codecName is the name a person knows a codec by.
func codecName(c string) string {
	switch strings.ToLower(c) {
	case "h264":
		return "H.264"
	case "hevc", "h265":
		return "HEVC"
	case "av1":
		return "AV1"
	case "vp9":
		return "VP9"
	case "mpeg4":
		return "MPEG-4"
	}
	return strings.ToUpper(c)
}

// sizeSentence is a size change: before → after, and the saving in percent when there was a before.
func sizeSentence(before, after int64) string {
	s := humanBytes(before) + " → " + humanBytes(after)
	if before > 0 {
		pct := math.Round(float64(after-before) * 100 / float64(before))
		s += fmt.Sprintf(" (%+.0f %%)", pct)
	}
	return s
}

// humanBytes writes a size in KB, MB or GB, one decimal place, as the pages do.
func humanBytes(n int64) string {
	const k = 1000.0
	f := float64(n)
	switch {
	case f >= k*k*k:
		return fmt.Sprintf("%.1f GB", f/(k*k*k))
	case f >= k*k:
		return fmt.Sprintf("%.1f MB", f/(k*k))
	case f >= k:
		return fmt.Sprintf("%.1f KB", f/k)
	}
	return fmt.Sprintf("%d B", n)
}

// trimFloat writes a number without trailing zeros: 4 for 4.0, 3.5 for 3.5.
func trimFloat(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", f), "0"), ".")
}
