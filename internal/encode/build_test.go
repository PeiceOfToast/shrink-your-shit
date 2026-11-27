// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package encode

import (
	"errors"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

func intp(n int) *int { return &n }

func cpuRung(codec Codec) Rung {
	encoder, _ := CPUEncoder(codec)
	return Rung{Encoder: encoder, Family: FamilyCPU, Decode: DecodeSoftware}
}

// baseInfo is a 4K 8-bit SDR H.264 file with one E-AC-3 audio stream and one SRT subtitle.
func baseInfo() media.Info {
	return media.Info{
		Path:   "in.mkv",
		Format: media.Format{Names: []string{"matroska"}},
		Video: []media.VideoTrack{{
			Index: 0, Codec: "h264", Width: 3840, Height: 2160, BitDepth: 8,
			FrameRate:      media.Rational{Num: 24, Den: 1},
			ColorPrimaries: "bt709", ColorTransfer: "bt709", ColorSpace: "bt709",
		}},
		Audio:     []media.AudioTrack{{Index: 1, Codec: "eac3", Channels: 6}},
		Subtitles: []media.SubtitleTrack{{Index: 2, Codec: "subrip", Language: "eng"}},
	}
}

func basePlan(codec Codec) Plan {
	return Plan{
		Video:     VideoPlan{Codec: codec, CRF: intp(22), Preset: "normal"},
		Container: ContainerMKV,
		Output:    "out.mkv",
	}
}

func buildOK(t *testing.T, plan Plan, info media.Info, rung Rung) Result {
	t.Helper()
	res, err := Build(plan, info, rung)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return res
}

// flagValue is the value after a flag, and whether the flag is there at all.
func flagValue(argv []string, flag string) (string, bool) {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag {
			return argv[i+1], true
		}
	}
	return "", false
}

func hasFlag(argv []string, flag string) bool {
	for _, arg := range argv {
		if arg == flag {
			return true
		}
	}
	return false
}

func mapped(argv []string) []string {
	var out []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-map" {
			out = append(out, argv[i+1])
		}
	}
	return out
}

func wantNotes(t *testing.T, res Result, want ...string) {
	t.Helper()
	if len(res.Notes) != len(want) {
		t.Fatalf("notes = %q, want %q", res.Notes, want)
	}
	for i := range want {
		if res.Notes[i] != want[i] {
			t.Errorf("note %d = %q, want %q", i, res.Notes[i], want[i])
		}
	}
}

func TestBuildMapsKeptStreamsInInputOrder(t *testing.T) {
	info := baseInfo()
	info.Audio = append(info.Audio, media.AudioTrack{Index: 4, Codec: "aac"})
	plan := basePlan(CodecHEVC)
	plan.Audio = []AudioPlan{{Index: 1, Action: StreamDrop}}

	res := buildOK(t, plan, info, cpuRung(CodecHEVC))
	if got := mapped(res.Argv); strings.Join(got, " ") != "0:0 0:4 0:2" {
		t.Errorf("maps = %v, want the video, the kept audio and the subtitle", got)
	}
	if v, _ := flagValue(res.Argv, "-c:a"); v != "copy" {
		t.Errorf("audio codec = %q, want copy", v)
	}
	if v, _ := flagValue(res.Argv, "-c:s"); v != "copy" {
		t.Errorf("subtitle codec = %q, want copy", v)
	}
	wantNotes(t, res)
}

func TestBuildFaststartOnlyOnMP4(t *testing.T) {
	for _, c := range []struct {
		container Container
		output    string
		codec     Codec
		faststart bool
	}{
		{ContainerMKV, "out.mkv", CodecHEVC, false},
		{ContainerMP4, "out.mp4", CodecHEVC, true},
		{ContainerWebM, "out.webm", CodecAV1, false},
	} {
		plan := basePlan(c.codec)
		plan.Container = c.container
		plan.Output = c.output
		res := buildOK(t, plan, baseInfo(), cpuRung(c.codec))
		if got := hasFlag(res.Argv, "-movflags"); got != c.faststart {
			t.Errorf("%s: faststart = %v, want %v", c.container, got, c.faststart)
		}
		if c.faststart {
			if v, _ := flagValue(res.Argv, "-movflags"); v != "+faststart" {
				t.Errorf("%s: movflags = %q, want +faststart", c.container, v)
			}
		}
	}
}

