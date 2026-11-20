// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"slices"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// The words a made track is named with: the layout, then the codec in brackets.
func TestTrackTitleWords(t *testing.T) {
	cases := []struct {
		codec    string
		channels int
		want     string
	}{
		{"aac", 2, "Stereo (AAC)"},
		{"eac3", 6, "Surround 5.1 (E-AC-3)"},
		{"ac3", 1, "Mono (AC-3)"},
		{"opus", 8, "Surround 7.1 (Opus)"},
		{"aac", 4, "4 channels (AAC)"},
		{"aac", 0, ""},
		{"", 2, ""},
	}
	for _, c := range cases {
		if got := trackTitle(c.codec, c.channels); got != c.want {
			t.Errorf("trackTitle(%q, %d) = %q, want %q", c.codec, c.channels, got, c.want)
		}
	}
}

// hasPair reports whether args holds a then b right after it.
func hasPair(args []string, a, b string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == a && args[i+1] == b {
			return true
		}
	}
	return false
}

// A title goes on the output audio track its -c:a:N is set for: the index counts the kept tracks, then the added.
func TestTitledTracksGetTheirOutputIndex(t *testing.T) {
	// a converted downmix, the first audio track
	p := Plan{AudioOrder: []int{1}, Tracks: []TrackOp{{Index: 1, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2, Title: "Stereo (AAC)"}}}
	args := p.trackArgs()
	if !hasPair(args, "-metadata:s:a:0", "title=Stereo (AAC)") {
		t.Errorf("converted downmix: want the title on a:0, got %v", args)
	}

	// track 1 dropped, so track 2 (converted) is the first output track
	p = Plan{AudioOrder: []int{1, 2}, Tracks: []TrackOp{
		{Index: 1, Type: "audio", Drop: true},
		{Index: 2, Type: "audio", Encoder: "eac3", Codec: "eac3", Kbps: 448, Channels: 6, Title: "Surround 5.1 (E-AC-3)"},
	}}
	args = p.trackArgs()
	if !hasPair(args, "-metadata:s:a:0", "title=Surround 5.1 (E-AC-3)") || !hasPair(args, "-c:a:0", "eac3") {
		t.Errorf("dropped track before: want the title on a:0 with the eac3 encoder, got %v", args)
	}

	// an added track comes after the kept audio, so it is a:1 when one track is kept
	p = Plan{AudioOrder: []int{1}, Tracks: []TrackOp{
		{Index: 1, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2, Add: true, Title: "Stereo (AAC)"},
	}}
	args = p.trackArgs()
	if !hasPair(args, "-metadata:s:a:1", "title=Stereo (AAC)") || !hasPair(args, "-map", "0:1") {
		t.Errorf("added track: want the title on a:1 after the kept track, got %v", args)
	}
	if hasPair(args, "-metadata:s:a:0", "title=Stereo (AAC)") {
		t.Errorf("added track: the kept track a:0 must not take the added track's title, got %v", args)
	}
}

// A track copied as it is gets no title from the plan; its own (from the source) stays.
func TestCopiedTrackGetsNoTitle(t *testing.T) {
	p := Plan{AudioOrder: []int{1, 2}, Tracks: []TrackOp{
		{Index: 2, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2, Title: "Stereo (AAC)"},
	}}
	args := p.trackArgs()
	if !hasPair(args, "-metadata:s:a:1", "title=Stereo (AAC)") {
		t.Errorf("converted track 2 is a:1: want its title there, got %v", args)
	}
	if slices.Contains(args, "-metadata:s:a:0") {
		t.Errorf("the copied track a:0 must get no title, got %v", args)
	}
}

