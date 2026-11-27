// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// audioDefaults is the settings a flow gets when its Audio JSON names nothing: the card's own
// defaults, as DecodeSettings fills them.
func audioDefaults() AudioSettings {
	return AudioSettings{
		Format: "copy", Channels: "source", BitratePer: "channel",
		Commentary: "keep", Subtitles: "keep_all", Downmix: ptr("plain"),
	}
}

func ptr[T any](v T) *T { return &v }

// audioTrack is one probe track with the fields the audio choices read.
func audioTrack(index int, codec string, channels int, language, title string) media.AudioTrack {
	return media.AudioTrack{
		Index: index, Codec: codec, Channels: channels, Language: language,
		Title: title, SampleRate: 48000, BitRate: 640_000,
	}
}

// planAudio runs the step's Plan on these tracks and returns the decision and the output plan.
func planAudio(t *testing.T, set AudioSettings, tracks ...media.AudioTrack) (flow.Decision, encode.Plan) {
	t.Helper()
	var p encode.Plan
	info := media.Info{Path: "/library/clip.mkv", Audio: tracks}
	d, err := AudioStep{}.Plan(set, flow.PlanInput{Info: info}, &p)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return d, p
}

// audioEntry finds one track's plan entry.
func audioEntry(t *testing.T, p encode.Plan, index int) encode.AudioPlan {
	t.Helper()
	for _, a := range p.Audio {
		if a.Index == index {
			return a
		}
	}
	t.Fatalf("no audio entry for stream %d in %+v", index, p.Audio)
	return encode.AudioPlan{}
}

func TestAudioAsIs(t *testing.T) {
	var p encode.Plan
	info := probeInfo("h264", 1920, 1080)
	d, err := AudioStep{}.Plan(audioDefaults(), flow.PlanInput{Info: info}, &p)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if d.Work || d.Reason != "audio.as_is" || d.Label != "Audio copied as is" {
		t.Errorf("decision = %+v, want audio.as_is", d)
	}
	if d.Detail["audioTracks"] != 2 || d.Detail["subtitleTracks"] != 1 {
		t.Errorf("detail = %v, want 2 audio and 1 subtitle", d.Detail)
	}
	for _, a := range p.Audio {
		if a.Action != encode.StreamCopy || a.Codec != "" || a.Default != nil {
			t.Errorf("entry = %+v, want every track copied as is", a)
		}
	}
	if len(p.Subtitles) != 0 {
		t.Errorf("subtitles = %+v, want every subtitle copied as is", p.Subtitles)
	}
}

// TestAudioConvertsStereoAAC is page 06's Stereo AAC choice: the matching track becomes stereo AAC
// and the other language is dropped.
func TestAudioConvertsStereoAAC(t *testing.T) {
	set := audioDefaults()
	set.Format, set.Channels, set.Languages = "aac", "stereo", []string{"eng"}
	d, p := planAudio(t, set,
		audioTrack(1, "eac3", 6, "eng", ""),
		audioTrack(2, "aac", 2, "ger", ""),
	)
	entry := audioEntry(t, p, 1)
	if entry.Action != encode.StreamConvert || entry.Codec != "aac" || entry.Channels != 2 {
		t.Errorf("eng entry = %+v, want a stereo AAC conversion", entry)
	}
	if audioEntry(t, p, 2).Action != encode.StreamDrop {
		t.Error("the German track must be dropped")
	}
	if !d.Work || d.Reason != "audio.convert" || d.Label != "Converting 1 of 2 audio tracks to AAC; dropping 1" {
		t.Errorf("decision = %+v, want the conversion sentence", d)
	}
}

// TestAudioSurroundKeepsEveryMatch is L-AUD-1's other half: a surround choice keeps every track in
// the person's languages, not only the main one.
func TestAudioSurroundKeepsEveryMatch(t *testing.T) {
	set := audioDefaults()
	set.Format, set.Channels, set.Languages = "aac", "source", []string{"eng"}
	_, p := planAudio(t, set,
		audioTrack(1, "eac3", 6, "eng", ""),
		audioTrack(2, "aac", 2, "eng", ""),
		audioTrack(3, "aac", 2, "ger", ""),
	)
	if audioEntry(t, p, 1).Action != encode.StreamConvert || audioEntry(t, p, 2).Action != encode.StreamCopy {
		t.Errorf("entries = %+v, want both English tracks kept", p.Audio)
	}
	if audioEntry(t, p, 3).Action != encode.StreamDrop {
		t.Error("the German track must be dropped")
	}
}

