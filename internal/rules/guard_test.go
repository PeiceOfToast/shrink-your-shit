// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// guardTemplates and guardFiles: every kind of workflow there is, over files of each sort.
func guardTemplates() map[string]Template {
	out := map[string]Template{}
	for q := 1; q <= 5; q++ {
		out[fmt.Sprintf("slider%d", q)] = SliderTemplate(q)
	}
	sel := func(name string, f func(*Selectors)) {
		s := DefaultSelectors(3)
		f(&s)
		out[name] = s.Template()
	}
	sel("selectors-default", func(*Selectors) {})
	sel("selectors-mp4", func(s *Selectors) { s.FileType = "mp4" })
	sel("selectors-keep-medium", func(s *Selectors) { s.FileType, s.MaxBitrate = "keep", "medium" })
	sel("selectors-720-stereo", func(s *Selectors) { s.Resolution, s.Audio = "720", "stereo_aac" })
	sel("selectors-beside", func(s *Selectors) { s.AfterConverting = "beside" })
	sel("selectors-folder-next", func(s *Selectors) { s.AfterConverting, s.FolderMode = "folder", NextToLibrary })
	sel("selectors-dv", func(s *Selectors) { s.IncludeDolbyVision = true })
	out["legacy-replace"] = LegacyTemplate(Default(), "replace")
	out["legacy-beside"] = LegacyTemplate(Default(), "beside")
	return out
}

func guardFiles() map[string]media.Info {
	aac := media.Stream{Index: 1, Type: "audio", Codec: "aac", Channels: 2, BitrateKbps: 192, Language: "eng"}
	ac3 := media.Stream{Index: 2, Type: "audio", Codec: "ac3", Channels: 6, BitrateKbps: 640, Language: "fre"}
	srt := media.Stream{Index: 3, Type: "subtitle", Codec: "subrip", Language: "eng"}
	v := func(codec string, w, h int, fps float64) media.Stream {
		return media.Stream{Index: 0, Type: "video", Codec: codec, Width: w, Height: h, FrameRate: fps, PixFmt: "yuv420p"}
	}
	hdr := v("hevc", 3840, 2160, 23.976)
	hdr.PixFmt, hdr.ColorTransfer, hdr.ColorPrimaries = "yuv420p10le", "smpte2084", "bt2020"
	return map[string]media.Info{
		"h264-1080-mkv": {Container: "matroska,webm", Ext: ".mkv", VideoCodec: "h264", BitrateKbps: 8000, DurationSec: 600, SizeBytes: 600 << 20, Streams: []media.Stream{v("h264", 1920, 1080, 24), aac, ac3, srt}},
		"hevc-4k-hdr":   {Container: "matroska,webm", Ext: ".mkv", VideoCodec: "hevc", BitrateKbps: 30000, DurationSec: 600, SizeBytes: 2 << 30, Streams: []media.Stream{hdr, aac}},
		"av1-mp4":       {Container: "mov,mp4,m4a,3gp,3g2,mj2", Ext: ".mp4", VideoCodec: "av1", BitrateKbps: 3000, DurationSec: 600, SizeBytes: 200 << 20, Streams: []media.Stream{v("av1", 1920, 1080, 30), aac}},
		"mpeg4-avi":     {Container: "avi", Ext: ".avi", VideoCodec: "mpeg4", BitrateKbps: 1500, DurationSec: 600, SizeBytes: 100 << 20, Streams: []media.Stream{v("mpeg4", 720, 400, 25), aac}},
	}
}

// guardPrints is every (workflow, file) pair's Plan fingerprint and skip code.
func guardPrints() map[string]string {
	out := map[string]string{}
	for tn, t := range guardTemplates() {
		t.Languages = "eng"
		for fn, info := range guardFiles() {
			p := t.Compile(info)
			out[tn+"/"+fn] = Snapshot{Plan: p}.Fingerprint() + " " + p.SkipCode
		}
	}
	return out
}

// V6a's guard: a straight workflow's Plans are what they were before branches existed (golden values
// taken on the commit before V6a). SYP_GUARD=print prints them.
func TestStraightPlansUnchanged(t *testing.T) {
	got := guardPrints()
	if os.Getenv("SYP_GUARD") == "print" {
		var keys []string
		for k := range got {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("\t%q: %q,\n", k, got[k])
		}
		return
	}
	if len(got) != len(guardGolden) {
		t.Fatalf("%d pairs, golden has %d", len(got), len(guardGolden))
	}
	for k, want := range guardGolden {
		if got[k] != want {
			t.Errorf("%s: %s, was %s", k, got[k], want)
		}
	}
	for tn, tm := range guardTemplates() { // and a straight Template's JSON says nothing of branches
		if b, _ := json.Marshal(tm); strings.Contains(string(b), "branches") {
			t.Errorf("%s: %s", tn, b)
		}
	}
}

