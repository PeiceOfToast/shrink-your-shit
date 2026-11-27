// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package encode

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// subtitlesInfo is the base file with exactly these subtitle streams.
func subtitlesInfo(tracks ...media.SubtitleTrack) media.Info {
	info := baseInfo()
	info.Subtitles = tracks
	return info
}

// TestSubtitlesMKVConvertsMovText is L-AUD-28: MKV holds every subtitle as it is, so only the one
// it cannot hold (mov_text) is converted, and nothing is left out.
func TestSubtitlesMKVConvertsMovText(t *testing.T) {
	info := subtitlesInfo(
		media.SubtitleTrack{Index: 2, Codec: "mov_text"},
		media.SubtitleTrack{Index: 3, Codec: "hdmv_pgs_subtitle"},
		media.SubtitleTrack{Index: 4, Codec: "ass"},
	)
	plan := basePlan(CodecHEVC)
	res := buildOK(t, plan, info, cpuRung(CodecHEVC))
	if got := strings.Join(mapped(res.Argv), " "); got != "0:0 0:1 0:2 0:3 0:4" {
		t.Errorf("maps = %q, want every subtitle", got)
	}
	if v, _ := flagValue(res.Argv, "-c:s:0"); v != "srt" {
		t.Errorf("first subtitle codec = %q, want srt", v)
	}
	for _, flag := range []string{"-c:s:1", "-c:s:2"} {
		if hasFlag(res.Argv, flag) {
			t.Errorf("argv = %v, want no conversion for %s", res.Argv, flag)
		}
	}
	wantNotes(t, res)
}

// TestSubtitlesMP4ConvertsTextAndLeavesPicturesOut is L-AUD-28 for MP4: the text forms it cannot
// hold as they are become mov_text, mov_text and dvd_subtitle stay as they are, and a picture
// subtitle is left out once, with the count in the log.
func TestSubtitlesMP4ConvertsTextAndLeavesPicturesOut(t *testing.T) {
	info := subtitlesInfo(
		media.SubtitleTrack{Index: 2, Codec: "subrip"},
		media.SubtitleTrack{Index: 3, Codec: "ass"},
		media.SubtitleTrack{Index: 4, Codec: "ssa"},
		media.SubtitleTrack{Index: 5, Codec: "webvtt"},
		media.SubtitleTrack{Index: 6, Codec: "text"},
		media.SubtitleTrack{Index: 7, Codec: "mov_text"},
		media.SubtitleTrack{Index: 8, Codec: "dvd_subtitle"},
		media.SubtitleTrack{Index: 9, Codec: "hdmv_pgs_subtitle"},
		media.SubtitleTrack{Index: 10, Codec: "ttml"},
	)
	plan := basePlan(CodecHEVC)
	plan.Container = ContainerMP4
	plan.Output = "out.mp4"

	res := buildOK(t, plan, info, cpuRung(CodecHEVC))
	if got := strings.Join(mapped(res.Argv), " "); got != "0:0 0:1 0:2 0:3 0:4 0:5 0:6 0:7 0:8" {
		t.Errorf("maps = %q, want every subtitle MP4 can hold", got)
	}
	for i := 0; i < 5; i++ {
		if v, _ := flagValue(res.Argv, "-c:s:"+string(rune('0'+i))); v != "mov_text" {
			t.Errorf("subtitle %d codec = %q, want mov_text", i, v)
		}
	}
	for _, flag := range []string{"-c:s:5", "-c:s:6"} {
		if hasFlag(res.Argv, flag) {
			t.Errorf("argv = %v, want no conversion for %s", res.Argv, flag)
		}
	}
	wantNotes(t, res,
		"Styled subtitles (ASS) became plain text: MP4 can't keep their styling.",
		"2 subtitles were left out: MP4 can't hold them.",
	)
}

// TestSubtitlesMP4LeftOutNoteIsSingular: one subtitle left out says "was ... it".
func TestSubtitlesMP4LeftOutNoteIsSingular(t *testing.T) {
	info := subtitlesInfo(media.SubtitleTrack{Index: 2, Codec: "hdmv_pgs_subtitle"})
	plan := basePlan(CodecHEVC)
	plan.Container = ContainerMP4
	plan.Output = "out.mp4"
	res := buildOK(t, plan, info, cpuRung(CodecHEVC))
	wantNotes(t, res, "1 subtitle was left out: MP4 can't hold it.")
}