// TestAudioOriginalLanguage is K1: "orig" is the first track in file order whose language is set
// and not und.
func TestAudioOriginalLanguage(t *testing.T) {
	set := audioDefaults()
	set.Languages = []string{"orig"}
	_, p := planAudio(t, set,
		audioTrack(1, "aac", 2, "jpn", ""),
		audioTrack(2, "aac", 2, "eng", ""),
		audioTrack(3, "aac", 2, "und", ""),
	)
	if audioEntry(t, p, 1).Action != encode.StreamCopy {
		t.Error("orig must keep the Japanese track")
	}
	if audioEntry(t, p, 2).Action != encode.StreamDrop || audioEntry(t, p, 3).Action != encode.StreamDrop {
		t.Error("orig must drop the others")
	}
}

// TestAudioUndMatchesNoLanguage is L-AUD-17: "und" keeps the tracks with no language.
func TestAudioUndMatchesNoLanguage(t *testing.T) {
	set := audioDefaults()
	set.Languages = []string{"und"}
	_, p := planAudio(t, set,
		audioTrack(1, "aac", 2, "eng", ""),
		audioTrack(2, "aac", 2, "und", ""),
		audioTrack(3, "aac", 2, "", ""),
	)
	if audioEntry(t, p, 1).Action != encode.StreamDrop {
		t.Error("English must be dropped")
	}
	if audioEntry(t, p, 2).Action != encode.StreamCopy || audioEntry(t, p, 3).Action != encode.StreamCopy {
		t.Error("und and an empty language are the same, and both are kept")
	}
}

// TestAudioLanguageSpellingsMatch is L-AUD-16: a two-letter code finds the 639-2/B track.
func TestAudioLanguageSpellingsMatch(t *testing.T) {
	set := audioDefaults()
	set.Languages = []string{"de"}
	_, p := planAudio(t, set,
		audioTrack(1, "aac", 2, "eng", ""),
		audioTrack(2, "aac", 2, "ger", ""),
	)
	if audioEntry(t, p, 1).Action != encode.StreamDrop {
		t.Error("English must be dropped")
	}
	if audioEntry(t, p, 2).Action != encode.StreamCopy {
		t.Error("German must be kept through its two-letter code")
	}
}

// TestAudioUnknownLanguageMatchesNothing is L-AUD-16's edge: a code the app does not know is no
// language at all, so it matches nothing rather than the untagged tracks, and K2 keeps the main one.
func TestAudioUnknownLanguageMatchesNothing(t *testing.T) {
	set := audioDefaults()
	set.Channels, set.Languages = "stereo", []string{"nope"}
	d, p := planAudio(t, set,
		audioTrack(1, "aac", 2, "eng", ""),
		audioTrack(2, "aac", 2, "", ""),
	)
	if audioEntry(t, p, 1).Action != encode.StreamCopy || audioEntry(t, p, 2).Action != encode.StreamDrop {
		t.Error("an unknown code keeps the main track, not the untagged one")
	}
	if !contains(d.Notes, noMatchMainNote) {
		t.Errorf("notes = %v, want %q", d.Notes, noMatchMainNote)
	}
}

// TestAudioOriginalNotFound is K1's other half: "orig" with no language matches nothing and says
// so, and the stereo choice then keeps the main track (K2).
func TestAudioOriginalNotFound(t *testing.T) {
	set := audioDefaults()
	set.Channels, set.Languages = "stereo", []string{"orig"}
	d, p := planAudio(t, set,
		audioTrack(1, "aac", 2, "und", ""),
		audioTrack(2, "aac", 2, "und", ""),
	)
	if audioEntry(t, p, 1).Action != encode.StreamCopy || audioEntry(t, p, 2).Action != encode.StreamDrop {
		t.Error("with no original language, the main (first) track is kept")
	}
	for _, note := range []string{origNotFoundNote, noMatchMainNote} {
		if !contains(d.Notes, note) {
			t.Errorf("notes = %v, want %q", d.Notes, note)
		}
	}
}

