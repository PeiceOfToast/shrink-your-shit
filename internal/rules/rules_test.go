// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

func TestMatch(t *testing.T) {
	r := Default()
	cases := []struct {
		name string
		info media.Info
		want bool
	}{
		{"h264 high bitrate", media.Info{VideoCodec: "h264", BitrateKbps: 8000}, true},
		{"h264 exactly at threshold", media.Info{VideoCodec: "h264", BitrateKbps: 4000}, true},
		{"h264 low bitrate", media.Info{VideoCodec: "h264", BitrateKbps: 1500}, false},
		{"already hevc", media.Info{VideoCodec: "hevc", BitrateKbps: 9000}, false},
		{"other codec", media.Info{VideoCodec: "mpeg2video", BitrateKbps: 9000}, false},
	}
	for _, c := range cases {
		got, why := r.Match(c.info)
		if got != c.want {
			t.Errorf("%s: got %v (%s), want %v", c.name, got, why, c.want)
		}
		if why == "" {
			t.Errorf("%s: empty reason", c.name)
		}
	}
}

func TestCustomRule(t *testing.T) {
	r := Rule{FromCodec: "mpeg4", MinBitrateKbps: 0}
	if ok, _ := r.Match(media.Info{VideoCodec: "mpeg4", BitrateKbps: 10}); !ok {
		t.Error("custom rule should match")
	}
}
