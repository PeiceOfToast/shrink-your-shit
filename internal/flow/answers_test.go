// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	_ "github.com/PeiceOfToast/shrink-your-shit/internal/flow/steps"
)

// answersWith is the guide's default answers with the changes applied.
func answersWith(changes map[string]any) flow.Answers {
	a := flow.DefaultAnswers()
	for key, value := range changes {
		a[key] = value
	}
	return a
}

// TestEveryStarterFitsTheGuide is flow-model.md's test for the starters: each guided starter reads
// back as answers, and those answers rebuild it and validate with nothing to fix.
func TestEveryStarterFitsTheGuide(t *testing.T) {
	for _, starter := range flow.Starters() {
		if !starter.Guided {
			continue
		}
		t.Run(starter.ID, func(t *testing.T) {
			answers, ok := flow.ToAnswers(starter.Flow)
			if !ok {
				t.Fatalf("the starter %q does not fit the guide, but it is offered as guided", starter.ID)
			}
			rebuilt, problems := flow.FromAnswers(answers, flow.DefaultAnswers())
			if len(problems) > 0 {
				t.Fatalf("FromAnswers on the starter's own answers = %+v, want no problems", problems)
			}
			back, ok := flow.ToAnswers(rebuilt)
			if !ok || !reflect.DeepEqual(flow.NormalizeAnswers(back, flow.DefaultAnswers()),
				flow.NormalizeAnswers(answers, flow.DefaultAnswers())) {
				t.Errorf("the starter's answers change on the way through the mapping: %v, ok=%v", back, ok)
			}
			rep := flow.Validate(rebuilt, flow.ValidateContext{Flow: rebuilt})
			if !rep.OK() {
				t.Errorf("the rebuilt starter has problems: %+v", rep.Problems)
			}
		})
	}
}

// answerCase is one answers object and whether the editor can publish it today. The engine's
// item-6/7/8 gates mean a few of the guide's answers are stored and previewed but not yet
// buildable; those rows still have to round-trip exactly.
type answerCase struct {
	name      string
	changes   map[string]any
	buildable bool
}

var answerCases = []answerCase{
	{name: "the defaults", changes: nil, buildable: true},
	{name: "a different format and size", changes: map[string]any{
		"format": "h264", "resolution": "480", "useGpu": false, "quality": 5,
	}},
	{name: "slower", changes: map[string]any{"slower": true}},
	{name: "AV1 at 1080p", changes: map[string]any{"format": "av1", "resolution": "1080", "quality": 4}},
	{name: "stereo Opus", changes: map[string]any{"audio": "stereo_opus"}},
	{name: "AAC with the source's channels", changes: map[string]any{"audio": "stereo_aac_surround"}},
	{name: "E-AC-3 surround", changes: map[string]any{"audio": "eac3_surround"}},
	{name: "AC-3 surround", changes: map[string]any{"audio": "ac3_surround"}},
	{name: "leave the audio as it is", changes: map[string]any{
		"audio": "as_is", "allLanguages": true, "subtitleLanguages": []string{},
	}, buildable: true},
	{name: "leave the audio, H.264", changes: map[string]any{
		"audio": "as_is", "format": "h264", "allLanguages": true, "subtitleLanguages": []string{},
	}, buildable: true},
	{name: "the starter's own answers", changes: map[string]any{
		"audio": "as_is", "useGpu": false, "allLanguages": true, "subtitleLanguages": []string{},
		"nameSuffix": "shrunk",
	}, buildable: true},
	{name: "my languages, spread everywhere", changes: map[string]any{
		"languages": []string{"eng", "jpn"}, "languagesEverywhere": true, "subtitles": "my_languages",
		"subtitleLanguages": []string{"eng", "jpn"},
	}},
	{name: "every language", changes: map[string]any{
		"allLanguages": true, "languages": []string{}, "subtitleLanguages": []string{},
	}, buildable: true},
	{name: "text-only subtitles", changes: map[string]any{"subtitles": "text_only"}},
	{name: "SRT subtitles", changes: map[string]any{"subtitles": "srt"}},
	{name: "only my subtitle languages", changes: map[string]any{
		"subtitles": "my_languages", "subtitleLanguages": []string{"ger"},
	}},
	{name: "MP4", changes: map[string]any{"fileType": "mp4"}, buildable: true},
	{name: "keep the container", changes: map[string]any{"fileType": "keep"}, buildable: true},
	{name: "a different folder", changes: map[string]any{
		"original": "one_folder", "folder": "/media/converted",
	}},
	{name: "a folder beside the library", changes: map[string]any{
		"original": "beside_library", "folder": "/media/Movies (smaller)",
	}},
	{name: "replace the original", changes: map[string]any{"original": "replace"}},
	{name: "keep Dolby Vision", changes: map[string]any{"includeDolbyVision": true}},
	{name: "a name suffix", changes: map[string]any{"nameSuffix": "small"}, buildable: true},
}