// TestSubtitlesWebMConvertsTextAndLeavesPicturesOut is L-AUD-28 for WebM: webvtt stays, every
// other text form becomes webvtt, a kind it does not know is copied, and picture subtitles are
// left out with the count.
func TestSubtitlesWebMConvertsTextAndLeavesPicturesOut(t *testing.T) {
	info := subtitlesInfo(
		media.SubtitleTrack{Index: 2, Codec: "webvtt"},
		media.SubtitleTrack{Index: 3, Codec: "subrip"},
		media.SubtitleTrack{Index: 4, Codec: "ass"},
		media.SubtitleTrack{Index: 5, Codec: "hdmv_pgs_subtitle"},
		media.SubtitleTrack{Index: 6, Codec: "dvd_subtitle"},
		media.SubtitleTrack{Index: 7, Codec: "ttml"},
	)
	info.Audio = []media.AudioTrack{{Index: 1, Codec: "opus", Channels: 2}}
	plan := basePlan(CodecAV1)
	plan.Container = ContainerWebM
	plan.Output = "out.webm"

	res := buildOK(t, plan, info, cpuRung(CodecAV1))
	if got := strings.Join(mapped(res.Argv), " "); got != "0:0 0:1 0:2 0:3 0:4 0:7" {
		t.Errorf("maps = %q, want webvtt, the converted text and the unknown kind", got)
	}
	if hasFlag(res.Argv, "-c:s:0") {
		t.Errorf("argv = %v, want a webvtt subtitle copied", res.Argv)
	}
	if v, _ := flagValue(res.Argv, "-c:s:1"); v != "webvtt" {
		t.Errorf("converted subtitle codec = %q, want webvtt", v)
	}
	wantNotes(t, res,
		"Styled subtitles (ASS) became plain text: WebM can't keep their styling.",
		"2 picture subtitles were left out: WebM can't hold them.",
	)
}

// TestSubtitlesDroppedByThePlanSayNothing: a subtitle the person chose to drop needs no note.
func TestSubtitlesDroppedByThePlanSayNothing(t *testing.T) {
	info := subtitlesInfo(
		media.SubtitleTrack{Index: 2, Codec: "hdmv_pgs_subtitle", Language: "ger"},
		media.SubtitleTrack{Index: 3, Codec: "subrip", Language: "eng"},
	)
	plan := basePlan(CodecHEVC)
	plan.Subtitles = []StreamPlan{{Index: 2, Action: StreamDrop}}
	res := buildOK(t, plan, info, cpuRung(CodecHEVC))
	if got := strings.Join(mapped(res.Argv), " "); got != "0:0 0:1 0:3" {
		t.Errorf("maps = %q, want the dropped subtitle gone", got)
	}
	wantNotes(t, res)
}

// TestSubtitlesExtractedAreTheirOwnOutputs is L-AUD-22's extraction: each extracted subtitle is an
// output of its own in the staging folder, written before the new file's own arguments so ffmpeg
// keeps them apart, and it leaves the new file. Only the streams left inside are numbered for the
// file type's conversions.
func TestSubtitlesExtractedAreTheirOwnOutputs(t *testing.T) {
	info := subtitlesInfo(
		media.SubtitleTrack{Index: 2, Codec: "subrip", Language: "eng"},
		media.SubtitleTrack{Index: 3, Codec: "ass", Language: "ger", Forced: true},
		media.SubtitleTrack{Index: 4, Codec: "subrip", Language: "eng"},
	)
	plan := basePlan(CodecHEVC)
	plan.Container = ContainerMP4
	plan.Output = "/library/film-shrunk.mp4"
	plan.SidecarDir = "/library/.film-shrunk.syp-tmp/s0"
	plan.Subtitles = []StreamPlan{
		{Index: 2, Action: StreamExtract, Part: "eng"},
		{Index: 3, Action: StreamExtract, Part: "ger.forced"},
	}

	res := buildOK(t, plan, info, cpuRung(CodecHEVC))
	argv := res.Argv
	if last := argv[len(argv)-1]; last != plan.Output {
		t.Fatalf("argv ends %q, want the new file %q", last, plan.Output)
	}
	// The extraction outputs come first, each with its own map and codec.
	if got := strings.Join(mapped(argv), " "); got != "0:2 0:3 0:0 0:1 0:4" {
		t.Errorf("maps = %q, want the extracts, then the video, audio and remaining subtitle", got)
	}
	if v, _ := flagValue(argv, "-c:s"); v != "srt" {
		t.Errorf("extract codec = %q, want srt", v)
	}
	i := indexOf(argv, StagedSidecar(plan.SidecarDir, 0))
	if i < 0 || i+2 >= len(argv) || argv[i+1] != "-map" || argv[i+2] != "0:3" {
		t.Fatalf("argv = %v, want the second extract right after the first", argv)
	}
	if got, want := StagedSidecar(plan.SidecarDir, 1), filepath.Join("/library", ".film-shrunk.syp-tmp", "s0", "sub.1.srt"); got != want {
		t.Errorf("second extract = %q, want %q", got, want)
	}
	// The only subtitle left in the file is index 4: it converts, and it is the first output one.
	if v, _ := flagValue(argv, "-c:s:0"); v != "mov_text" {
		t.Errorf("remaining subtitle codec = %q, want mov_text", v)
	}

	extracts, err := Extracts(plan, info)
	if err != nil {
		t.Fatalf("extracts: %v", err)
	}
	if len(extracts) != 2 || extracts[0].Part != "eng" || extracts[1].Part != "ger.forced" {
		t.Errorf("extracts = %+v, want the two parts in file order", extracts)
	}
	audio, subs, err := KeptStreams(plan, info)
	if err != nil {
		t.Fatalf("kept streams: %v", err)
	}
	if len(audio) != 1 || len(subs) != 1 || subs[0] != 4 {
		t.Errorf("kept audio %v subtitles %v, want the one subtitle left inside", audio, subs)
	}
}

