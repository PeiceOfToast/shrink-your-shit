// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"testing"
)

// The encoder golden matrix: every codec x depth x scale x encoder (the processor and each graphics encoder of the
// codec's format), through FFmpegArgsWith, and the full graphics pipeline through
// FFmpegArgsFullGPU (ok is false for the families without one). The expected arguments are testdata/encoder_args.tsv, generated once from the code before the
// encoder profiles moved (slice 5a commit 1). Every later change must leave the file unchanged.

type goldenCase struct {
	name string
	plan Plan
	enc  string
	full bool
}

func goldenCases() []goldenCase {
	hdr := &HDR{Transfer: "smpte2084", Primaries: "bt2020", Matrix: "bt2020nc"}
	codecs := []string{"libx265", "libx264", "libsvtav1", "libvpx-vp9"}
	encs := []string{"", "hevc_nvenc", "hevc_qsv", "hevc_vaapi", "hevc_amf", "h264_nvenc", "h264_qsv", "h264_vaapi", "h264_amf", "av1_nvenc", "av1_qsv", "av1_vaapi", "av1_amf"}
	format := map[string]string{"libx265": "", "libx264": "h264", "libsvtav1": "av1", "libvpx-vp9": "vp9"}
	var cases []goldenCase
	for _, codec := range codecs {
		for _, depth := range []string{"sdr", "10bit", "hdr"} {
			if (depth == "hdr" && codec != "libx265") || (depth == "10bit" && codec == "libvpx-vp9") {
				continue
			}
			for _, scale := range []int{0, 1080} {
				for _, enc := range encs {
					v := VideoEncode{Codec: codec, CRF: 22, Preset: "medium", Height: scale, Format: format[codec]}
					switch depth {
					case "10bit":
						v.TenBit = true
					case "hdr":
						v.HDR = hdr
					}
					p := Plan{Video: v}
					name := fmt.Sprintf("%s|%s|%d|crf", codec, depth, scale)
					cases = append(cases, goldenCase{name: name + "|with|" + enc, plan: p, enc: enc})
					if enc != "" {
						cases = append(cases, goldenCase{name: name + "|full|" + enc, plan: p, enc: enc, full: true})
					}
				}
			}
		}
	}
	return cases
}

// TestEncoderArgsGoldens compares every case with its line in testdata/encoder_args.tsv: whether the encoder was
// used (ok), and the exact arguments.
func TestEncoderArgsGoldens(t *testing.T) {
	old := vaapiDevice
	vaapiDevice = func() string { return "/dev/dri/renderD129" }
	defer func() { vaapiDevice = old }()

	f, err := os.Open("testdata/encoder_args.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	want := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), "\t")
		if !ok {
			t.Fatalf("bad line %q", sc.Text())
		}
		want[name] = rest
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	cases := goldenCases()
	if len(cases) != len(want) {
		t.Errorf("%d cases, %d goldens", len(cases), len(want))
	}
	for _, c := range cases {
		golden, ok := want[c.name]
		if !ok {
			t.Errorf("%s: no golden", c.name)
			continue
		}
		var args []string
		var used bool
		if c.full {
			args, used = c.plan.FFmpegArgsFullGPU("in.mkv", "out.mkv", c.enc)
		} else {
			args, used = c.plan.FFmpegArgsWith("in.mkv", "out.mkv", c.enc)
		}
		got := fmt.Sprintf("%v\t%s", used, strings.Join(args, " "))
		if got != golden {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, golden)
		}
	}
}
