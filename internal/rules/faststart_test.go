// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"slices"
	"testing"
)

// An MP4-family output is written with its index at the front (-movflags +faststart), the last option before the
// output file; an MKV or WebM output gets no movflags at all.
func TestFaststartOnMP4FamilyOutputs(t *testing.T) {
	p := Plan{Video: VideoEncode{Index: 0, Codec: "libx264", CRF: 20}}
	for _, out := range []string{".Movie.1.syp-tmp/new.mp4", ".Movie.1.syp-tmp/new.m4v", ".Movie.1.syp-tmp/new.MOV"} {
		args := p.FFmpegArgs("Movie.mkv", out)
		n := len(args)
		if n < 3 || args[n-3] != "-movflags" || args[n-2] != "+faststart" || args[n-1] != out {
			t.Errorf("%s: want -movflags +faststart right before the output, got %v", out, args)
		}
	}
}

func TestNoFaststartOnMKVOrWebM(t *testing.T) {
	p := Plan{Video: VideoEncode{Index: 0, Codec: "libx264", CRF: 20}}
	for _, out := range []string{".Movie.1.syp-tmp/new.mkv", ".Movie.1.syp-tmp/new.webm"} {
		if args := p.FFmpegArgs("Movie.mp4", out); slices.Contains(args, "-movflags") {
			t.Errorf("%s: no movflags for this container, got %v", out, args)
		}
	}
}

// A repackaged file (video copied, Video.Copy) is an MP4 output too: the flag goes on every MP4-family output.
func TestFaststartOnACopiedVideoToMP4(t *testing.T) {
	p := Plan{Video: VideoEncode{Index: 0, Codec: "libx264", Copy: true}}
	args := p.FFmpegArgs("Movie.mkv", ".Movie.1.syp-tmp/new.mp4")
	if i := slices.Index(args, "-movflags"); i < 0 || i+1 >= len(args) || args[i+1] != "+faststart" {
		t.Errorf("copy to mp4: want +faststart, got %v", args)
	}
}

// The fingerprint hashes the Plan, not the arguments: building an MP4's arguments leaves it as it was, so files
// already converted are not queued again for the flag.
func TestFaststartLeavesTheFingerprint(t *testing.T) {
	p := Plan{AudioOrder: []int{1}, Tracks: []TrackOp{{Index: 1, Type: "audio", Encoder: "aac", Codec: "aac", Kbps: 128, Channels: 2}},
		Video: VideoEncode{Index: 0, Codec: "libx264", CRF: 20}, Container: "mp4"}
	before := Snapshot{Plan: p}.Fingerprint()
	_ = p.FFmpegArgs("Movie.mkv", ".Movie.1.syp-tmp/new.mp4")
	if after := (Snapshot{Plan: p}).Fingerprint(); after != before {
		t.Errorf("the fingerprint changed with the arguments: %s and %s", before, after)
	}
}