// TestBuildConvertsATrackMP4CannotHold is L-AUD-10: audio a container cannot hold is converted,
// never dropped, and the note says which codec it became. Subtitles are still dropped.
func TestBuildConvertsATrackMP4CannotHold(t *testing.T) {
	info := baseInfo()
	info.Audio = []media.AudioTrack{{Index: 1, Codec: "opus"}, {Index: 4, Codec: "aac"}}
	info.Subtitles = []media.SubtitleTrack{
		{Index: 2, Codec: "hdmv_pgs_subtitle"},
		{Index: 3, Codec: "mov_text"},
	}
	plan := basePlan(CodecHEVC)
	plan.Container = ContainerMP4
	plan.Output = "out.mp4"

	res := buildOK(t, plan, info, cpuRung(CodecHEVC))
	if got := mapped(res.Argv); strings.Join(got, " ") != "0:0 0:1 0:4 0:3" {
		t.Errorf("maps = %v, want both audio tracks and the subtitle MP4 holds", got)
	}
	if v, _ := flagValue(res.Argv, "-c:a:0"); v != "eac3" {
		t.Errorf("first audio codec = %q, want eac3", v)
	}
	if v, _ := flagValue(res.Argv, "-b:a:0"); v != "192k" {
		t.Errorf("first audio bitrate = %q, want the automatic eac3 rate", v)
	}
	wantNotes(t, res,
		"audio stream #1 (opus) is converted to eac3: MP4 cannot hold Opus",
		"dropped subtitle stream #2 (hdmv_pgs_subtitle): MP4 cannot hold it",
	)
}

// TestBuildConvertsToOpusForWebM is L-AUD-8: WebM gets libopus at 128 kbps, and audio is not
// dropped. The titles are still dropped, since WebM holds only WebVTT.
func TestBuildConvertsToOpusForWebM(t *testing.T) {
	info := baseInfo()
	info.Audio = []media.AudioTrack{{Index: 1, Codec: "aac"}, {Index: 4, Codec: "opus"}}
	info.Subtitles = []media.SubtitleTrack{
		{Index: 2, Codec: "subrip"},
		{Index: 3, Codec: "webvtt"},
	}
	plan := basePlan(CodecAV1)
	plan.Container = ContainerWebM
	plan.Output = "out.webm"

	res := buildOK(t, plan, info, cpuRung(CodecAV1))
	if got := mapped(res.Argv); strings.Join(got, " ") != "0:0 0:1 0:4 0:3" {
		t.Errorf("maps = %v, want both audio tracks and the WebVTT subtitle", got)
	}
	if v, _ := flagValue(res.Argv, "-c:a:0"); v != "libopus" {
		t.Errorf("first audio codec = %q, want libopus", v)
	}
	if v, _ := flagValue(res.Argv, "-b:a:0"); v != "128k" {
		t.Errorf("first audio bitrate = %q, want 128k", v)
	}
	wantNotes(t, res,
		"audio stream #1 (aac) is converted to opus: WebM needs Opus or Vorbis audio",
		"dropped subtitle stream #2 (subrip): WebM cannot hold it",
	)
}