// A track converted without a downmix (Channels 0) takes its count from the source, never more than it has.
func TestTitleFromTheSourceChannels(t *testing.T) {
	info := withAudio(media.Stream{Codec: "truehd", Channels: 6, Language: "eng"})
	p := Plan{AudioOrder: []int{1}, Tracks: []TrackOp{{Index: 1, Type: "audio", Encoder: "eac3", Codec: "eac3", Kbps: 448}}}
	p.titleTracks(info)
	if got := p.Tracks[0].Title; got != "Surround 5.1 (E-AC-3)" {
		t.Errorf("Channels 0 of a 5.1 source: title %q, want %q", got, "Surround 5.1 (E-AC-3)")
	}

	// the encoded count is what the file holds, even above the source's (a 5.1 upmixed to 7.1 is 7.1)
	p = Plan{Tracks: []TrackOp{{Index: 1, Type: "audio", Encoder: "eac3", Codec: "eac3", Kbps: 448, Channels: 8}}}
	p.titleTracks(info)
	if got := p.Tracks[0].Title; got != "Surround 7.1 (E-AC-3)" {
		t.Errorf("a count above the source: title %q, want the encoded %q", got, "Surround 7.1 (E-AC-3)")
	}

	// a mono source upmixed to stereo is Stereo, not the source's Mono (review2 P3a)
	mono := withAudio(media.Stream{Codec: "ac3", Channels: 1, Language: "eng"})
	p = Plan{Tracks: []TrackOp{{Index: 1, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2}}}
	p.titleTracks(mono)
	if got := p.Tracks[0].Title; got != "Stereo (AAC)" {
		t.Errorf("mono upmixed to stereo: title %q, want %q", got, "Stereo (AAC)")
	}

	// a source track with its own title keeps it: no automatic title is written (the source's is copied)
	titledSrc := withAudio(media.Stream{Codec: "flac", Channels: 6, Language: "eng", Title: "Director's commentary"})
	p = Plan{Tracks: []TrackOp{{Index: 1, Type: "audio", Encoder: "eac3", Codec: "eac3", Kbps: 448, Channels: 6}}}
	p.titleTracks(titledSrc)
	if got := p.Tracks[0].Title; got != "" {
		t.Errorf("a titled source track: title %q, want none so the source's title stays", got)
	}

	// two converted tracks from a mono source and a 5.1 source: each named for its own encode
	two := withAudio(media.Stream{Codec: "ac3", Channels: 1, Language: "eng"}, media.Stream{Codec: "flac", Channels: 6, Language: "jpn"})
	p = Plan{Tracks: []TrackOp{
		{Index: 1, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2},
		{Index: 2, Type: "audio", Encoder: "eac3", Codec: "eac3", Kbps: 448, Channels: 6},
	}}
	p.titleTracks(two)
	if p.Tracks[0].Title != "Stereo (AAC)" || p.Tracks[1].Title != "Surround 5.1 (E-AC-3)" {
		t.Errorf("two converted tracks: titles %q and %q", p.Tracks[0].Title, p.Tracks[1].Title)
	}

	p = Plan{Tracks: []TrackOp{
		{Index: 1, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2, Add: true},
		{Index: 1, Type: "audio", Drop: true},
	}}
	p.titleTracks(info)
	if p.Tracks[0].Title != "Stereo (AAC)" || p.Tracks[1].Title != "" {
		t.Errorf("an added stereo track gets its title, a dropped one none: got %q and %q", p.Tracks[0].Title, p.Tracks[1].Title)
	}
}

// An added track is a new track: from a titled source it gets the automatic name, not the source's (two tracks
// with one name would be wrong). A converted track of the same source keeps the source title (no name written).
func TestAddedTrackFromATitledSourceGetsItsName(t *testing.T) {
	titledSrc := withAudio(media.Stream{Codec: "dts", Channels: 6, Language: "eng", Title: "Old DTS title"})
	p := Plan{AudioOrder: []int{1}, Tracks: []TrackOp{
		{Index: 1, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2, Add: true},
		{Index: 1, Type: "audio", Encoder: "eac3", Codec: "eac3", Kbps: 448, Channels: 6},
	}}
	p.titleTracks(titledSrc)
	if got := p.Tracks[0].Title; got != "Stereo (AAC)" {
		t.Errorf("an added track from a titled source: title %q, want %q", got, "Stereo (AAC)")
	}
	if got := p.Tracks[1].Title; got != "" {
		t.Errorf("a converted track of a titled source: title %q, want none", got)
	}
}

// The titles are not part of the fingerprint: a converted file is not queued again for them, and the plan keeps them.
func TestTitlesLeaveTheFingerprint(t *testing.T) {
	plain := Plan{AudioOrder: []int{1}, Tracks: []TrackOp{{Index: 1, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2}}}
	titled := plain
	titled.Tracks = []TrackOp{{Index: 1, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2, Title: "Stereo (AAC)"}}
	a, b := Snapshot{Plan: plain}, Snapshot{Plan: titled}
	if a.Fingerprint() != b.Fingerprint() {
		t.Errorf("a title changed the fingerprint: %s and %s", a.Fingerprint(), b.Fingerprint())
	}
	if titled.Tracks[0].Title != "Stereo (AAC)" {
		t.Errorf("the fingerprint cleared the plan's title: %+v", titled.Tracks[0])
	}
}
