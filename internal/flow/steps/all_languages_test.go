// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// "Keep every language" on keeps every track, even with a list of languages set (review item 6): the API is open, so
// the engine does not rely on the page never writing both. Red-first: the filter keeps only the French track.
func TestAllLanguagesKeepsEveryTrackWhateverTheListSays(t *testing.T) {
	tracks := []media.AudioTrack{
		{Index: 0, Codec: "aac", Channels: 2, Language: "eng"},
		{Index: 1, Codec: "aac", Channels: 2, Language: "fra"},
		{Index: 2, Codec: "aac", Channels: 2, Language: "deu"},
	}
	set := AudioSettings{AllLanguages: true, Languages: []string{"fra"}}
	got, _ := chooseTracks(set, tracks)
	if len(got) != 3 {
		t.Errorf("kept %d of 3 tracks with every language on, want all 3", len(got))
	}
}

// With the flag off, the list is the filter (the same selection as before the flag).
func TestNotAllLanguagesKeepsOnlyTheListsTracks(t *testing.T) {
	tracks := []media.AudioTrack{
		{Index: 0, Codec: "aac", Channels: 2, Language: "eng"},
		{Index: 1, Codec: "aac", Channels: 2, Language: "fra"},
		{Index: 2, Codec: "aac", Channels: 2, Language: "deu"},
	}
	set := AudioSettings{AllLanguages: false, Languages: []string{"fra"}}
	got, _ := chooseTracks(set, tracks)
	if len(got) != 1 || got[0].Language != "fra" {
		t.Errorf("kept %v, want the French track only", got)
	}
}

// Item 5: "my languages" that resolves to nothing keeps every language, and the run says so (decisions.md, 11-28).
// Red-first: without the note the run keeps every track silently.
func TestMyLanguagesWithNothingSetKeepsEveryTrackAndSaysSo(t *testing.T) {
	tracks := []media.AudioTrack{
		{Index: 0, Codec: "aac", Channels: 2, Language: "eng"},
		{Index: 1, Codec: "aac", Channels: 2, Language: "fra"},
	}
	set := AudioSettings{AllLanguages: false, Languages: []string{}}
	got, notes := chooseTracks(set, tracks)
	if len(got) != 2 {
		t.Errorf("kept %d of 2 tracks, want every track", len(got))
	}
	found := false
	for _, n := range notes {
		if n == noMineEmptyNote {
			found = true
		}
	}
	if !found {
		t.Errorf("notes = %v, want the note that my languages is empty", notes)
	}
}