func TestBuildTenBitSDRStaysTenBit(t *testing.T) {
	info := baseInfo()
	info.Video[0].BitDepth = 10
	info.Video[0].PixelFormat = "yuv420p10le"

	res := buildOK(t, basePlan(CodecHEVC), info, cpuRung(CodecHEVC))
	if v, _ := flagValue(res.Argv, "-pix_fmt:v:0"); v != "yuv420p10le" {
		t.Errorf("pixel format = %q, want yuv420p10le from the source", v)
	}
	if v, _ := flagValue(res.Argv, "-profile:v:0"); v != "main10" {
		t.Errorf("HEVC profile = %q, want main10", v)
	}

	// An explicit 8 asks to drop it.
	plan := basePlan(CodecHEVC)
	plan.Video.BitDepth = Depth8
	res = buildOK(t, plan, info, cpuRung(CodecHEVC))
	if v, _ := flagValue(res.Argv, "-pix_fmt:v:0"); v != "yuv420p" {
		t.Errorf("pixel format = %q, want yuv420p for an explicit 8", v)
	}
	if hasFlag(res.Argv, "-profile:v:0") {
		t.Error("an 8-bit HEVC encode carries main10")
	}

	// HDR that is kept forces ten-bit even when the plan says 8 (L-HW-16).
	info.Video[0].ColorTransfer = "smpte2084"
	plan.Video.BitDepth = Depth8
	res = buildOK(t, plan, info, cpuRung(CodecHEVC))
	if v, _ := flagValue(res.Argv, "-pix_fmt:v:0"); v != "yuv420p10le" {
		t.Errorf("HDR pixel format = %q, want ten-bit whatever the plan says", v)
	}
}

func TestBuildHDR10KeepsItsMetadata(t *testing.T) {
	info := baseInfo()
	info.Video[0].BitDepth = 10
	info.Video[0].ColorPrimaries = "bt2020"
	info.Video[0].ColorTransfer = "smpte2084"
	info.Video[0].ColorSpace = "bt2020nc"

	res := buildOK(t, basePlan(CodecHEVC), info, cpuRung(CodecHEVC))
	for flag, want := range map[string]string{
		"-color_primaries:v:0": "bt2020",
		"-color_trc:v:0":       "smpte2084",
		"-colorspace:v:0":      "bt2020nc",
	} {
		if v, _ := flagValue(res.Argv, flag); v != want {
			t.Errorf("%s = %q, want %q", flag, v, want)
		}
	}
	params, _ := flagValue(res.Argv, "-x265-params:v:0")
	for _, part := range []string{"colorprim=bt2020", "transfer=smpte2084", "hdr10=1", "hdr10-opt=1"} {
		if !strings.Contains(params, part) {
			t.Errorf("x265 params %q lack %q", params, part)
		}
	}

	// HLG keeps its transfer; HDR10's static-metadata parameters would be wrong for it.
	info.Video[0].ColorTransfer = "arib-std-b67"
	res = buildOK(t, basePlan(CodecHEVC), info, cpuRung(CodecHEVC))
	if v, _ := flagValue(res.Argv, "-color_trc:v:0"); v != "arib-std-b67" {
		t.Errorf("HLG transfer = %q, want arib-std-b67", v)
	}
	if hasFlag(res.Argv, "-x265-params:v:0") {
		t.Error("an HLG encode carries HDR10 x265 parameters")
	}

	// SDR gets no colour description at all.
	info = baseInfo()
	res = buildOK(t, basePlan(CodecHEVC), info, cpuRung(CodecHEVC))
	if hasFlag(res.Argv, "-color_trc:v:0") {
		t.Error("an SDR encode describes a transfer")
	}
}

