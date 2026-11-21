// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import "testing"

// indexOfPair is the position of the first "-map <spec>" in args after from, or -1.
func indexOfPair(args []string, from int, spec string) int {
	for i := from; i+1 < len(args); i++ {
		if args[i] == "-map" && args[i+1] == spec {
			return i
		}
	}
	return -1
}

// A font attachment is written after the streams a reorder moves: a file with styled subtitles (a font
// attachment) and an added track fails at mux on ffmpeg 7.1.5 when the attachment stays ahead of the added
// copy, so the attachment is taken out with the moved streams and mapped again behind them.
func TestAttachmentIsMappedAfterTheMovedStreams(t *testing.T) {
	// video 0, 5.1 audio 1 (the source of the added stereo AAC), subtitle 2, font attachment 3
	p := Plan{
		Video: VideoEncode{Codec: "libx265", CRF: 28}, AudioOrder: []int{1}, AudioTracks: 2,
		Tracks:        []TrackOp{{Index: 1, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2, Add: true}},
		SubtitleOrder: []int{2},
	}
	args := p.FFmpegArgs("i", "o")
	added := indexOfPair(args, 0, "0:1") // the added copy's remap: the last "-map 0:1"
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-map" && args[i+1] == "0:1" {
			added = i
		}
	}
	unmapped := indexOfPair(args, 0, "-0:t")
	remapped := indexOfPair(args, 0, "0:t?")
	if unmapped < 0 || remapped < 0 {
		t.Fatalf("the attachment is not taken out and mapped again: %v", args)
	}
	if remapped < added {
		t.Errorf("the attachment is mapped at %d, before the added copy at %d: %v", remapped, added, args)
	}
}

// With no added track nothing moves, so the attachment keeps the plain "-map 0" and is not taken out.
func TestAttachmentIsKeptWhenNothingMoves(t *testing.T) {
	// subtitle 2 is dropped (a track op, not an added one): the streams are taken out, nothing is moved
	p := Plan{
		Video: VideoEncode{Codec: "libx265", CRF: 28}, AudioOrder: []int{1}, AudioTracks: 1,
		Tracks: []TrackOp{{Index: 2, Type: "subtitle", Drop: true}},
	}
	args := p.FFmpegArgs("i", "o")
	if indexOfPair(args, 0, "0") < 0 {
		t.Fatalf("the source is not mapped whole: %v", args)
	}
	if indexOfPair(args, 0, "-0:t") >= 0 || indexOfPair(args, 0, "0:t?") >= 0 {
		t.Errorf("an attachment is taken out with no reorder: %v", args)
	}
}

// Two added tracks from different sources: the pivot is the earliest source (5, 1 and 3 are the sources of
// the copies, so the streams after source 1 move). dev-review's ask 1: the latest source instead gives
// [0 1 2 3 1 3 4].
func TestTwoAddedTracksPivotIsTheEarliestSource(t *testing.T) {
	p := Plan{
		Video: VideoEncode{Codec: "libx265", CRF: 28}, AudioOrder: []int{1, 2, 3, 4}, AudioTracks: 6,
		Tracks: []TrackOp{
			{Index: 1, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2, Add: true},
			{Index: 3, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2, Add: true},
		},
	}
	args := p.FFmpegArgs("i", "o")
	got := outputStreams(args, 5)
	want := []int{0, 1, 1, 2, 3, 3, 4}
	if !sameInts(got, want) {
		t.Fatalf("output streams %v, want %v\nargs: %v", got, want, args)
	}
}
