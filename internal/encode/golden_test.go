// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package encode

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// The golden table is the review surface for the arguments (L-HW-20): every change to a command
// shows up as a diff in testdata/args.tsv. Run `go test ./internal/encode -update` to rewrite it,
// then read the diff before committing.

var update = flag.Bool("update", false, "rewrite testdata/args.tsv")

type goldenCase struct {
	name    string
	summary string
	plan    Plan
	info    media.Info
	rung    Rung
}

// goldenCases is codec × bit depth kind (8, 10-bit SDR, HDR10, HLG) × scale (none, 1080 from
// 2160, 720) × container (mkv, mp4) for the processor family: the matrix the brief asks for.
func goldenCases() []goldenCase {
	codecs := []struct {
		name  string
		codec Codec
	}{
		{"hevc", CodecHEVC}, {"h264", CodecH264}, {"av1", CodecAV1},
	}
	depths := []struct {
		name string
		info media.Info
	}{
		{"8", goldenInfo(8, "bt709")},
		{"10sdr", goldenInfo(10, "bt709")},
		{"hdr10", goldenInfo(10, "smpte2084")},
		{"hlg", goldenInfo(10, "arib-std-b67")},
	}
	scales := []struct {
		name  string
		scale Scale
	}{
		{"none", Scale{}},
		{"1080", Scale{Height: 1080}},
		{"720", Scale{Height: 720}},
	}
	containers := []struct {
		name      string
		container Container
	}{
		{"mkv", ContainerMKV}, {"mp4", ContainerMP4},
	}

	var cases []goldenCase
	for _, codec := range codecs {
		for _, depth := range depths {
			for _, scale := range scales {
				for _, container := range containers {
					plan := Plan{
						Video: VideoPlan{
							Codec: codec.codec, CRF: intp(22), Preset: "normal", Scale: scale.scale,
						},
						Container: container.container,
						Output:    "out." + container.name,
					}
					cases = append(cases, goldenCase{
						name:    strings.Join([]string{codec.name, depth.name, scale.name, container.name}, "-"),
						summary: strings.Join([]string{codec.name, depth.name, scale.name, container.name}, " "),
						plan:    plan,
						info:    depth.info,
						rung:    cpuRung(codec.codec),
					})
				}
			}
		}
	}
	return cases
}

// goldenInfo is a 3840×2160 source so every scale row has something to scale, with one audio and
// one subtitle stream so the container rules are visible in the table.
func goldenInfo(bitDepth int, transfer string) media.Info {
	primaries, space := "bt709", "bt709"
	if transfer != "bt709" {
		primaries, space = "bt2020", "bt2020nc"
	}
	return media.Info{
		Path: "in.mkv",
		Video: []media.VideoTrack{{
			Index: 0, Codec: "h264", Width: 3840, Height: 2160, BitDepth: bitDepth,
			ColorPrimaries: primaries, ColorTransfer: transfer, ColorSpace: space,
		}},
		Audio:     []media.AudioTrack{{Index: 1, Codec: "eac3"}},
		Subtitles: []media.SubtitleTrack{{Index: 2, Codec: "subrip"}},
	}
}

func TestGoldenArguments(t *testing.T) {
	lines := []string{"name\tplan\targv"}
	for _, c := range goldenCases() {
		res, err := Build(c.plan, c.info, c.rung)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		lines = append(lines, c.name+"\t"+c.summary+"\t"+strings.Join(res.Argv, " "))
	}
	got := strings.Join(lines, "\n") + "\n"

	path := filepath.Join("testdata", "args.tsv")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the golden table (%v); run go test ./internal/encode -update", err)
	}
	if got != string(want) {
		t.Errorf("the arguments differ from %s; run go test ./internal/encode -update and review the diff\n%s",
			path, firstDifferingLine(string(want), got))
	}
}

// firstDifferingLine names the first row that moved, so a failure points at the change rather
// than printing hundreds of rows.
func firstDifferingLine(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return "line " + strconv.Itoa(i+1) + ":\n  table: " + a + "\n  built: " + b
		}
	}
	return "the tables differ but line by line they match"
}
