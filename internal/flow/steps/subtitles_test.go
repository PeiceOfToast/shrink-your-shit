// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// subtitleInfo is a probed file with the audio the language choices need and exactly these
// subtitle streams.
func subtitleInfo(tracks ...media.SubtitleTrack) media.Info {
	info := probeInfo("h264", 1920, 1080)
	info.Subtitles = tracks
	return info
}

// subtitleTrack is one probed subtitle with the fields the names read.
func subtitleTrack(index int, codec, language, title string, forced, sdh bool) media.SubtitleTrack {
	return media.SubtitleTrack{
		Index: index, Codec: codec, Language: language, Title: title,
		Forced: forced, HearingImpaired: sdh,
	}
}

// planSubs runs the step's Plan on this file and returns the decision and the plan.
func planSubs(t *testing.T, set AudioSettings, info media.Info) (flow.Decision, encode.Plan) {
	t.Helper()
	var p encode.Plan
	d, err := AudioStep{}.Plan(set, flow.PlanInput{Info: info}, &p)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return d, p
}

// dropIndexes and extractParts read the subtitle part of the plan.
func dropIndexes(p encode.Plan) []int {
	var out []int
	for _, s := range p.Subtitles {
		if s.Action == encode.StreamDrop {
			out = append(out, s.Index)
		}
	}
	return out
}

