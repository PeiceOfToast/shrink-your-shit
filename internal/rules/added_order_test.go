// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"strconv"
	"strings"
	"testing"
)

// outputStreams is the source stream index of each stream ffmpeg writes, read from the -map arguments in
// order: "0" maps all n streams, "0:N" one stream, and a negative map takes out the streams already mapped.
// Data streams are not in these test files, so "-0:d" is ignored.
func outputStreams(args []string, n int) []int {
	var out []int
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "-map" {
			continue
		}
		spec := args[i+1]
		i++
		neg := strings.HasPrefix(spec, "-")
		spec = strings.TrimPrefix(spec, "-")
		var from []int
		switch {
		case spec == "0":
			for j := 0; j < n; j++ {
				from = append(from, j)
			}
		case strings.HasPrefix(spec, "0:"):
			j, err := strconv.Atoi(spec[2:])
			if err != nil {
				continue
			}
			from = []int{j}
		default:
			continue
		}
		if !neg {
			out = append(out, from...)
			continue
		}
		var rest []int
		for _, s := range out {
			gone := false
			for _, f := range from {
				gone = gone || f == s
			}
			if !gone {
				rest = append(rest, s)
			}
		}
		out = rest
	}
	return out
}

// Slice 17: an added track sits straight after the track it was made from, so in the s6 shape (one video,
// E-AC-3 5.1 kept, the stereo AAC made from the E-AC-3, 37 subtitles after the audio) the AAC is output 2.
func TestAddedTrackSitsStraightAfterItsSource(t *testing.T) {
	for _, c := range []struct {
		name  string
		plan  Plan
		count int // streams in the source
		want  []int
	}{
		{
			name: "s6 with 37 subtitles",
			plan: Plan{
				Video: VideoEncode{Codec: "libx265", CRF: 28}, AudioOrder: []int{1}, AudioTracks: 2,
				Tracks:        []TrackOp{{Index: 1, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2, Add: true}},
				SubtitleOrder: subtitleIndexes(2, 37),
			},
			count: 39,
			want:  append([]int{0, 1, 1}, subtitleIndexes(2, 37)...),
		},
		{
			name: "s6 without subtitles",
			plan: Plan{
				Video: VideoEncode{Codec: "libx265", CRF: 28}, AudioOrder: []int{1}, AudioTracks: 2,
				Tracks: []TrackOp{{Index: 1, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2, Add: true}},
			},
			count: 2,
			want:  []int{0, 1, 1},
		},
		{
			// a kept audio track after the source: the copy goes between them, and that track's a:N moves on
			name: "a later audio track after the source, with subtitles",
			plan: Plan{
				Video: VideoEncode{Codec: "libx265", CRF: 28}, AudioOrder: []int{1, 2}, AudioTracks: 3,
				Tracks:        []TrackOp{{Index: 1, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2, Add: true}},
				SubtitleOrder: subtitleIndexes(3, 4),
			},
			count: 7,
			want:  []int{0, 1, 1, 2, 3, 4, 5, 6},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			args := c.plan.FFmpegArgs("i", "o")
			got := outputStreams(args, c.count)
			if !sameInts(got, c.want) {
				t.Fatalf("output streams %v, want %v\nargs: %s", got, c.want, strings.Join(args, " "))
			}
		})
	}
}

// The added AAC is a:1 in the s6 shape: its codec and channels are its own, and the kept E-AC-3 stays a:0.
func TestAddedTrackKeepsItsOwnAudioSpecifier(t *testing.T) {
	p := Plan{
		Video: VideoEncode{Codec: "libx265", CRF: 28}, AudioOrder: []int{1}, AudioTracks: 2,
		Tracks:        []TrackOp{{Index: 1, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2, Add: true}},
		SubtitleOrder: subtitleIndexes(2, 37),
	}
	args := p.trackArgs()
	if !hasPair(args, "-c:a:1", "aac") || !hasPair(args, "-ac:a:1", "2") {
		t.Errorf("the added AAC is a:1 with its own encoder and channels: %v", args)
	}
	if hasPair(args, "-c:a:0", "aac") {
		t.Errorf("the kept E-AC-3 a:0 must not take the added track's encoder: %v", args)
	}
}

// subtitleIndexes is the stream indexes first..first+count-1.
func subtitleIndexes(first, count int) []int {
	var out []int
	for i := 0; i < count; i++ {
		out = append(out, first+i)
	}
	return out
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