func TestBuildDolbyVision(t *testing.T) {
	info := baseInfo()
	info.Video[0].BitDepth = 10
	info.Video[0].ColorTransfer = "smpte2084"
	info.Video[0].HDR = media.HDR{DolbyVision: true, DVProfile: 5}

	_, err := Build(basePlan(CodecHEVC), info, cpuRung(CodecHEVC))
	if !errors.Is(err, ErrSkip) {
		t.Fatalf("profile 5: error = %v, want ErrSkip", err)
	}

	// 7 and 8 convert as their HDR10 base and say so.
	info.Video[0].HDR.DVProfile = 8
	res := buildOK(t, basePlan(CodecHEVC), info, cpuRung(CodecHEVC))
	wantNotes(t, res, "Dolby Vision profile 8 is written as its HDR10 base")
	if v, _ := flagValue(res.Argv, "-pix_fmt:v:0"); v != "yuv420p10le" {
		t.Errorf("pixel format = %q, want ten-bit", v)
	}

	// A record that does not say which profile it is cannot be judged safe to convert.
	info.Video[0].HDR.DVProfile = 0
	if _, err := Build(basePlan(CodecHEVC), info, cpuRung(CodecHEVC)); !errors.Is(err, ErrSkip) {
		t.Errorf("profile 0: error = %v, want ErrSkip", err)
	}

	// A copied video carries the Dolby Vision layer untouched.
	plan := basePlan(CodecCopy)
	plan.Video.CRF = nil
	plan.Video.Preset = ""
	info.Video[0].HDR.DVProfile = 5
	res = buildOK(t, plan, info, Rung{})
	if v, _ := flagValue(res.Argv, "-c:v:0"); v != "copy" {
		t.Errorf("video codec = %q, want copy", v)
	}
}

func TestBuildRefusesAGPURung(t *testing.T) {
	for _, family := range []Family{FamilyQSV, FamilyNVENC, FamilyVAAPI, FamilyAMF} {
		rung := Rung{Encoder: "hevc_qsv", Family: family}
		if _, err := Build(basePlan(CodecHEVC), baseInfo(), rung); !errors.Is(err, ErrRungNotBuilt) {
			t.Errorf("%s: error = %v, want ErrRungNotBuilt", family, err)
		}
	}
	rung := cpuRung(CodecHEVC)
	rung.Decode = DecodeHardware
	if _, err := Build(basePlan(CodecHEVC), baseInfo(), rung); !errors.Is(err, ErrRungNotBuilt) {
		t.Errorf("hardware decode: error = %v, want ErrRungNotBuilt", err)
	}
}

func TestBuildRefusesLaterStageFields(t *testing.T) {
	plan := basePlan(CodecHEVC)
	plan.Video.CRF = nil
	plan.RateControl = &RateControl{Mode: "vbr", AvgMbps: 5, MaxMbps: 7, TwoPass: true}
	if _, err := Build(plan, baseInfo(), cpuRung(CodecHEVC)); !errors.Is(err, ErrNotBuiltYet) {
		t.Errorf("two passes: error = %v, want ErrNotBuiltYet", err)
	}

}

func TestBuildSkipsHDRTheFlowLeavesAlone(t *testing.T) {
	info := baseInfo()
	info.Video[0].BitDepth = 10
	info.Video[0].ColorTransfer = "smpte2084"
	plan := basePlan(CodecHEVC)
	plan.Video.HDR = HDRSkip
	if _, err := Build(plan, info, cpuRung(CodecHEVC)); !errors.Is(err, ErrSkip) {
		t.Errorf("error = %v, want ErrSkip for an HDR source", err)
	}
	// An SDR source with skip set is simply an ordinary encode.
	if _, err := Build(plan, baseInfo(), cpuRung(CodecHEVC)); err != nil {
		t.Errorf("SDR with skip: %v", err)
	}
}