// guardGolden: taken on e9943aa (V5c, the commit before V6a) with SYP_GUARD=print.
var guardGolden = map[string]string{
	"legacy-beside/av1-mp4":               "faa600fca4f271be already_hevc",
	"legacy-beside/h264-1080-mkv":         "faa600fca4f271be ",
	"legacy-beside/hevc-4k-hdr":           "ccbe3f66ecada98f already_hevc",
	"legacy-beside/mpeg4-avi":             "faa600fca4f271be format_not_converted",
	"legacy-replace/av1-mp4":              "087812f06ca7cd44 already_hevc",
	"legacy-replace/h264-1080-mkv":        "087812f06ca7cd44 ",
	"legacy-replace/hevc-4k-hdr":          "31bb028624993cdc already_hevc",
	"legacy-replace/mpeg4-avi":            "087812f06ca7cd44 format_not_converted",
	"selectors-720-stereo/av1-mp4":        "003a48eb6938850b modern_format",
	"selectors-720-stereo/h264-1080-mkv":  "13600252bde58a37 ",
	"selectors-720-stereo/hevc-4k-hdr":    "656c57937885832d ",
	"selectors-720-stereo/mpeg4-avi":      "f21de8abde304b3e ",
	"selectors-beside/av1-mp4":            "2fd2b8e604368ad7 modern_format",
	"selectors-beside/h264-1080-mkv":      "3b476f744643e73a ",
	"selectors-beside/hevc-4k-hdr":        "e92844323578c0c4 already_in_format",
	"selectors-beside/mpeg4-avi":          "359e2549eeed2aa8 ",
	"selectors-default/av1-mp4":           "960cd2dd2aed5d25 modern_format",
	"selectors-default/h264-1080-mkv":     "131eb140619b7124 ",
	"selectors-default/hevc-4k-hdr":       "728eef9bdd34058b already_in_format",
	"selectors-default/mpeg4-avi":         "f21de8abde304b3e ",
	"selectors-dv/av1-mp4":                "960cd2dd2aed5d25 modern_format",
	"selectors-dv/h264-1080-mkv":          "131eb140619b7124 ",
	"selectors-dv/hevc-4k-hdr":            "728eef9bdd34058b already_in_format",
	"selectors-dv/mpeg4-avi":              "f21de8abde304b3e ",
	"selectors-folder-next/av1-mp4":       "b6c1805396d84c2b modern_format",
	"selectors-folder-next/h264-1080-mkv": "3e12d077acc845ee ",
	"selectors-folder-next/hevc-4k-hdr":   "20bfffd8754c051f already_in_format",
	"selectors-folder-next/mpeg4-avi":     "2d3c54dd909abb86 ",
	"selectors-keep-medium/av1-mp4":       "60587a1d00191bdc modern_format",
	"selectors-keep-medium/h264-1080-mkv": "b1ed8b1bcb250622 ",
	"selectors-keep-medium/hevc-4k-hdr":   "97ecda6c61d5fd0d ",
	"selectors-keep-medium/mpeg4-avi":     "9d7bb83c61aba41a ",
	"selectors-mp4/av1-mp4":               "eca65f76d1382d15 modern_format",
	"selectors-mp4/h264-1080-mkv":         "069339f33cec6081 ",
	"selectors-mp4/hevc-4k-hdr":           "def9480578f8be0b ",
	"selectors-mp4/mpeg4-avi":             "058d9b1f52eac94f ",
	"slider1/av1-mp4":                     "b5672133bfd4d4a6 modern_format",
	"slider1/h264-1080-mkv":               "dfc63d13b0b85e0a ",
	"slider1/hevc-4k-hdr":                 "bfa2466e7342e6c1 already_in_format",
	"slider1/mpeg4-avi":                   "754d5503c42bd7ad ",
	"slider2/av1-mp4":                     "98336c257669afe9 modern_format",
	"slider2/h264-1080-mkv":               "163554dda7bd2b34 ",
	"slider2/hevc-4k-hdr":                 "2bc29f44c83a8ea5 already_in_format",
	"slider2/mpeg4-avi":                   "c95fd81c8beb30b3 ",
	"slider3/av1-mp4":                     "58a0daa6f0216096 modern_format",
	"slider3/h264-1080-mkv":               "61b5686069575f83 ",
	"slider3/hevc-4k-hdr":                 "84345f8d730b7453 already_in_format",
	"slider3/mpeg4-avi":                   "fb1250d584315b86 ",
	"slider4/av1-mp4":                     "17075e3074b1110c modern_format",
	"slider4/h264-1080-mkv":               "d3f87258f49dd7ca ",
	"slider4/hevc-4k-hdr":                 "093708c13fd9b366 already_in_format",
	"slider4/mpeg4-avi":                   "13fef3b132b01f3b ",
	"slider5/av1-mp4":                     "20af9d79d2af7280 modern_format",
	"slider5/h264-1080-mkv":               "5cf8c4e788fd7aba ",
	"slider5/hevc-4k-hdr":                 "230828e91cd23620 already_in_format",
	"slider5/mpeg4-avi":                   "666f969f9502c3c3 ",
}
