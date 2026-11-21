// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"reflect"
	"strings"
	"testing"
)

// titlesByStream collects the title= metadata of each audio stream in trackArgs' output, by stream spec.
func titlesByStream(args []string) map[string][]string {
	out := map[string][]string{}
	for i := 0; i+1 < len(args); i++ {
		if strings.HasPrefix(args[i], "-metadata:s:a:") && strings.HasPrefix(args[i+1], "title=") {
			out[args[i]] = append(out[args[i]], args[i+1])
		}
	}
	return out
}

// Title precedence, one rule (F6 round 2): the workflow's title on every audio track first; else each track's own
// title (a converted or added track's automatic name; a titled source's kept title is copied by ffmpeg). No track
// ever gets two title= values.
func TestTitlePrecedence(t *testing.T) {
	// output audio: a converted track of source 1 (named by its codec), source 2 kept as it is, and an added
	// track made from source 1 (named for itself)
	tracks := func() []TrackOp {
		return []TrackOp{
			{Index: 1, Type: "audio", Encoder: "eac3", Codec: "eac3", Kbps: 384, Channels: 6, Title: "Surround 5.1 (E-AC-3)"},
			{Index: 2, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2, Title: ""}, // a titled source, converted: ffmpeg copies its title
			{Index: 1, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2, Add: true, Title: "Stereo (AAC)"},
		}
	}
	base := func() Plan {
		return Plan{Tracks: tracks(), AudioOrder: []int{1, 2}, AudioTracks: 3}
	}

	t.Run("a workflow title goes on every track, once each", func(t *testing.T) {
		p := base()
		p.Title = "Movie"
		got := titlesByStream(p.trackArgs())
		want := map[string][]string{
			"-metadata:s:a:0": {"title=Movie"},
			"-metadata:s:a:1": {"title=Movie"},
			"-metadata:s:a:2": {"title=Movie"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("workflow title: %v, want %v", got, want)
		}
	})

	t.Run("without a workflow title, each track's own title, once", func(t *testing.T) {
		got := titlesByStream(base().trackArgs())
		want := map[string][]string{
			"-metadata:s:a:0": {"title=Surround 5.1 (E-AC-3)"},
			"-metadata:s:a:1": {"title=Stereo (AAC)"}, // the copy sits straight after its source (slice 17)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("own titles: %v, want %v (a titled source's kept title gets no title=)", got, want)
		}
	})
}