func TestBuildScaleNeverUpsizes(t *testing.T) {
	cases := []struct {
		name       string
		sourceW    int
		sourceH    int
		rotation   int
		box        Scale
		wantFilter string
	}{
		{"1080 from 2160", 3840, 2160, 0, Scale{Height: 1080}, "scale=1920:1080"},
		{"720 from 2160", 3840, 2160, 0, Scale{Height: 720}, "scale=1280:720"},
		{"custom box keeps the shape", 3840, 2160, 0, Scale{Width: 1920, Height: 800}, "scale=1422:800"},
		{"box 640 on a 720p source", 1280, 720, 0, Scale{Width: 640, Height: 640}, "scale=640:360"},
		{"same height is no scaling", 1920, 1080, 0, Scale{Height: 1080}, ""},
		{"never larger than the source", 320, 240, 0, Scale{Height: 1080}, ""},
		// ffmpeg autorotates before -vf, so a quarter turn is fitted as the upright (swapped) size.
		{"quarter turn 90", 320, 240, 90, Scale{Height: 180}, "scale=134:180"},
		{"quarter turn -90", 320, 240, -90, Scale{Height: 180}, "scale=134:180"},
		{"quarter turn 270", 320, 240, 270, Scale{Height: 180}, "scale=134:180"},
		{"half turn does not swap", 320, 240, 180, Scale{Height: 180}, "scale=240:180"},
		{"phone clip in a 1080 box upright", 3840, 2160, 90, Scale{Height: 1080}, "scale=608:1080"},
	}
	for _, c := range cases {
		info := baseInfo()
		info.Video[0].Width, info.Video[0].Height = c.sourceW, c.sourceH
		info.Video[0].Rotation = c.rotation
		plan := basePlan(CodecHEVC)
		plan.Video.Scale = c.box
		res := buildOK(t, plan, info, cpuRung(CodecHEVC))
		got, _ := flagValue(res.Argv, "-vf:v:0")
		if got != c.wantFilter {
			t.Errorf("%s: filter = %q, want %q", c.name, got, c.wantFilter)
		}
	}
}

func TestBuildPresetsAndTags(t *testing.T) {
	cases := []struct {
		codec  Codec
		choice string
		preset string
		tag    string
	}{
		{CodecHEVC, "", "medium", "hvc1"},
		{CodecHEVC, "faster", "fast", "hvc1"},
		{CodecHEVC, "slower", "slow", "hvc1"},
		{CodecH264, "slower", "slow", "avc1"},
		{CodecAV1, "normal", "6", "av01"},
		{CodecAV1, "slower", "4", "av01"},
	}
	for _, c := range cases {
		plan := basePlan(c.codec)
		plan.Video.Preset = c.choice
		res := buildOK(t, plan, baseInfo(), cpuRung(c.codec))
		if v, _ := flagValue(res.Argv, "-preset:v:0"); v != c.preset {
			t.Errorf("%s/%s: preset = %q, want %q", c.codec, c.choice, v, c.preset)
		}
		if v, _ := flagValue(res.Argv, "-tag:v:0"); v != c.tag {
			t.Errorf("%s: tag = %q, want %q", c.codec, v, c.tag)
		}
		if v, present := flagValue(res.Argv, "-svtav1-params:v:0"); c.codec == CodecAV1 {
			if !present || v != "tune=0" {
				t.Errorf("%s: svt params = %q present=%v, want tune=0", c.codec, v, present)
			}
		} else if present {
			t.Errorf("%s: carries SVT parameters", c.codec)
		}
	}

	plan := basePlan(CodecHEVC)
	plan.Video.Preset = "turbo"
	if _, err := Build(plan, baseInfo(), cpuRung(CodecHEVC)); !errors.Is(err, ErrInvalidPlan) {
		t.Errorf("unknown preset: error = %v, want ErrInvalidPlan", err)
	}
}

func TestBuildCopyLeavesTheVideoAlone(t *testing.T) {
	plan := basePlan(CodecCopy)
	plan.Video.CRF = nil
	plan.Video.Preset = ""
	res := buildOK(t, plan, baseInfo(), Rung{})
	if v, _ := flagValue(res.Argv, "-c:v:0"); v != "copy" {
		t.Errorf("video codec = %q, want copy", v)
	}
	for _, flag := range []string{"-crf:v:0", "-preset:v:0", "-tag:v:0", "-pix_fmt:v:0"} {
		if hasFlag(res.Argv, flag) {
			t.Errorf("a copied video carries %s", flag)
		}
	}

	plan.Video.Scale = Scale{Height: 720}
	if _, err := Build(plan, baseInfo(), Rung{}); !errors.Is(err, ErrInvalidPlan) {
		t.Errorf("copy with a scale: error = %v, want ErrInvalidPlan", err)
	}
}