// TestAnswersRoundTrip is flow-model.md's table test: reading the answers back gives the same
// answers, and the answers the guide offers today validate clean when the editor can build them.
func TestAnswersRoundTrip(t *testing.T) {
	for _, tc := range answerCases {
		t.Run(tc.name, func(t *testing.T) {
			answers := answersWith(tc.changes)
			rebuilt, problems := flow.FromAnswers(answers, flow.DefaultAnswers())
			if len(problems) > 0 {
				t.Fatalf("FromAnswers = %+v, want no problems", problems)
			}
			got, ok := flow.ToAnswers(rebuilt)
			if !ok {
				t.Fatal("ToAnswers refused the flow the answers just made")
			}
			want := flow.NormalizeAnswers(answers, flow.DefaultAnswers())
			got = flow.NormalizeAnswers(got, flow.DefaultAnswers())
			if !reflect.DeepEqual(got, want) {
				t.Errorf("ToAnswers(FromAnswers(a)) =\n %#v\nwant %#v", got, want)
			}
			rep := flow.Validate(rebuilt, flow.ValidateContext{Flow: rebuilt})
			if tc.buildable && !rep.OK() {
				t.Errorf("problems = %+v, want none: the editor can build this flow today", rep.Problems)
			}
			if !tc.buildable && rep.OK() {
				t.Log("this answer row is buildable now; move it into the buildable set")
			}
		})
	}
}

// TestBesideLibraryNeedsItsOwnFolder is the one answer that needs a folder to keep its name: a
// sibling folder ending in " (smaller)". Any other folder is the plain "a different folder".
func TestBesideLibraryNeedsItsOwnFolder(t *testing.T) {
	for _, tc := range []struct {
		name   string
		folder string
	}{
		{"plain folder", "/media/converted"},
		{"the suffix alone", "/media/ (smaller)"},
		{"the suffix in the middle", "/media/Movies (smaller)/Clips"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			answers := answersWith(map[string]any{"original": "beside_library", "folder": tc.folder})
			f, problems := flow.FromAnswers(answers, flow.DefaultAnswers())
			if len(problems) > 0 {
				t.Fatalf("FromAnswers = %+v", problems)
			}
			got, ok := flow.ToAnswers(f)
			if !ok {
				t.Fatal("ToAnswers refused the flow")
			}
			if got["original"] != "one_folder" {
				t.Errorf("original = %v, want one_folder: %q is not a library's (smaller) sibling", got["original"], tc.folder)
			}
		})
	}
	// The exact shape does round-trip, and it is the one a library's parent folder gives.
	answers := answersWith(map[string]any{"original": "beside_library", "folder": "/media/Movies (smaller)"})
	f, _ := flow.FromAnswers(answers, flow.DefaultAnswers())
	got, ok := flow.ToAnswers(f)
	if !ok || got["original"] != "beside_library" || got["folder"] != "/media/Movies (smaller)" {
		t.Fatalf("the library sibling folder read back as %v, ok=%v; want beside_library in /media/Movies (smaller)", got, ok)
	}
}

