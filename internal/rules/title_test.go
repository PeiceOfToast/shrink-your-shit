// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// W4b: the audio track title is written only when the workflow's field is set; every default gives
// today's arguments byte for byte.
func TestTrackTitleArgs(t *testing.T) {
	info := h264(1920, 1080, 8000,
		media.Stream{Index: 1, Type: "audio", Codec: "ac3", Channels: 6, Language: "eng"},
		media.Stream{Index: 2, Type: "audio", Codec: "aac", Channels: 2, Language: "jpn"})
	p := DefaultSelectors(3).Template().Compile(info)
	today := strings.Join(p.FFmpegArgs("i", "o"), " ")
	if strings.Contains(today, "title=") {
		t.Errorf("no title is set, but: %s", today)
	}
	p.Title = "Movie (2019)"
	if n := p.AudioTrackCount(info); n != 2 {
		t.Errorf("%d audio tracks, want 2", n)
	}
	p.AudioTracks = p.AudioTrackCount(info)
	args := strings.Join(p.FFmpegArgs("i", "o"), " ")
	if !strings.Contains(args, "-metadata:s:a:0 title=Movie (2019)") || !strings.Contains(args, "-metadata:s:a:1 title=Movie (2019)") {
		t.Errorf("the title is not on every track: %s", args)
	}
	// the title is the only change
	stripped := strings.ReplaceAll(args, "-metadata:s:a:0 title=Movie (2019) ", "")
	stripped = strings.ReplaceAll(stripped, "-metadata:s:a:1 title=Movie (2019) ", "")
	if stripped != today {
		t.Errorf("title changed more than itself:\n got %s\nwant %s", stripped, today)
	}
}

func TestAudioTrackCount(t *testing.T) {
	info := h264(1920, 1080, 8000,
		media.Stream{Index: 1, Type: "audio", Codec: "ac3", Channels: 6},
		media.Stream{Index: 2, Type: "audio", Codec: "aac", Channels: 2},
		media.Stream{Index: 3, Type: "audio", Codec: "aac", Channels: 2})
	// no audio step: every track stays
	if n := (Plan{}).AudioTrackCount(info); n != 3 {
		t.Errorf("kept as is: %d, want 3", n)
	}
	// one dropped, one made from another
	p := Plan{AudioOrder: []int{1, 2}, Tracks: []TrackOp{
		{Index: 1, Drop: true},
		{Index: 2, Add: true, Encoder: "aac", Channels: 2},
	}}
	if n := p.AudioTrackCount(info); n != 2 {
		t.Errorf("dropped 1 and added 1: %d, want 2", n)
	}
}

func TestSrtNameFor(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "Movie.hevc.mkv")
	// nothing resolved (every job before W4b, and today's default): today's name
	p := Plan{Tracks: []TrackOp{{Index: 1, Type: "subtitle", Extract: "eng", ToFile: true}}}
	if got, want := p.SrtNameFor(final, 0), SrtName(final, "eng"); got != want {
		t.Errorf("%q, want %q", got, want)
	}
	// resolved by the run: the template's name
	p.SrtNames = []string{filepath.Join(dir, "Movie by language.srt")}
	if got, want := p.SrtNameFor(final, 0), p.SrtNames[0]; got != want {
		t.Errorf("%q, want %q", got, want)
	}
}