func TestBuildClearsStatisticsTags(t *testing.T) {
	plan := basePlan(CodecHEVC)
	plan.MetadataClear = []string{"FOO", "BPS"}

	res := buildOK(t, plan, baseInfo(), cpuRung(CodecHEVC))
	count := map[string]int{}
	for i := 0; i+1 < len(res.Argv); i++ {
		if res.Argv[i] == "-metadata:s:v:0" {
			count[res.Argv[i+1]]++
		}
	}
	for _, want := range []string{"FOO=", "BPS=", "BPS-eng=", "DURATION=", "DURATION-eng=",
		"NUMBER_OF_FRAMES=", "NUMBER_OF_FRAMES-eng=", "_STATISTICS_WRITING_APP-eng="} {
		if count[want] != 1 {
			t.Errorf("metadata %q cleared %d times, want once", want, count[want])
		}
	}

	plan = basePlan(CodecCopy)
	plan.Video.CRF = nil
	plan.Video.Preset = ""
	res = buildOK(t, plan, baseInfo(), Rung{})
	for i := 0; i < len(res.Argv); i++ {
		if res.Argv[i] == "-metadata:s:v:0" {
			t.Error("a copied video has its tags cleared")
		}
	}
}

func TestBuildNotesDroppedVideoTracks(t *testing.T) {
	info := baseInfo()
	info.Video = append(info.Video,
		media.VideoTrack{Index: 5, Codec: "png", AttachedPicture: true},
		media.VideoTrack{Index: 6, Codec: "hevc"},
	)
	res := buildOK(t, basePlan(CodecHEVC), info, cpuRung(CodecHEVC))
	wantNotes(t, res,
		"dropped video stream #5 (png, cover art)",
		"dropped video stream #6 (hevc): only one video stream is converted",
	)
}

func TestBuildRefusesInvalidPlans(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Plan, *media.Info, *Rung)
		want   error
	}{
		{"no output", func(p *Plan, _ *media.Info, _ *Rung) { p.Output = "" }, ErrInvalidPlan},
		{"no input", func(_ *Plan, i *media.Info, _ *Rung) { i.Path = "" }, ErrInvalidPlan},
		{"unknown container", func(p *Plan, _ *media.Info, _ *Rung) { p.Container = "avi" }, ErrInvalidPlan},
		{"unknown codec", func(p *Plan, _ *media.Info, _ *Rung) { p.Video.Codec = "vp9" }, ErrInvalidPlan},
		{"no video", func(_ *Plan, i *media.Info, _ *Rung) {
			i.Video = []media.VideoTrack{{Index: 0, Codec: "png", AttachedPicture: true}}
		}, ErrInvalidPlan},
		{"no CRF", func(p *Plan, _ *media.Info, _ *Rung) { p.Video.CRF = nil }, ErrInvalidPlan},
		{"CRF too high", func(p *Plan, _ *media.Info, _ *Rung) { p.Video.CRF = intp(64) }, ErrInvalidPlan},
		{"negative frame rate", func(p *Plan, _ *media.Info, _ *Rung) { p.Video.FPS = -1 }, ErrInvalidPlan},
		{"bit depth 12", func(p *Plan, _ *media.Info, _ *Rung) { p.Video.BitDepth = 12 }, ErrInvalidPlan},
		{"negative scale", func(p *Plan, _ *media.Info, _ *Rung) { p.Video.Scale = Scale{Width: -10} }, ErrInvalidPlan},
		{"unknown stream", func(p *Plan, _ *media.Info, _ *Rung) {
			p.Audio = []AudioPlan{{Index: 9, Action: StreamDrop}}
		}, ErrInvalidPlan},
		{"stream named twice", func(p *Plan, _ *media.Info, _ *Rung) {
			p.Audio = []AudioPlan{{Index: 1, Action: StreamDrop}, {Index: 1, Action: StreamCopy}}
		}, ErrInvalidPlan},
		{"unknown action", func(p *Plan, _ *media.Info, _ *Rung) {
			p.Audio = []AudioPlan{{Index: 1, Action: "mix"}}
		}, ErrInvalidPlan},
		{"webm with HEVC", func(p *Plan, _ *media.Info, _ *Rung) {
			p.Container = ContainerWebM
			p.Output = "out.webm"
		}, ErrInvalidPlan},
		{"webm copying h264", func(p *Plan, _ *media.Info, _ *Rung) {
			p.Video.Codec = CodecCopy
			p.Video.CRF = nil
			p.Video.Preset = ""
			p.Container = ContainerWebM
			p.Output = "out.webm"
		}, ErrInvalidPlan},
		{"encoder for another codec", func(_ *Plan, _ *media.Info, r *Rung) { r.Encoder = "libx264" }, ErrInvalidPlan},
	}
	for _, c := range cases {
		plan, info, rung := basePlan(CodecHEVC), baseInfo(), cpuRung(CodecHEVC)
		c.mutate(&plan, &info, &rung)
		if _, err := Build(plan, info, rung); !errors.Is(err, c.want) {
			t.Errorf("%s: error = %v, want %v", c.name, err, c.want)
		}
	}
}

