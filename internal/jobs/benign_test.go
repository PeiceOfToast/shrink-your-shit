// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"strings"
	"testing"
)

// R8: a message we have seen in CI and CT logs on files that converted and passed every check is kept, with a
// short note saying it is harmless.
func TestBenignLinesAreLabelled(t *testing.T) {
	for _, line := range []string{
		"[h264 @ 0x55] Last message repeated 7 times",
		"[h264 @ 0x55] non-existing PPS 0 referenced",
		"[h264 @ 0x55] non-existing SPS 0 referenced in buffering period",
		"[mpeg2video @ 0x55] no frame!",
		"[h264 @ 0x55] mmco: unref short failure",
		"[h264 @ 0x55] Increasing reorder buffer to 2",
		"[matroska @ 0x55] Timestamps are unset in a packet for stream 0.",
		"x265 [info]: HEVC encoder version 3.5",
	} {
		note := BenignNote(line)
		if note == "" {
			t.Errorf("%q: no note", line)
			continue
		}
		got := BenignText(line)
		if !strings.HasPrefix(got, line+" (harmless: ") || !strings.HasSuffix(got, ")") {
			t.Errorf("%q: labelled as %q", line, got)
		}
	}
}

// R8 only covers messages that are harmless. A real error stays exactly as ffmpeg printed it, so nobody is
// taught to ignore one: an x265 error, an invalid-data line, and an unknown warning are not labelled.
func TestRealErrorsAreNotLabelled(t *testing.T) {
	for _, line := range []string{
		"x265 [error]: unable to open encoder",
		"[h264 @ 0x55] Invalid data found when processing input",
		"[h264 @ 0x55] error while decoding MB 12 3, bytestream -5",
		"[libx265 @ 0x55] Error initializing output stream",
	} {
		if note := BenignNote(line); note != "" {
			t.Errorf("%q: labelled harmless (%q)", line, note)
		}
		if got := BenignText(line); got != line {
			t.Errorf("%q: changed to %q", line, got)
		}
	}
}