// TestAudioNothingMatches is K2: no track in the wanted languages keeps the default-flagged track
// for a stereo choice, and every track for a surround one.
func TestAudioNothingMatches(t *testing.T) {
	tracks := []media.AudioTrack{
		audioTrack(1, "aac", 2, "eng", ""),
		audioTrack(2, "aac", 2, "ger", ""),
	}
	tracks[0].Default = true

	set := audioDefaults()
	set.Channels, set.Languages = "stereo", []string{"fre"}
	d, p := planAudio(t, set, tracks...)
	if audioEntry(t, p, 1).Action != encode.StreamCopy || audioEntry(t, p, 2).Action != encode.StreamDrop {
		t.Error("the stereo choice keeps the default-flagged track")
	}
	if d.Detail["dropped"] != 1 || !contains(d.Notes, noMatchMainNote) {
		t.Errorf("decision = %+v, want the main-track sentence", d)
	}

	set.Channels = "source"
	d, p = planAudio(t, set, tracks...)
	if len(p.Audio) != 2 || audioEntry(t, p, 1).Action == encode.StreamDrop || audioEntry(t, p, 2).Action == encode.StreamDrop {
		t.Error("the surround choice keeps every track")
	}
	if !contains(d.Notes, noMatchAllNote) {
		t.Errorf("notes = %v, want %q", d.Notes, noMatchAllNote)
	}
}

// TestAudioCommentaryDropped is K5: the comment disposition and a title that says commentary are
// dropped, "Comment" alone is not, and "Commentaries" is.
func TestAudioCommentaryDropped(t *testing.T) {
	set := audioDefaults()
	set.Commentary = "drop"
	disposition := audioTrack(5, "aac", 2, "spa", "")
	disposition.Comment = true
	d, p := planAudio(t, set,
		audioTrack(1, "aac", 2, "eng", ""),
		audioTrack(2, "aac", 2, "eng", "Commentary with the director"),
		audioTrack(3, "aac", 2, "ger", "Commentaries"),
		audioTrack(4, "aac", 2, "fre", "Comment"),
		disposition,
	)
	for _, index := range []int{2, 3, 5} {
		if audioEntry(t, p, index).Action != encode.StreamDrop {
			t.Errorf("stream %d is commentary and must be dropped", index)
		}
	}
	for _, index := range []int{1, 4} {
		if audioEntry(t, p, index).Action != encode.StreamCopy {
			t.Errorf("stream %d is not commentary and must be kept", index)
		}
	}
	if d.Reason != "audio.keep_languages" || d.Label != "Keeping 2 of 5 audio tracks" {
		t.Errorf("decision = %+v, want the kept-counts sentence", d)
	}
}

// TestAudioOnlyCommentaryKept is K2's never-silent rule: dropping every track would leave no sound,
// so they stay and the log says why.
func TestAudioOnlyCommentaryKept(t *testing.T) {
	set := audioDefaults()
	set.Commentary = "drop"
	d, p := planAudio(t, set, audioTrack(1, "aac", 2, "eng", "Commentary"))
	if d.Work {
		t.Errorf("decision = %+v, want no work", d)
	}
	if audioEntry(t, p, 1).Action != encode.StreamCopy {
		t.Error("the only track is kept")
	}
	if !contains(d.Notes, onlyCommentaryNote) {
		t.Errorf("notes = %v, want %q", d.Notes, onlyCommentaryNote)
	}
}

// TestAudioSmallTrackKept is L-AUD-4: a lossy track already at or below the target rate is copied,
// because converting it would not make it smaller.
func TestAudioSmallTrackKept(t *testing.T) {
	set := audioDefaults()
	set.Format, set.Channels = "aac", "stereo"
	small := audioTrack(1, "mp3", 2, "eng", "")
	small.BitRate = 96_000
	d, p := planAudio(t, set, small)
	if audioEntry(t, p, 1).Action != encode.StreamCopy {
		t.Error("a 96 kbps stereo MP3 is copied")
	}
	if !contains(d.Notes, smallKeptNote) {
		t.Errorf("notes = %v, want %q", d.Notes, smallKeptNote)
	}

	big := audioTrack(1, "mp3", 2, "eng", "")
	big.BitRate = 320_000
	_, p = planAudio(t, set, big)
	if audioEntry(t, p, 1).Action != encode.StreamConvert {
		t.Error("a 320 kbps stereo MP3 converts to the smaller AAC")
	}
}