// TestBuildRateControl is page 07's bitrate part of the table: a bitrate in place of the CRF, with
// CBR pinning the at-most to the average and VBR letting it rise to the plan's number.
func TestBuildRateControl(t *testing.T) {
	cases := []struct {
		name  string
		codec Codec
		rc    RateControl
		want  string
	}{
		{"cbr", CodecH264, RateControl{Mode: "cbr", AvgMbps: 6},
			"-b:v:0 6000k -minrate:v:0 6000k -maxrate:v:0 6000k -bufsize:v:0 12000k"},
		{"vbr", CodecH264, RateControl{Mode: "vbr", AvgMbps: 6, MaxMbps: 9},
			"-b:v:0 6000k -maxrate:v:0 9000k -bufsize:v:0 18000k"},
	}
	for _, c := range cases {
		plan := basePlan(c.codec)
		plan.Video.CRF = nil
		plan.RateControl = &c.rc
		res := buildOK(t, plan, baseInfo(), cpuRung(c.codec))
		line := strings.Join(res.Argv, " ")
		if !strings.Contains(line, c.want) {
			t.Errorf("%s: argv = %s\nwant %s", c.name, line, c.want)
		}
		if strings.Contains(line, "-crf") {
			t.Errorf("%s: argv = %s, want no CRF", c.name, line)
		}
	}

	// HEVC's CBR is strict: without the HRD filler a simple picture encodes at a fraction of the
	// rate. H.264 and AV1 take no x265 options.
	hevc := basePlan(CodecHEVC)
	hevc.Video.CRF = nil
	hevc.RateControl = &RateControl{Mode: "cbr", AvgMbps: 6}
	params, ok := flagValue(buildOK(t, hevc, baseInfo(), cpuRung(CodecHEVC)).Argv, "-x265-params:v:0")
	if !ok || params != "hrd=1:strict-cbr=1" {
		t.Errorf("hevc CBR params = %q, %v; want hrd=1:strict-cbr=1", params, ok)
	}
	for _, codec := range []Codec{CodecH264, CodecAV1} {
		plan := basePlan(codec)
		plan.Video.CRF = nil
		plan.RateControl = &RateControl{Mode: "cbr", AvgMbps: 6}
		if hasFlag(buildOK(t, plan, baseInfo(), cpuRung(codec)).Argv, "-x265-params:v:0") {
			t.Errorf("%s CBR: argv has -x265-params, want none", codec)
		}
	}

	// An HDR10 HEVC CBR file carries the color description and strict CBR in one flag: a second
	// -x265-params would replace the first, silently dropping one of them.
	hdr := baseInfo()
	hdr.Video[0].BitDepth = 10
	hdr.Video[0].ColorPrimaries, hdr.Video[0].ColorTransfer, hdr.Video[0].ColorSpace = "bt2020", "smpte2084", "bt2020nc"
	hdrPlan := basePlan(CodecHEVC)
	hdrPlan.Video.CRF = nil
	hdrPlan.RateControl = &RateControl{Mode: "cbr", AvgMbps: 6}
	hdrArgv := buildOK(t, hdrPlan, hdr, cpuRung(CodecHEVC)).Argv
	params, ok = flagValue(hdrArgv, "-x265-params:v:0")
	if !ok || !strings.Contains(params, "hdr10=1") || !strings.Contains(params, "hrd=1:strict-cbr=1") {
		t.Errorf("HDR CBR params = %q, want the color description and strict CBR together", params)
	}
	flags := 0
	for _, arg := range hdrArgv {
		if arg == "-x265-params:v:0" {
			flags++
		}
	}
	if flags != 1 {
		t.Errorf("x265 params flags = %d, want 1 (a second would replace the first)", flags)
	}
}

