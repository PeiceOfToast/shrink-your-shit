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

// logText is job id's log lines after sequence number after, as the stream and the Files view show them ("15:04:05
// text" a line), and the number to read from next (the last line's; after again when there is none).
func logText(store *jobs.Store, id, after int64) (text string, next int64, err error) {
	lines, err := store.LogLinesAfter(id, after, logPiece)
	if err != nil {
		return "", after, err
	}
	var b strings.Builder
	next = after
	for _, l := range lines {
		at, _ := time.Parse(time.RFC3339, l.At)
		fmt.Fprintf(&b, "%s %s\n", at.UTC().Format("15:04:05"), l.Text)
		next = l.Seq
	}
	return b.String(), next, nil
}
