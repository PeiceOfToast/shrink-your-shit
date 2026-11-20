// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import "regexp"

// benignLine is one entry of the harmless-message table (log-spec R8): the pattern and the note the job log
// adds after the line.
type benignLine struct {
	re   *regexp.Regexp
	note string
}

// benignLines are tool messages we have seen in CI and CT logs on files that converted and passed every
// check: the decoder starting before the first keyframe of a broadcast recording, ffmpeg's own repeat
// marker, the H.264 reference messages, x265's log lines and the timestamp guess. Only messages observed
// there belong here; a guess would teach readers to ignore a real error. A message that points at damage in
// the original is not here (sourceDamage counts those): the note never covers an encode that failed.
var benignLines = []benignLine{
	{regexp.MustCompile(`(?i)last message repeated`),
		"ffmpeg repeats its last message here instead of printing it again"},
	{regexp.MustCompile(`(?i)non-existing (pps|sps)`),
		"the recording starts in the middle of a group of pictures; the decoder is waiting for the first keyframe"},
	{regexp.MustCompile(`(?i)no frame!`),
		"the decoder had no picture to show at that point"},
	{regexp.MustCompile(`(?i)mmco: unref short failure`),
		"a reference-management note from the H.264 decoder"},
	{regexp.MustCompile(`(?i)increasing reorder buffer`),
		"the decoder made room for frames that arrive out of order"},
	{regexp.MustCompile(`(?i)timestamps are unset`),
		"ffmpeg guessed the timestamps; the new file is timed the same way ffmpeg read it"},
	{regexp.MustCompile(`(?i)^x265 \[info\]`),
		"a line from x265's own log, not an error"}, // only [info]: an x265 [error] is never harmless
}

// BenignNote returns the short note for a known harmless ffmpeg or ffprobe line ("harmless: ..." in the job
// log), or "" for every other line (log-spec R8).
func BenignNote(line string) string {
	for _, b := range benignLines {
		if b.re.MatchString(line) {
			return b.note
		}
	}
	return ""
}

// BenignText is a tool line as the job log keeps it: the line itself, with its note when it is known to be
// harmless. A line with no note is returned unchanged.
func BenignText(line string) string {
	if note := BenignNote(line); note != "" {
		return line + " (harmless: " + note + ")"
	}
	return line
}
