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
					// H.264 is not used for HDR, by choice (#855): those shapes are refused, not written, so they
					// are tested as refusals in TestH264HDRIsRefusedInEveryShape, not as rows here.
					if codec.codec == CodecH264 && (depth.name == "hdr10" || depth.name == "hlg") {
						continue
					}
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
	// A rotated phone clip: ffmpeg autorotates before -vf, so the 1080 box fits the upright
	// 2160×3840 picture and writes 608×1080 rather than squashing it into 1920×1080 (review
	// 44dbabe). This row fails if the coded size is used again.
	rotated := goldenInfo(8, "bt709")
	rotated.Video[0].Rotation = 90
	cases = append(cases, goldenCase{
		name:    "rotated90-1080-mkv",
		summary: "hevc 8 rotated90 1080 mkv",
		plan: Plan{
			Video:     VideoPlan{Codec: CodecHEVC, CRF: intp(22), Preset: "normal", Scale: Scale{Height: 1080}},
			Container: ContainerMKV,
			Output:    "out.mkv",
		},
		info: rotated,
		rung: cpuRung(CodecHEVC),
	})
	// Rate control (page 07's Advanced bitrate): a bitrate replaces the CRF for each processor
	// encoder. VBR rows exist only for the ladder's vbrFormats (HEVC and H.264).
	for _, c := range []struct {
		name  string
		codec Codec
		rc    RateControl
	}{
		{"hevc-cbr", CodecHEVC, RateControl{Mode: "cbr", AvgMbps: 3.6}},
		{"h264-cbr", CodecH264, RateControl{Mode: "cbr", AvgMbps: 6}},
		{"av1-cbr", CodecAV1, RateControl{Mode: "cbr", AvgMbps: 3}},
		{"hevc-vbr", CodecHEVC, RateControl{Mode: "vbr", AvgMbps: 3.6, MaxMbps: 5.4}},
		{"h264-vbr", CodecH264, RateControl{Mode: "vbr", AvgMbps: 6, MaxMbps: 9}},
	} {
		cases = append(cases, goldenCase{
			name:    c.name,
			summary: strings.ReplaceAll(c.name, "-", " ") + " rate control",
			plan: Plan{
				Video:       VideoPlan{Codec: c.codec, Preset: "normal"},
				RateControl: &c.rc,
				Container:   ContainerMKV,
				Output:      "out.mkv",
			},
			info: goldenInfo(8, "bt709"),
			rung: cpuRung(c.codec),
		})
	}
	// Strict CBR on an HDR10 file: the color description and the HRD filler share one -x265-params
	// flag. Two flags would let the second replace the first.
	cases = append(cases, goldenCase{
		name:    "hevc-hdr10-cbr",
		summary: "hevc hdr10 cbr rate control",
		plan: Plan{
			Video:       VideoPlan{Codec: CodecHEVC, Preset: "normal"},
			RateControl: &RateControl{Mode: "cbr", AvgMbps: 12},
			Container:   ContainerMKV,
			Output:      "out.mkv",
		},
		info: goldenInfo(10, "smpte2084"),
		rung: cpuRung(CodecHEVC),
	})

	// Card rungs (S3-2): every family × codec × depth × scale a record's probe can report, for the
	// hybrid path (the processor decodes, the card encodes). Only 8-bit and 10-bit SDR here: a
	// record whose 10-bit or HDR probe failed never offers those rungs, which is hw.Pick's rule.
	// The plan's slider position is the flow's normal (3), so L-HW-12's QSV HEVC offset shows.
	for _, f := range cardFamilies {
		for _, codec := range codecs {
			for _, depth := range []struct {
				name string
				info media.Info
			}{
				{"8", goldenInfo(8, "bt709")},
				{"10sdr", goldenInfo(10, "bt709")},
			} {
				for _, scale := range scales {
					cases = append(cases, goldenCase{
						name:    strings.Join([]string{f.name, codec.name, depth.name, scale.name, "mkv"}, "-"),
						summary: strings.Join([]string{f.name, codec.name, depth.name, scale.name, "mkv"}, " "),
						plan: Plan{
							Video: VideoPlan{
								Codec: codec.codec, CRF: intp(22), Preset: "normal", Quality: 3, Scale: scale.scale,
							},
							Container: ContainerMKV,
							Output:    "out.mkv",
						},
						info: depth.info,
						rung: cardRung(f.family, codec.codec, f.device),
					})
				}
			}
		}
	}
	// HDR on a card: the color description is written as for the processor, and the card's own
	// encoder carries the metadata — the probe is what proves it survives.
	for _, f := range cardFamilies {
		for _, hdr := range []struct {
			name     string
			transfer string
		}{
			{"hdr10", "smpte2084"}, {"hlg", "arib-std-b67"},
		} {
			cases = append(cases, goldenCase{
				name:    f.name + "-hevc-" + hdr.name + "-none-mkv",
				summary: f.name + " hevc " + hdr.name + " none mkv",
				plan: Plan{
					Video:     VideoPlan{Codec: CodecHEVC, CRF: intp(22), Preset: "normal", Quality: 3},
					Container: ContainerMKV, Output: "out.mkv",
				},
				info: goldenInfo(10, hdr.transfer),
				rung: cardRung(f.family, CodecHEVC, f.device),
			})
		}
	}
	// The full GPU pipeline (L-HW-11): the card decodes and encodes, and nothing is uploaded. Only
	// QSV and VA-API have it, and only without frame rate, tone mapping or a depth change.
	for _, f := range cardFamilies[0:2] {
		for _, depth := range []struct {
			name string
			info media.Info
		}{
			{"8", goldenInfo(8, "bt709")},
			{"10sdr", goldenInfo(10, "bt709")},
		} {
			for _, scale := range []struct {
				name  string
				scale Scale
			}{
				{"none", Scale{}}, {"720", Scale{Height: 720}},
			} {
				cases = append(cases, goldenCase{
					name:    f.name + "-full-hevc-" + depth.name + "-" + scale.name + "-mkv",
					summary: f.name + " full hevc " + depth.name + " " + scale.name + " mkv",
					plan: Plan{
						Video: VideoPlan{
							Codec: CodecHEVC, CRF: intp(22), Preset: "normal", Quality: 3, Scale: scale.scale,
						},
						Container: ContainerMKV, Output: "out.mkv",
					},
					info: depth.info,
					rung: fullRung(f.family, CodecHEVC, f.device),
				})
			}
		}
	}
	// Tone mapping runs on the processor, so these are hybrid rungs wherever the flow picks one.
	for _, f := range cardFamilies {
		cases = append(cases, goldenCase{
			name:    f.name + "-hevc-tonemap-mkv",
			summary: f.name + " hevc tonemap mkv",
			plan: Plan{
				Video:     VideoPlan{Codec: CodecHEVC, CRF: intp(22), Preset: "normal", Quality: 3, HDR: HDRTonemap},
				Container: ContainerMKV, Output: "out.mkv",
			},
			info: goldenInfo(10, "smpte2084"),
			rung: cardRung(f.family, CodecHEVC, f.device),
		})
	}
	// L-HW-12's measured offsets at each slider position, on QSV's HEVC encoder.
	for quality := 1; quality <= 5; quality++ {
		cases = append(cases, goldenCase{
			name:    "qsv-hevc-quality-" + strconv.Itoa(quality),
			summary: "qsv hevc quality " + strconv.Itoa(quality),
			plan: Plan{
				Video:     VideoPlan{Codec: CodecHEVC, CRF: intp(22), Preset: "normal", Quality: quality},
				Container: ContainerMKV, Output: "out.mkv",
			},
			info: goldenInfo(8, "bt709"),
			rung: cardRung(FamilyQSV, CodecHEVC, ""),
		})
	}
	// The speed choice per family: written as -preset on the two that have one, and a note on
	// VA-API, which does not (L-HW-13).
	for _, c := range []struct {
		name string
		rung Rung
	}{
		{"qsv-hevc-faster-mkv", cardRung(FamilyQSV, CodecHEVC, "")},
		{"nvenc-hevc-slower-mkv", cardRung(FamilyNVENC, CodecHEVC, "")},
		{"vaapi-hevc-faster-mkv", cardRung(FamilyVAAPI, CodecHEVC, renderNode)},
	} {
		preset := "faster"
		if strings.Contains(c.name, "slower") {
			preset = "slower"
		}
		cases = append(cases, goldenCase{
			name:    c.name,
			summary: strings.ReplaceAll(c.name, "-", " "),
			plan: Plan{
				Video:     VideoPlan{Codec: CodecHEVC, CRF: intp(22), Preset: preset, Quality: 3},
				Container: ContainerMKV, Output: "out.mkv",
			},
			info: goldenInfo(8, "bt709"),
			rung: c.rung,
		})
	}
	// The speed choice on a card encoder of every codec: the card's own names, never SVT-AV1's
	// numbers, which QSV refuses and NVENC reads upside down. AV1 gets both directions, because
	// that is where the two scales disagree most.
	for _, c := range []struct {
		name   string
		codec  Codec
		preset string
		rung   Rung
	}{
		{"qsv-h264-faster-mkv", CodecH264, "faster", cardRung(FamilyQSV, CodecH264, "")},
		{"qsv-av1-faster-mkv", CodecAV1, "faster", cardRung(FamilyQSV, CodecAV1, "")},
		{"qsv-av1-slower-mkv", CodecAV1, "slower", cardRung(FamilyQSV, CodecAV1, "")},
		{"nvenc-h264-slower-mkv", CodecH264, "slower", cardRung(FamilyNVENC, CodecH264, "")},
		{"nvenc-av1-faster-mkv", CodecAV1, "faster", cardRung(FamilyNVENC, CodecAV1, "")},
		{"nvenc-av1-slower-mkv", CodecAV1, "slower", cardRung(FamilyNVENC, CodecAV1, "")},
	} {
		cases = append(cases, goldenCase{
			name:    c.name,
			summary: strings.ReplaceAll(c.name, "-", " "),
			plan: Plan{
				Video:     VideoPlan{Codec: c.codec, CRF: intp(22), Preset: c.preset, Quality: 3},
				Container: ContainerMKV, Output: "out.mkv",
			},
			info: goldenInfo(8, "bt709"),
			rung: c.rung,
		})
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