// TestToAnswersRefusesWhatTheGuideCannotSay: a flow edited on page 07 into a setting the guide has
// no answer for does not fit, so the page keeps the person in the editor.
func TestToAnswersRefusesWhatTheGuideCannotSay(t *testing.T) {
	base, _ := flow.FromAnswers(flow.DefaultAnswers(), flow.DefaultAnswers())
	for _, tc := range []struct {
		name  string
		step  int
		patch string
	}{
		{"a bitrate", 0, `{"rateControl":"vbr"}`},
		{"a custom speed", 0, `{"preset":"faster"}`},
		{"a frame rate", 0, `{"fps":30}`},
		{"an audio bitrate", 1, `{"bitrateKbps":128}`},
		{"dropping commentary", 1, `{"commentary":"drop"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := base
			f.Steps = append([]flow.StepRef(nil), base.Steps...)
			f.Steps[tc.step].Settings = mergeJSON(t, base.Steps[tc.step].Settings, tc.patch)
			if _, ok := flow.ToAnswers(f); ok {
				t.Errorf("ToAnswers accepted a flow with %s; the guide has no answer for it", tc.name)
			}
		})
	}
}

// TestToAnswersRefusesAFlowTheRebuildChanges is the exactness gate's own test: every mapped field
// is one the guide offers, and the flow validates, so the field-by-field reads all succeed, but the
// answers the mapping makes rebuild a different flow (a folder left behind when the page switched
// back to next-to). ToAnswers must refuse, so the page keeps the person in the editor instead of
// saving answers that quietly drop a setting they can see (#840 review).
func TestToAnswersRefusesAFlowTheRebuildChanges(t *testing.T) {
	f, problems := flow.FromAnswers(flow.DefaultAnswers(), flow.DefaultAnswers())
	if len(problems) > 0 {
		t.Fatalf("FromAnswers on the defaults = %+v", problems)
	}
	// The editor keeps the folder when the where choice moves back to next-to, so this is a flow a
	// person can save and publish; nothing about it is invalid.
	f.Steps = append([]flow.StepRef(nil), f.Steps...)
	f.Steps[2].Settings = mergeJSON(t, f.Steps[2].Settings, `{"folder":"/media/kept"}`)
	if rep := flow.Validate(f, flow.ValidateContext{Flow: f}); !rep.OK() {
		t.Fatalf("the flow has problems, so it is not the case this test is about: %+v", rep.Problems)
	}
	if answers, ok := flow.ToAnswers(f); ok {
		t.Errorf("ToAnswers accepted a flow whose rebuild drops its folder: %#v", answers)
	}
	// The same flow saves as a real editor draft and reports fitsGuided false.
	store, ctx := workflowStore(t)
	id, _, err := store.Create(ctx, f, "editor")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	info, err := store.Draft(ctx, id)
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if info.Answers != nil {
		t.Errorf("answers = %#v; want none for a flow ToAnswers refuses", info.Answers)
	}
}

// TestFromAnswersProblemNamesTheField: page 06 shows the first problem on its field with the
// invalid_answer code, so the mapping has to name both.
func TestFromAnswersProblemNamesTheField(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		value any
	}{
		{"quality", "quality", 9},
		{"quality is not a number", "quality", "good"},
		{"format", "format", "vp9"},
		{"audio", "audio", "stereo_mp3"},
		{"original", "original", "elsewhere"},
		{"languages", "languages", []any{"eng", 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := flow.FromAnswers(map[string]any{tc.key: tc.value}, flow.DefaultAnswers())
			if len(problems) == 0 {
				t.Fatal("no problem for an answer the guide does not offer")
			}
			first := problems[0]
			if first.Field != tc.key || first.Code != "invalid_answer" || first.Message == "" {
				t.Errorf("problem = %+v, want %s and invalid_answer", first, tc.key)
			}
		})
	}
	if _, problems := flow.FromAnswers(flow.DefaultAnswers(), flow.DefaultAnswers()); len(problems) > 0 {
		t.Errorf("the default answers are problems: %+v", problems)
	}
}

// TestSentencesMatchPage06 is the preview snapshot: page 06's "What this does" sentences, word for
// word, from the answers the page's mockFlow.ts draws.
func TestSentencesMatchPage06(t *testing.T) {
	for _, tc := range []struct {
		name    string
		changes map[string]any
		want    []string
	}{
		{
			// Page 06's default audio is stereo AAC now that item 7 built conversion.
			name: "the defaults",
			want: []string{
				"Converts video to HEVC at quality 3, keeping the picture size, on the graphics card when it can.",
				"Audio: AAC, stereo, every language.",
				"Keeps every subtitle.",
				"Saves as MKV.",
				"Puts the new file next to the original; the original stays where it is.",
			},
		},
		{
			name: "1080p on the processor, a folder, E-AC-3",
			changes: map[string]any{
				"resolution": "1080", "useGpu": false, "audio": "eac3_surround",
				"subtitles": "my_languages", "subtitleLanguages": []string{"eng"},
				"original": "one_folder", "folder": "/media/converted",
			},
			want: []string{
				"Converts video to HEVC at quality 3, at most 1080p.",
				"Audio: E-AC-3, same channels as the source, every language.",
				"Keeps subtitles in English.",
				"Saves as MKV.",
				"Puts the new file in /media/converted; the original stays where it is.",
			},
		},
		{
			name: "every language, text subtitles, the same container",
			changes: map[string]any{
				"allLanguages": true, "subtitles": "text_only", "fileType": "keep",
			},
			want: []string{
				"Converts video to HEVC at quality 3, keeping the picture size, on the graphics card when it can.",
				"Audio: AAC, stereo, every language.",
				"Only text subtitles are kept.",
				"The file keeps its container.",
				"Puts the new file next to the original; the original stays where it is.",
			},
		},
		{
			name:    "SRT files, replacing the original",
			changes: map[string]any{"subtitles": "srt", "original": "replace"},
			want: []string{
				"Converts video to HEVC at quality 3, keeping the picture size, on the graphics card when it can.",
				"Audio: AAC, stereo, every language.",
				"Subtitles are made into SRT files.",
				"Saves as MKV.",
				"Replaces the original. The old file goes to the trash and is kept 7 days.",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, problems := flow.FromAnswers(answersWith(tc.changes), flow.DefaultAnswers())
			if len(problems) > 0 {
				t.Fatalf("FromAnswers = %+v", problems)
			}
			got := flow.Sentences(f)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("sentences =\n %s\nwant\n %s", strings.Join(got, "\n "), strings.Join(tc.want, "\n "))
			}
		})
	}
}

// TestTheGuideGreysWhereItCannotSave: page 06 offers replace and both folder choices but greys
// them with the editor's own sentences, and a new workflow's answers start next to the original.
func TestTheGuideGreysWhereItCannotSave(t *testing.T) {
	guide := flow.Guide()
	if got := guide.Defaults["original"]; got != "next_to_original" {
		t.Errorf("default original = %v, want next_to_original", got)
	}
	want := map[string]string{
		"replace":        flow.ReplaceWhy,
		"one_folder":     flow.FolderWhy,
		"beside_library": flow.FolderWhy,
	}
	seen := map[string]bool{}
	for _, part := range guide.Parts {
		for _, field := range part.Fields {
			if field.Key != "original" {
				continue
			}
			for _, choice := range field.Choices {
				why, greyed := want[choice.Value]
				if !greyed {
					if !choice.Available {
						t.Errorf("%s is greyed: %+v", choice.Value, choice)
					}
					continue
				}
				seen[choice.Value] = true
				if choice.Available || choice.Why != why {
					t.Errorf("%s = %+v, want greyed with %q", choice.Value, choice, why)
				}
			}
		}
	}
	for value := range want {
		if !seen[value] {
			t.Errorf("the guide has no %s choice to grey", value)
		}
	}
}

// TestTheGuideOffersTheAudioChoices: item 7 builds audio conversion, so the chip, the languages
// field and every audio choice are offered, and the default is page 06's stereo AAC.
func TestTheGuideOffersTheAudioChoices(t *testing.T) {
	if got := flow.DefaultAnswers()["audio"]; got != "stereo_aac" {
		t.Errorf("default audio = %v, want stereo_aac", got)
	}
	guide := flow.Guide()
	var chip *flow.GuideChip
	for i := range guide.Chips {
		if guide.Chips[i].ID == "keep_my_languages" {
			chip = &guide.Chips[i]
		}
	}
	if chip == nil {
		t.Fatal("the guide has no keep_my_languages chip")
	}
	if !chip.Available || chip.Why != "" {
		t.Errorf("chip = %+v; want available", *chip)
	}
	var languages *flow.GuideField
	for _, part := range guide.Parts {
		for i := range part.Fields {
			switch field := &part.Fields[i]; field.Key {
			case "languages":
				languages = field
			case "audio":
				for _, choice := range field.Choices {
					if !choice.Available || choice.Why != "" {
						t.Errorf("audio choice %q = %+v; want available", choice.Value, choice)
					}
				}
			}
		}
	}
	if languages == nil {
		t.Fatal("the guide has no languages field")
	}
	if languages.Available != nil || languages.Why != "" {
		t.Errorf("languages field = %+v; want an ordinary field", *languages)
	}
}

// mergeJSON is a step's settings with the patch's fields added or replaced.
func mergeJSON(t *testing.T, base json.RawMessage, patch string) json.RawMessage {
	t.Helper()
	var fields, added map[string]any
	if err := json.Unmarshal(base, &fields); err != nil {
		t.Fatalf("read the step's settings: %v", err)
	}
	if err := json.Unmarshal([]byte(patch), &added); err != nil {
		t.Fatalf("read the patch: %v", err)
	}
	for key, value := range added {
		fields[key] = value
	}
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