// TestAudioLosslessConverts is L-AUD-5: a lossless track is converted even when its rate is low,
// because a lossless copy is not the smaller form.
func TestAudioLosslessConverts(t *testing.T) {
	set := audioDefaults()
	set.Format, set.Channels = "aac", "stereo"
	_, p := planAudio(t, set, audioTrack(1, "flac", 2, "eng", ""))
	if audioEntry(t, p, 1).Action != encode.StreamConvert {
		t.Error("FLAC converts to AAC")
	}
}

// TestAudioSevenOneToAACHasNoFoldNote: AAC at 5.1 is not the AC-3 family's cap, so the sentence
// naming E-AC-3 and AC-3 must not appear when the person asked for 5.1 AAC.
func TestAudioSevenOneToAACHasNoFoldNote(t *testing.T) {
	set := audioDefaults()
	set.Format, set.Channels = "aac", "5.1"
	d, p := planAudio(t, set, audioTrack(1, "truehd", 8, "eng", ""))
	entry := audioEntry(t, p, 1)
	if entry.Action != encode.StreamConvert || entry.Codec != "aac" || entry.Channels != 6 {
		t.Errorf("entry = %+v, want a 5.1 AAC conversion", entry)
	}
	if contains(d.Notes, foldedNote) {
		t.Errorf("notes = %v, want no E-AC-3/AC-3 sentence for AAC", d.Notes)
	}
}

// TestAudioFoldsSevenOne is L-AUD-6: an E-AC-3 7.1 track becomes 5.1 and the log says so.
func TestAudioFoldsSevenOne(t *testing.T) {
	set := audioDefaults()
	set.Format, set.Channels = "aac", "source"
	set.Codec = ptr("eac3")
	d, p := planAudio(t, set, audioTrack(1, "truehd", 8, "eng", ""))
	entry := audioEntry(t, p, 1)
	if entry.Action != encode.StreamConvert || entry.Codec != "eac3" {
		t.Errorf("entry = %+v, want an E-AC-3 conversion", entry)
	}
	if !contains(d.Notes, foldedNote) {
		t.Errorf("notes = %v, want %q", d.Notes, foldedNote)
	}
}

// TestAudioDefaultTrack is L-AUD-12: the chosen language is the output's default and the other
// tracks are cleared, whatever the file had.
func TestAudioDefaultTrack(t *testing.T) {
	set := audioDefaults()
	set.DefaultTrack = ptr("ger")
	d, p := planAudio(t, set,
		audioTrack(1, "aac", 2, "eng", ""),
		audioTrack(2, "aac", 2, "ger", ""),
	)
	if got := audioEntry(t, p, 1).Default; got == nil || *got {
		t.Error("the English track must be cleared")
	}
	if got := audioEntry(t, p, 2).Default; got == nil || !*got {
		t.Error("the German track must be the default")
	}
	if !d.Work || d.Reason != "audio.default_track" {
		t.Errorf("decision = %+v, want a default-track change", d)
	}

	set.DefaultTrack = nil
	_, p = planAudio(t, set,
		audioTrack(1, "aac", 2, "eng", ""),
		audioTrack(2, "aac", 2, "ger", ""),
	)
	if audioEntry(t, p, 1).Default != nil {
		t.Error("an empty Default track leaves each file's own flags")
	}

	// An unknown code names no track, so the file keeps its own flags and the log says why.
	set.DefaultTrack = ptr("nope")
	d, p = planAudio(t, set,
		audioTrack(1, "aac", 2, "eng", ""),
		audioTrack(2, "aac", 2, "", ""),
	)
	for _, a := range p.Audio {
		if a.Default != nil {
			t.Errorf("entry = %+v, want no default flag", a)
		}
	}
	if !contains(d.Notes, defaultNotFoundNote) {
		t.Errorf("notes = %v, want %q", d.Notes, defaultNotFoundNote)
	}
}

// TestAudioNoAudio is K2: a file with no audio tracks is not an error.
func TestAudioNoAudio(t *testing.T) {
	var p encode.Plan
	d, err := AudioStep{}.Plan(audioDefaults(), flow.PlanInput{Info: media.Info{Path: "/library/a.mkv"}}, &p)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if d.Work || d.Reason != "audio.no_audio" || len(p.Audio) != 0 {
		t.Errorf("decision = %+v, plan = %+v; want no_audio", d, p)
	}
}