// TestBuildRefusesBadRateControl is the plan's own guard: a mode the table does not have, VBR for a
// format the ladder does not offer it for, and a plan that carries both a CRF and a bitrate.
func TestBuildRefusesBadRateControl(t *testing.T) {
	cases := []struct {
		name  string
		codec Codec
		rc    RateControl
	}{
		{"VBR for AV1", CodecAV1, RateControl{Mode: "vbr", AvgMbps: 3, MaxMbps: 4.5}},
		{"VBR without an at-most", CodecHEVC, RateControl{Mode: "vbr", AvgMbps: 3.6}},
		{"VBR at-most below the average", CodecHEVC, RateControl{Mode: "vbr", AvgMbps: 5, MaxMbps: 4}},
		{"unknown mode", CodecHEVC, RateControl{Mode: "abr", AvgMbps: 5}},
		{"no average", CodecHEVC, RateControl{Mode: "cbr"}},
	}
	for _, c := range cases {
		plan := basePlan(c.codec)
		plan.Video.CRF = nil
		plan.RateControl = &c.rc
		if _, err := Build(plan, baseInfo(), cpuRung(c.codec)); !errors.Is(err, ErrInvalidPlan) {
			t.Errorf("%s: error = %v, want ErrInvalidPlan", c.name, err)
		}
	}

	// Both a CRF and a bitrate would leave one of them silent.
	plan := basePlan(CodecHEVC)
	plan.RateControl = &RateControl{Mode: "cbr", AvgMbps: 5}
	if _, err := Build(plan, baseInfo(), cpuRung(CodecHEVC)); !errors.Is(err, ErrInvalidPlan) {
		t.Errorf("CRF and a bitrate: error = %v, want ErrInvalidPlan", err)
	}

	// A copied picture cannot change its rate.
	plan = basePlan(CodecHEVC)
	plan.Video = VideoPlan{Codec: CodecCopy}
	plan.RateControl = &RateControl{Mode: "cbr", AvgMbps: 5}
	if _, err := Build(plan, baseInfo(), cpuRung(CodecHEVC)); !errors.Is(err, ErrInvalidPlan) {
		t.Errorf("copy with a bitrate: error = %v, want ErrInvalidPlan", err)
	}
}

func TestFrameRateIsWrittenAsAsked(t *testing.T) {
	cases := map[float64]string{30: "30", 23.976: "23.976", 29.97: "29.97"}
	for f, want := range cases {
		if got := frameRate(f); got != want {
			t.Errorf("frameRate(%v) = %q, want %q", f, got, want)
		}
	}
}
