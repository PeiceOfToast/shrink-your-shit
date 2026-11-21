// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"strings"
	"testing"
)

// F9: a re-encoded stream's statistics are cleared (they describe the old stream); a copied stream keeps its own.
func TestStaleStatsClearedOnReencodedStreamsOnly(t *testing.T) {
	p := Plan{
		Video:       VideoEncode{Codec: "libx265", CRF: 28},
		AudioOrder:  []int{1, 2},
		AudioTracks: 2,
		Tracks: []TrackOp{
			{Index: 1, Type: "audio", Encoder: "aac", Kbps: 128, Channels: 2}, // converted: re-encoded
			{Index: 2, Type: "audio"}, // copied: kept as it is
			{Index: 1, Type: "audio", Encoder: "aac", Kbps: 128, Channels: 2, Add: true}, // added from track 1: a re-encode too
			{Index: 3, Type: "subtitle", Encoder: "srt"},                                 // converted subtitle: re-encoded, so cleared
			{Index: 4, Type: "subtitle"},                                                 // copied subtitle: kept
		},
		SubtitleOrder: []int{3, 4},
	}
	args := strings.Join(p.FFmpegArgs("i", "o"), " ")
	for _, want := range []string{"-metadata:s:v:0 BPS= ", "-metadata:s:v:0 NUMBER_OF_BYTES= ", "-metadata:s:v:0 BPS-eng= ",
		"-metadata:s:a:0 BPS= ", "-metadata:s:a:0 _STATISTICS_TAGS= ", "-metadata:s:a:1 BPS= ", "-metadata:s:s:0 BPS= "} {
		if !strings.Contains(args+" ", want) {
			t.Errorf("re-encoded stream: %q missing from %s", want, args)
		}
	}
	if strings.Contains(args, "-metadata:s:a:2 BPS") { // the copied track is a:2 now, after the added copy a:1
		t.Errorf("a copied audio stream must keep its statistics: %s", args)
	}
	if strings.Contains(args, "-metadata:s:s:1 BPS") {
		t.Errorf("a copied subtitle stream must keep its statistics: %s", args)
	}

	// a copied video (repackaged): its statistics are left alone, the converted audio's are still cleared
	p.Video.Copy = true
	args = strings.Join(p.FFmpegArgs("i", "o"), " ")
	if strings.Contains(args, "-metadata:s:v:0 BPS") {
		t.Errorf("a copied video must keep its statistics: %s", args)
	}
	if !strings.Contains(args, "-metadata:s:a:0 BPS= ") {
		t.Errorf("the converted audio is still re-encoded, but its statistics are not cleared: %s", args)
	}
}

// staleVideoArgs is staleStatArgs("v:0"), written out: the arguments every re-encoded video carries (F9).
const staleVideoArgs = "-metadata:s:v:0 BPS= -metadata:s:v:0 BPS-eng= -metadata:s:v:0 NUMBER_OF_BYTES= -metadata:s:v:0 NUMBER_OF_BYTES-eng= -metadata:s:v:0 NUMBER_OF_FRAMES= -metadata:s:v:0 NUMBER_OF_FRAMES-eng= -metadata:s:v:0 DURATION= -metadata:s:v:0 DURATION-eng= -metadata:s:v:0 _STATISTICS_TAGS= -metadata:s:v:0 _STATISTICS_TAGS-eng= -metadata:s:v:0 _STATISTICS_WRITING_APP= -metadata:s:v:0 _STATISTICS_WRITING_APP-eng= -metadata:s:v:0 _STATISTICS_WRITING_DATE_UTC= -metadata:s:v:0 _STATISTICS_WRITING_DATE_UTC-eng="
