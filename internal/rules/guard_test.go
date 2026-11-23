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
	sel("selectors-keep-medium", func(s *Selectors) { s.FileType, s.MaxBitrate, s.RateControl = "keep", "medium", "quality_legacy" })
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
// Re-taken for F3 (11-23): an HEVC plan now carries the measured QSV offset (cardCRF); every skip code is
// unchanged, only those plans' fingerprints moved.
var guardGolden = map[string]string{
	"legacy-beside/av1-mp4":               "faa600fca4f271be already_hevc",
	"legacy-beside/h264-1080-mkv":         "faa600fca4f271be ",
	"legacy-beside/hevc-4k-hdr":           "ccbe3f66ecada98f already_hevc",
	"legacy-beside/mpeg4-avi":             "faa600fca4f271be format_not_converted",
	"legacy-replace/av1-mp4":              "087812f06ca7cd44 already_hevc",
	"legacy-replace/h264-1080-mkv":        "087812f06ca7cd44 ",
	"legacy-replace/hevc-4k-hdr":          "31bb028624993cdc already_hevc",
	"legacy-replace/mpeg4-avi":            "087812f06ca7cd44 format_not_converted",
	"selectors-720-stereo/av1-mp4":        "670fbff63e1e8dbb modern_format",
	"selectors-720-stereo/h264-1080-mkv":  "751e2265c61ef719 ",
	"selectors-720-stereo/hevc-4k-hdr":    "ecd3ee34ea2f6c1b ",
	"selectors-720-stereo/mpeg4-avi":      "d9bd862fd14fa5c8 ",
	"selectors-beside/av1-mp4":            "4c484a3672345ac5 modern_format",
	"selectors-beside/h264-1080-mkv":      "a18f91a1f50c438f ",
	"selectors-beside/hevc-4k-hdr":        "e92844323578c0c4 already_in_format",
	"selectors-beside/mpeg4-avi":          "915c3caa9032523d ",
	"selectors-default/av1-mp4":           "d8342e61692f92bd modern_format",
	"selectors-default/h264-1080-mkv":     "c5399bf9ec46c3c3 ",
	"selectors-default/hevc-4k-hdr":       "728eef9bdd34058b already_in_format",
	"selectors-default/mpeg4-avi":         "d9bd862fd14fa5c8 ",
	"selectors-dv/av1-mp4":                "d8342e61692f92bd modern_format",
	"selectors-dv/h264-1080-mkv":          "c5399bf9ec46c3c3 ",
	"selectors-dv/hevc-4k-hdr":            "728eef9bdd34058b already_in_format",
	"selectors-dv/mpeg4-avi":              "d9bd862fd14fa5c8 ",
	"selectors-folder-next/av1-mp4":       "81a010b225f79e9b modern_format",
	"selectors-folder-next/h264-1080-mkv": "bdbc6ebdc3505ebf ",
	"selectors-folder-next/hevc-4k-hdr":   "20bfffd8754c051f already_in_format",
	"selectors-folder-next/mpeg4-avi":     "04bdb0c5410c6ffc ",
	"selectors-keep-medium/av1-mp4":       "31bf0d0f37330c4c modern_format",
	"selectors-keep-medium/h264-1080-mkv": "6c67fba4c5c3bad4 ",
	"selectors-keep-medium/hevc-4k-hdr":   "df9eefecb1b93915 ",
	"selectors-keep-medium/mpeg4-avi":     "e7d53b9e3f5bd498 ",
	"selectors-mp4/av1-mp4":               "d9d2cb081d9a48ea modern_format",
	"selectors-mp4/h264-1080-mkv":         "883dc48c0fe2e198 ",
	"selectors-mp4/hevc-4k-hdr":           "def9480578f8be0b ",
	"selectors-mp4/mpeg4-avi":             "f35d4924f22e3b3e ",
	"slider1/av1-mp4":                     "b6056256b10cc00f modern_format",
	"slider1/h264-1080-mkv":               "f157eb049964e3fe ",
	"slider1/hevc-4k-hdr":                 "bfa2466e7342e6c1 already_in_format",
	"slider1/mpeg4-avi":                   "a73914b80df0d05f ",
	"slider2/av1-mp4":                     "a11220e7075d752e modern_format",
	"slider2/h264-1080-mkv":               "962e9ef4fec59fcf ",
	"slider2/hevc-4k-hdr":                 "2bc29f44c83a8ea5 already_in_format",
	"slider2/mpeg4-avi":                   "b260f5e112823a37 ",
	"slider3/av1-mp4":                     "f9fcb9e1705b28d6 modern_format",
	"slider3/h264-1080-mkv":               "45b092fca26abdc3 ",
	"slider3/hevc-4k-hdr":                 "84345f8d730b7453 already_in_format",
	"slider3/mpeg4-avi":                   "1b942bbda2b215d3 ",
	"slider4/av1-mp4":                     "ff050c00882d1bc2 modern_format",
	"slider4/h264-1080-mkv":               "4bd45d9691980147 ",
	"slider4/hevc-4k-hdr":                 "093708c13fd9b366 already_in_format",
	"slider4/mpeg4-avi":                   "95be853efe88268d ",
	"slider5/av1-mp4":                     "946969027a28223d modern_format",
	"slider5/h264-1080-mkv":               "d522674a445e8054 ",
	"slider5/hevc-4k-hdr":                 "230828e91cd23620 already_in_format",
	"slider5/mpeg4-avi":                   "3a8f20aeeafceb70 ",
}