func extractParts(p encode.Plan) []string {
	var out []string
	for _, s := range p.Subtitles {
		if s.Action == encode.StreamExtract {
			out = append(out, s.Part)
		}
	}
	return out
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameStrings(a, b []string) bool { return strings.Join(a, "\x00") == strings.Join(b, "\x00") }

// TestSubtitlesKeepAllLeavesThemAlone is the default: nothing is dropped or extracted.
func TestSubtitlesKeepAllLeavesThemAlone(t *testing.T) {
	info := subtitleInfo(subtitleTrack(3, "subrip", "eng", "", false, false), subtitleTrack(4, "hdmv_pgs_subtitle", "ger", "", false, false))
	set := audioDefaults()
	d, p := planSubs(t, set, info)
	if len(p.Subtitles) != 0 || d.Work || d.Reason != "audio.as_is" {
		t.Errorf("decision = %+v plan = %+v, want every subtitle left where it is", d, p.Subtitles)
	}
}

// TestSubtitlesMyLanguages is L-AUD-20: subtitles in another language go, an untagged subtitle
// stays, and with no language list nothing is dropped.
func TestSubtitlesMyLanguages(t *testing.T) {
	info := subtitleInfo(
		subtitleTrack(3, "subrip", "eng", "", false, false),
		subtitleTrack(4, "subrip", "ger", "", false, false),
		subtitleTrack(5, "subrip", "", "", false, false),
		subtitleTrack(6, "hdmv_pgs_subtitle", "ger", "", false, false),
	)
	set := audioDefaults()
	set.Subtitles = "my_languages"
	set.SubtitleLanguages = []string{"eng"}
	d, p := planSubs(t, set, info)
	if got := dropIndexes(p); !sameInts(got, []int{4, 6}) {
		t.Errorf("drops = %v, want the German subtitles only", got)
	}
	if d.Detail["subtitlesDropped"] != 2 || !d.Work || d.Reason != "audio.subtitles" {
		t.Errorf("decision = %+v, want two subtitle drops", d)
	}

	// The subtitle list wins when both lists are set (L-AUD-20's precedence).
	set.SubtitleLanguages = []string{"eng"}
	set.Languages = []string{"ger"}
	_, p = planSubs(t, set, info)
	if got := dropIndexes(p); !sameInts(got, []int{4, 6}) {
		t.Errorf("drops = %v, want the subtitle list to decide, not the audio's", got)
	}

	// The audio's languages are used when the subtitle list is empty (L-AUD-20's precedence).
	set.SubtitleLanguages = nil
	set.Languages = []string{"ger"}
	_, p = planSubs(t, set, info)
	if got := dropIndexes(p); !sameInts(got, []int{3}) {
		t.Errorf("drops = %v, want the English subtitle dropped by the audio's languages", got)
	}

	// With no list at all, nothing is dropped.
	set.Languages = nil
	d, p = planSubs(t, set, info)
	if len(p.Subtitles) != 0 || d.Work {
		t.Errorf("decision = %+v plan = %+v, want nothing dropped with no languages", d, p.Subtitles)
	}
}

// TestSubtitlesTextOnly is L-AUD-21: only picture subtitles go, a kind the program does not know
// stays, and the log says how many went.
func TestSubtitlesTextOnly(t *testing.T) {
	info := subtitleInfo(
		subtitleTrack(3, "subrip", "eng", "", false, false),
		subtitleTrack(4, "hdmv_pgs_subtitle", "eng", "", false, false),
		subtitleTrack(5, "ttml", "eng", "", false, false),
		subtitleTrack(6, "dvd_subtitle", "ger", "", false, false),
	)
	set := audioDefaults()
	set.Subtitles = "text_only"
	set.AllLanguages = true // no language list here: every language, as the step's default
	d, p := planSubs(t, set, info)
	if got := dropIndexes(p); !sameInts(got, []int{4, 6}) {
		t.Errorf("drops = %v, want the two picture subtitles only", got)
	}
	if len(d.Notes) != 1 || d.Notes[0] != "2 picture subtitles were left out: you chose text only." {
		t.Errorf("notes = %q, want the count of picture subtitles", d.Notes)
	}
	if d.Detail["subtitlesKept"] != 2 {
		t.Errorf("kept = %v, want the text and the unknown kind", d.Detail["subtitlesKept"])
	}
}

// TestSubtitlesSrt is L-AUD-22 with its trap: only the text subtitles in the person's languages
// (or untagged) are extracted; other languages and picture subtitles stay inside the new file.
func TestSubtitlesSrt(t *testing.T) {
	info := subtitleInfo(
		subtitleTrack(3, "subrip", "eng", "", false, false),
		subtitleTrack(4, "subrip", "ger", "", false, false),
		subtitleTrack(5, "hdmv_pgs_subtitle", "eng", "", false, false),
		subtitleTrack(6, "subrip", "", "", false, false),
	)
	set := audioDefaults()
	set.Subtitles = "srt"
	set.SubtitleLanguages = []string{"eng"}
	d, p := planSubs(t, set, info)
	if got := extractParts(p); !sameStrings(got, []string{"eng", ""}) {
		t.Errorf("extracts = %q, want the English and untagged subtitles", got)
	}
	if len(dropIndexes(p)) != 0 {
		t.Errorf("drops = %v, want every other subtitle still inside the file", dropIndexes(p))
	}
	if d.Detail["subtitlesExtracted"] != 2 || d.Detail["subtitlesKept"] != 2 {
		t.Errorf("detail = %v, want two extracted and two still inside", d.Detail)
	}
	if d.Label != "Saving 2 subtitles as SRT files" {
		t.Errorf("label = %q", d.Label)
	}
}

// TestSubtitlesSrtNames is L-AUD-22's naming: lang[.forced|.sdh][.N], canonicalised, forced over
// SDH, numbered when repeated, and no language part for an untagged subtitle.
func TestSubtitlesSrtNames(t *testing.T) {
	info := subtitleInfo(
		subtitleTrack(3, "subrip", "fra", "", false, false),
		subtitleTrack(4, "subrip", "eng", "", true, true),
		subtitleTrack(5, "subrip", "eng", "[SDH]", false, false),
		subtitleTrack(6, "subrip", "eng", "", false, false),
		subtitleTrack(7, "subrip", "eng", "", false, false),
		subtitleTrack(8, "subrip", "", "", false, false),
		subtitleTrack(9, "subrip", "", "", false, false),
	)
	set := audioDefaults()
	set.Subtitles = "srt"
	set.Languages = []string{"fre", "eng"}
	_, p := planSubs(t, set, info)
	want := []string{"fre", "eng.forced", "eng.sdh", "eng", "eng.2", "", "2"}
	if got := extractParts(p); !sameStrings(got, want) {
		t.Errorf("parts = %q, want %q", got, want)
	}
}

// TestSubtitlesSrtUsesTheMainAudioLanguage: with no languages chosen, "yours" is the default audio
// track's language, else the first track's (L-AUD-22).
func TestSubtitlesSrtUsesTheMainAudioLanguage(t *testing.T) {
	info := subtitleInfo(
		subtitleTrack(3, "subrip", "eng", "", false, false),
		subtitleTrack(4, "subrip", "ger", "", false, false),
	)
	eng := info.Audio[0]
	ger := info.Audio[1]
	withDefault := ger
	withDefault.Default = true
	info.Audio = []media.AudioTrack{eng, withDefault}
	set := audioDefaults()
	set.Subtitles = "srt"
	_, p := planSubs(t, set, info)
	if got := extractParts(p); strings.Join(got, " ") != "ger" {
		t.Errorf("extracts = %q, want the default track's language", got)
	}

	// No default flag: the first audio track wins.
	info.Audio = []media.AudioTrack{eng, ger}
	_, p = planSubs(t, set, info)
	if got := extractParts(p); strings.Join(got, " ") != "eng" {
		t.Errorf("extracts = %q, want the first track's language", got)
	}
}

// TestIsSDH is L-AUD-23: the hearing-impaired disposition or a title word marks SDH. Forced beats
// it in the name, which TestSubtitlesSrtNames shows.
func TestIsSDH(t *testing.T) {
	cases := []struct {
		track media.SubtitleTrack
		want  bool
	}{
		{subtitleTrack(1, "subrip", "eng", "", false, true), true},
		{subtitleTrack(1, "subrip", "eng", "English SDH", false, false), true},
		{subtitleTrack(1, "subrip", "eng", "English (CC)", false, false), true},
		{subtitleTrack(1, "subrip", "eng", "Hearing impaired", false, false), true},
		{subtitleTrack(1, "subrip", "eng", "English", false, false), false},
		{subtitleTrack(1, "subrip", "eng", "SDH", false, false), true},
	}
	for _, c := range cases {
		if got := isSDH(c.track); got != c.want {
			t.Errorf("isSDH(%q, sdh=%v) = %v, want %v", c.track.Title, c.track.HearingImpaired, got, c.want)
		}
	}
}
