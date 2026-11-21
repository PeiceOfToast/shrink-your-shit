// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

func TestNeedsTenBit(t *testing.T) {
	src := func(depth int) *media.Info {
		return &media.Info{Streams: []media.Stream{{Type: "video", Codec: "hevc", BitDepth: depth}}}
	}
	hdr10 := &HDR{Transfer: "smpte2084"}
	hlg := &HDR{Transfer: "arib-std-b67"}
	for _, c := range []struct {
		name   string
		plan   Plan
		source *media.Info
		want   bool
	}{
		{"HDR10", Plan{Video: VideoEncode{Codec: "libx265", HDR: hdr10}}, nil, true},
		{"HLG", Plan{Video: VideoEncode{Codec: "libx265", HDR: hlg}}, nil, true},
		{"10-bit SDR with TenBit set", Plan{Video: VideoEncode{Codec: "libx265", TenBit: true}}, nil, true},
		{"10-bit SDR plan without TenBit, 10-bit source (older server)", Plan{Video: VideoEncode{Codec: "libx265"}}, src(10), true},
		{"8-bit source", Plan{Video: VideoEncode{Codec: "libx265"}}, src(8), false},
		{"copied video from a 10-bit source", Plan{Video: VideoEncode{Copy: true}}, src(10), false},
		{"copied video with a codec set, 10-bit source", Plan{Video: VideoEncode{Codec: "libx265", Copy: true}}, src(10), false},
		{"nil source, no TenBit", Plan{Video: VideoEncode{Codec: "libx265"}}, nil, false},
		{"no codec, 10-bit source", Plan{Video: VideoEncode{}}, src(10), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := NeedsTenBit(c.plan, c.source); got != c.want {
				t.Fatalf("NeedsTenBit = %v, want %v", got, c.want)
			}
		})
	}
}