// TestSubtitlesExtractedNeedTheirFolder: ffmpeg cannot write the .srt files without somewhere to
// put them, and a plan that says neither is refused rather than silently dropping the subtitles.
func TestSubtitlesExtractedNeedTheirFolder(t *testing.T) {
	info := subtitlesInfo(media.SubtitleTrack{Index: 2, Codec: "subrip"})
	plan := basePlan(CodecHEVC)
	plan.Subtitles = []StreamPlan{{Index: 2, Action: StreamExtract}}
	if _, err := Build(plan, info, cpuRung(CodecHEVC)); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("err = %v, want an invalid plan", err)
	}
}

// TestSubtitlesOnlyTextIsExtracted: a picture subtitle has no text ffmpeg could write to .srt, so
// the plan is refused rather than producing an empty file.
func TestSubtitlesOnlyTextIsExtracted(t *testing.T) {
	info := subtitlesInfo(media.SubtitleTrack{Index: 2, Codec: "hdmv_pgs_subtitle"})
	plan := basePlan(CodecHEVC)
	plan.SidecarDir = "tmp"
	plan.Subtitles = []StreamPlan{{Index: 2, Action: StreamExtract, Part: "eng"}}
	if _, err := Build(plan, info, cpuRung(CodecHEVC)); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("err = %v, want an invalid plan", err)
	}
}

// TestSubtitlesPlanErrors: a name part on anything but an extraction, a duplicate, and a stream
// the file does not have are plan errors, because the plan was built against another file.
func TestSubtitlesPlanErrors(t *testing.T) {
	info := subtitlesInfo(media.SubtitleTrack{Index: 2, Codec: "subrip"})
	cases := map[string][]StreamPlan{
		"part on a drop":   {{Index: 2, Action: StreamDrop, Part: "eng"}},
		"named twice":      {{Index: 2, Action: StreamDrop}, {Index: 2, Action: StreamExtract, Part: "eng"}},
		"stream not there": {{Index: 9, Action: StreamDrop}},
	}
	for name, list := range cases {
		plan := basePlan(CodecHEVC)
		plan.SidecarDir = "tmp"
		plan.Subtitles = list
		if _, err := Build(plan, info, cpuRung(CodecHEVC)); !errors.Is(err, ErrInvalidPlan) {
			t.Errorf("%s: err = %v, want an invalid plan", name, err)
		}
	}
}

// TestSrtNames is L-AUD-22's naming: lang[.forced|.sdh][.N] goes after the base name, an untagged
// subtitle has no language part, and the staged file is numbered by its place in the plan.
func TestSrtNames(t *testing.T) {
	cases := map[string]string{
		"eng":        "film.eng.srt",
		"ger.forced": "film.ger.forced.srt",
		"eng.sdh":    "film.eng.sdh.srt",
		"eng.2":      "film.eng.2.srt",
		"":           "film.srt",
		"2":          "film.2.srt",
	}
	for part, want := range cases {
		if got := SrtName("/library/film.mkv", part); got != "/library/"+want {
			t.Errorf("part %q: name = %q, want %q", part, got, want)
		}
	}
	if got, want := StagedSidecar("/tmp/stage", 3), filepath.Join("/tmp/stage", "sub.3.srt"); got != want {
		t.Errorf("staged = %q, want %q", got, want)
	}
}

func indexOf(argv []string, s string) int {
	for i, a := range argv {
		if a == s {
			return i
		}
	}
	return -1
}
