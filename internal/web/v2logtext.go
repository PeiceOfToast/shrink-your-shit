// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"fmt"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// logPiece is how many log lines one read gives (the old file reader's ReadMax, in lines).
const logPiece = 200

// logBatch is one read of job id's log: the lines after sequence number after (at most logPiece), and the number
// to read from next (the last line's; after again when there is none).
func logBatch(store *jobs.Store, id, after int64) (lines []jobs.LogLine, next int64, err error) {
	lines, err = store.LogLinesAfter(id, after, logPiece)
	if err != nil {
		return nil, after, err
	}
	next = after
	if len(lines) > 0 {
		next = lines[len(lines)-1].Seq
	}
	return lines, next, nil
}

// logTextOf shows log lines as the stream and the Files view do: "15:04:05 text" a line, UTC.
func logTextOf(lines []jobs.LogLine) string {
	var b strings.Builder
	for _, l := range lines {
		fmt.Fprintf(&b, "%s %s\n", clockOf(l.At), l.Text)
	}
	return b.String()
}

// logText is job id's log lines after sequence number after, as text (see logTextOf), and the number to read from
// next.
func logText(store *jobs.Store, id, after int64) (text string, next int64, err error) {
	lines, next, err := logBatch(store, id, after)
	if err != nil {
		return "", after, err
	}
	return logTextOf(lines), next, nil
}

// clockOf is a log line's time as HH:MM:SS (UTC), the part the Files view and the stream show.
func clockOf(at string) string {
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return at
	}
	return t.UTC().Format("15:04:05")
}