func TestAudioValidate(t *testing.T) {
	check := AudioStep{}.Validate(audioDefaults(), flow.ValidateContext{})
	if len(check.Problems) != 0 {
		t.Errorf("copy problems = %+v, want none", check.Problems)
	}
	// The closed card's line, page 07's words: codec, channels, languages, subtitles.
	if want := "Same as the source · same channels as the source · my languages · subtitles: keep all"; check.Summary != want {
		t.Errorf("summary = %q, want %q", check.Summary, want)
	}
	set := audioDefaults()
	set.Format = "flac"
	check = AudioStep{}.Validate(set, flow.ValidateContext{})
	if len(check.Problems) != 1 || check.Problems[0].Code != "setting.choice" {
		t.Errorf("flac problems = %+v, want the choice problem", check.Problems)
	}
}

// TestAudioCopyConflicts: a number that needs conversion is refused while the sound is copied.
func TestAudioCopyConflicts(t *testing.T) {
	set := audioDefaults()
	set.Channels = "stereo"
	set.BitrateKbps = ptr(128)
	set.SampleRate = ptr(48000)
	set.Loudnorm = true
	set.Downmix = ptr("dpl2")
	check := AudioStep{}.Validate(set, flow.ValidateContext{})
	got := map[string]bool{}
	for _, p := range check.Problems {
		if p.Code != "audio.copy_conflict" {
			t.Errorf("problem = %+v, want audio.copy_conflict", p)
		}
		got[p.Field] = true
	}
	for _, field := range []string{"channels", "bitrateKbps", "sampleRate", "loudnorm", "downmix"} {
		if !got[field] {
			t.Errorf("problems = %v, want one on %s", got, field)
		}
	}
}

// TestAudioInertFields: the downmix choice is greyed when the channels are not going to stereo, and
// FLAC has no bitrate to set.
func TestAudioInertFields(t *testing.T) {
	set := audioDefaults()
	set.Format = "aac"
	check := AudioStep{}.Validate(set, flow.ValidateContext{})
	if !greyed(check.Disabled, "downmix") {
		t.Errorf("disabled = %+v, want the downmix greyed with source channels", check.Disabled)
	}
	set.Channels = "stereo"
	set.Codec = ptr("flac")
	set.BitrateKbps = ptr(128)
	check = AudioStep{}.Validate(set, flow.ValidateContext{})
	if greyed(check.Disabled, "downmix") {
		t.Error("the downmix applies when the channels go to stereo")
	}
	if !greyed(check.Disabled, "bitrateKbps") {
		t.Errorf("disabled = %+v, want the bitrate greyed for FLAC", check.Disabled)
	}
}

// TestAudioNotYet pins the one setting the engine still cannot act on: keeping the original track
// as well.
func TestAudioNotYet(t *testing.T) {
	set := audioDefaults()
	set.KeepOriginal = true
	set.Subtitles = "my_languages"
	set.SubtitleLanguages = []string{"eng"}
	check := AudioStep{}.Validate(set, flow.ValidateContext{})
	got := map[string]string{}
	for _, p := range check.Problems {
		if p.Code != "audio.not_yet" {
			t.Errorf("problem = %+v, want audio.not_yet", p)
		}
		got[p.Field] = p.Message
	}
	want := map[string]string{
		"keepOriginal": "Keeping the original track as well is not built yet.",
	}
	for field, message := range want {
		if got[field] != message {
			t.Errorf("%s = %q, want %q", field, got[field], message)
		}
	}

	// The defaults are the engine's behaviour now: nothing to report.
	def, err := flow.DecodeSettings(AudioStep{}.Def(), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if problems := (AudioStep{}).Validate(def, flow.ValidateContext{}).Problems; len(problems) != 0 {
		t.Errorf("defaults problems = %+v, want none", problems)
	}
}

func TestAudioDefaults(t *testing.T) {
	s, err := flow.DecodeSettings(AudioStep{}.Def(), []byte(`{}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := s.(AudioSettings)
	if got.Format != "copy" || got.Channels != "source" {
		t.Errorf("default = %+v, want copy and the source's channels", got)
	}
}

func greyed(disabled []flow.Disabled, field string) bool {
	for _, d := range disabled {
		if d.Field == field {
			return true
		}
	}
	return false
}
