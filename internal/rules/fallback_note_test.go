// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"strings"
	"testing"
)

// Under the Convert step's only choice the job log never names the processor, for any reason the graphics
// encoder is not used (the test failed, 10-bit, HDR, mastering data). Other choices keep the clause.
func TestUnderOnlyTheFallbackNoteNeverNamesTheProcessor(t *testing.T) {
	cases := []struct {
		name   string
		main   string
		need10 bool
		hdr    *HDR
		caps   []map[string]any
	}{
		{"encoder did not pass its test", "hevc_qsv", false, nil, gpuCaps(nil)},
		{"HDR needs a 10-bit encoder", "hevc_qsv", false, hdrSource(true), gpuCaps([]string{"hevc_qsv"})},
		{"a 10-bit SDR file needs a 10-bit encoder", "hevc_qsv", true, nil, gpuCaps([]string{"hevc_qsv"})},
		{"HDR10 with mastering data", "hevc_nvenc", false, hdrSource(true), gpuCaps([]string{"hevc_nvenc"}, "hevc_nvenc")},
	}
	for _, c := range cases {
		enc, why := EncoderWhy("libx265", c.main, c.need10, c.hdr, c.caps)
		if enc != "" || why == "" {
			t.Fatalf("%s: got %q, %q; want the processor with a reason", c.name, enc, why)
		}
		if got := FallbackNote(VideoEncode{Hardware: "only"}, why); strings.Contains(got, "processor") || got == "" {
			t.Errorf("%s under only: note %q; want the reason without the processor", c.name, got)
		}
		if got := FallbackNote(VideoEncode{Hardware: "auto"}, why); !strings.HasSuffix(got, "; using the processor") {
			t.Errorf("%s under auto: note %q; want the processor clause", c.name, got)
		}
		if _, note := EncoderFor("libx265", c.main, c.need10, c.hdr, c.caps); note != UsingTheProcessor(why) {
			t.Errorf("%s: EncoderFor note %q, want %q", c.name, note, UsingTheProcessor(why))
		}
	}
}
