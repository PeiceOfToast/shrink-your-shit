// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"regexp"
	"strings"
)

// logLevelRe finds the level ffmpeg puts in front of each log line when asked for it (-loglevel
// level+verbose): "[error] " at the start, or after the log context, which can be more than one part
// ("[vist#0:0/h264 @ 0x...] [dec:h264 @ 0x...] [error] ...").
var logLevelRe = regexp.MustCompile(`^(?:\[[^\]]*\] )*\[(quiet|panic|fatal|error|warning|info|verbose|debug|trace)\] `)

// ErrorLines keeps only the error-level lines of a run logged with -loglevel level+verbose, with the level
// marker removed: the text then reads as -v error writes it, which is what the job's checks judge. A line
// with no marker is kept as it is (a test runner, a driver's chatter, or an older ffmpeg's plain error text).
func ErrorLines(raw []byte) string {
	var kept []string
	for _, l := range strings.Split(string(raw), "\n") {
		m := logLevelRe.FindStringSubmatchIndex(l)
		if m == nil {
			kept = append(kept, l)
			continue
		}
		switch l[m[2]:m[3]] {
		case "panic", "fatal", "error":
			kept = append(kept, l[:m[2]-1]+l[m[1]:]) // drop the marker and its space
		}
	}
	return strings.Join(kept, "\n")
}
