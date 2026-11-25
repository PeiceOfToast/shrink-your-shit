// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"reflect"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Every goal's defaults build a workflow that validates, keeps the original, asks for nothing afterwards
// (ui-shop G1 item 5) and reads back as exactly those answers.
func TestGuideDefaultsAreSafeAndReadBack(t *testing.T) {
	allFormats(t)
	for _, goal := range []string{GoalSmaller, GoalEveryDevice, GoalBoth} {
		a := DefaultAnswers(goal)
		if a.Original == OriginalReplace || a.IfBigger != "keep" || a.After != (After{}) || a.Goal != goal {
			t.Fatalf("%s: a default must keep the original and ask for nothing afterwards: %+v", goal, a)
		}
		if iss := Check(a); len(iss) > 0 {
			t.Fatalf("%s: %+v", goal, iss)
		}
		d := Build(a)
		if r := Validate(d); r.Errors() {
			t.Fatalf("%s: %+v", goal, r.Issues)
		}
		if _, iss := ToTemplate(d, rules.Template{}); len(iss) > 0 {
			t.Fatalf("%s: compile: %+v", goal, iss)
		}
		g := GuideOf(d)
		if g.Coverage != CoverageFull || g.Answers == nil || !reflect.DeepEqual(*g.Answers, Fill(a)) {
			t.Fatalf("%s: read back %s %+v\nwant %+v", goal, g.Coverage, g.Answers, Fill(a))
		}
	}
	d := Build(DefaultAnswers(GoalSmaller)) // the default is today's preset, step for step
	p := Preset()
	if got, want := stepTypesOf(d), stepTypesOf(p); !reflect.DeepEqual(got, want) {
		t.Errorf("steps %v, the preset's are %v", got, want)
	}
	if c := guideConfig(d, TypeCommit); c["policy"] != "separate_folder" || c["folderMode"] != "next_to_library" || c["ifBigger"] != "keep" {
		t.Errorf("save: %v", c)
	}
	if c := guideConfig(Build(DefaultAnswers(GoalEveryDevice)), TypeConvert); c["format"] != "h264" {
		t.Errorf("every device: %v", c)
	}
}

// Every answer round-trips: the conditions in front, one folder for all, the cards after the save,
// slower, every language, replace.
func TestGuideRoundTripsEveryAnswer(t *testing.T) {
	allFormats(t)
	a := Answers{Goal: GoalBoth, Quality: 4, Slower: true, Resolution: "1080", Audio: "eac3_surround", AllLanguages: true,
		Subtitles: "text_only", FileType: "mkv", Original: OriginalOneFolder, Folder: "/media/shrunk", IfBigger: "replace",
		Skip: Skip{MinSizeGB: 1.5, MinResolution: 1080, MinMinutes: 20}, IncludeDolbyVision: true,
		After: After{MediaServer: true, Downloader: true, Notify: true, NotifyTarget: "Phone"}}
	if iss := Check(a); len(iss) > 0 {
		t.Fatal(iss)
	}
	d := Build(a)
	if r := Validate(d); r.Errors() {
		t.Fatalf("%+v", r.Issues)
	}
	if _, iss := ToTemplate(d, rules.Template{}); len(iss) > 0 {
		t.Fatalf("compile: %+v", iss)
	}
	g := GuideOf(d)
	want := Fill(a)
	want.Goal = GoalSmaller // the goal isn't stored: HEVC in MKV reads as "smaller"
	if g.Coverage != CoverageFull || !reflect.DeepEqual(*g.Answers, want) {
		t.Fatalf("read back %s %+v\nwant %+v (why %q)", g.Coverage, g.Answers, want, g.Why)
	}
	a.Goal = GoalSmaller
	if got := Steps(d); strings.Join(got, " > ") != "Library files > Check the file > Leave some files alone > Convert video > Audio > Subtitles > File type > Check the result > Save the result > Tell the media server > Tell the downloader > Send a notification" {
		t.Errorf("steps: %v", got)
	}
	if c := guideConfig(d, TypeCheck); intOf(c, "size", 0) != 1536 || intOf(c, "resolution", 0) != 1080 || intOf(c, "minMinutes", 0) != 20 {
		t.Errorf("the conditions: %v", c)
	}
	// the conditions taken away again: the pair goes, the input links straight on
	a.Skip, a.After = Skip{}, After{Downloader: true}
	d2 := Apply(d, a)
	if r := Validate(d2); r.Errors() {
		t.Fatalf("%+v", r.Issues)
	}
	g2 := GuideOf(d2)
	if g2.Coverage != CoverageFull || !reflect.DeepEqual(*g2.Answers, Fill(a)) || len(d2.Nodes) != len(PresetOrder)+1 {
		t.Errorf("after taking them away: %s %+v, %d nodes", g2.Coverage, g2.Answers, len(d2.Nodes))
	}
}

// A workflow with steps the questions don't show reads as partial, names them, and keeps them when the
// answers are applied; one the questions can't read says why.
func TestGuideReadsAHandEditedWorkflow(t *testing.T) {
	allFormats(t)
	d := Build(DefaultAnswers(GoalSmaller))
	commit, input := nodeOf(d, TypeCommit), nodeOf(d, TypeInput)
	tag := node(TypeTag, "", map[string]any{"add": "shrunk"})
	note := node(TypeNote, "", map[string]any{"text": "starting"})
	d.Nodes = append(d.Nodes, tag, note)
	d.Edges = append(d.Edges, link(commit.ID, "success", tag.ID))
	for i := range d.Edges { // the note sits between the input and the skips
		if d.Edges[i].From == input.ID {
			d.Edges = append(d.Edges, link(note.ID, "success", d.Edges[i].To))
			d.Edges[i].To = note.ID
		}
	}
	g := GuideOf(d)
	if g.Coverage != CoveragePartial || strings.Join(g.Extra, ", ") != "Add a note to the log, Tag the file" || g.Answers == nil {
		t.Fatalf("%+v", g)
	}
	a := *g.Answers
	a.Quality, a.Skip, a.After = 2, Skip{MinSizeGB: 2}, After{MediaServer: true}
	d2 := Apply(d, a)
	if r := Validate(d2); r.Errors() {
		t.Fatalf("%+v", r.Issues)
	}
	g2 := GuideOf(d2)
	if g2.Coverage != CoveragePartial || !reflect.DeepEqual(*g2.Answers, Fill(a)) || strings.Join(g2.Extra, ", ") != "Add a note to the log, Tag the file" {
		t.Fatalf("after applying: %+v\nwant %+v", g2, Fill(a))
	}
	if got := Steps(d2); strings.Join(got, " > ") != "Library files > Check the file > Add a note to the log > Leave some files alone > Convert video > Audio > Subtitles > File type > Check the result > Save the result > Tell the media server > Tag the file" {
		t.Errorf("steps: %v", got)
	}
	// the steps' settings Basic doesn't ask ride along as More: read back as stored, written back unchanged,
	// changed or taken away only through More (the Advanced flow)
	for i := range d2.Nodes {
		if d2.Nodes[i].Type == TypeConvert {
			d2.Nodes[i].Config["hardware"] = "cpu"
		}
	}
	g3 := GuideOf(d2)
	if g3.Coverage != CoveragePartial || !reflect.DeepEqual(g3.Answers.More, map[string]map[string]any{TypeConvert: {"hardware": "cpu"}}) {
		t.Fatalf("%+v", g3.Answers.More)
	}
	if c := guideConfig(Apply(d2, a), TypeConvert); c["hardware"] != "cpu" {
		t.Errorf("hardware changed without More: %v", c)
	}
	more := *g3.Answers
	more.More[TypeConvert] = map[string]any{"hardware": nil, "removeBlackBars": true, "quality": 5}
	if c := guideConfig(Apply(d2, more), TypeConvert); c["hardware"] != nil || c["removeBlackBars"] != true || intOf(c, "quality", 0) != 2 {
		t.Errorf("through More: %v", c)
	}
	// can't be read: a question in the conversion's way, the earlier install's settings, a reordered conversion
	q := node(TypeCheckName, "", map[string]any{"pattern": "x"})
	d3 := clone(d)
	d3.Nodes = append(d3.Nodes, q)
	for i := range d3.Edges {
		if d3.Edges[i].From == note.ID {
			d3.Edges = append(d3.Edges, link(q.ID, "match", d3.Edges[i].To))
			d3.Edges[i].To = q.ID
		}
	}
	if g := GuideOf(d3); g.Coverage != CoverageNone || g.Answers != nil || !strings.Contains(g.Why, "'Check the file name' sits where the questions can't show it") || !strings.HasSuffix(g.Why, "Open it in Advanced.") {
		t.Errorf("a question: %+v", g)
	}
	if g := GuideOf(FromTemplate(rules.Template{Kind: rules.KindLegacy}, nil)); g.Coverage != CoverageNone || !strings.Contains(g.Why, "earlier install") {
		t.Errorf("legacy: %+v", g)
	}
	d4 := Build(DefaultAnswers(GoalSmaller))
	au, sub := nodeOf(d4, TypeAudio), nodeOf(d4, TypeSubtitles)
	for i := range d4.Nodes { // swap Audio and Subtitles
		switch d4.Nodes[i].ID {
		case au.ID:
			d4.Nodes[i].ID = "swap"
		case sub.ID:
			d4.Nodes[i].ID = au.ID
		}
	}
	for i := range d4.Nodes {
		if d4.Nodes[i].ID == "swap" {
			d4.Nodes[i].ID = sub.ID
		}
	}
	if g := GuideOf(d4); g.Coverage != CoverageNone || !strings.Contains(g.Why, "usual order") {
		t.Errorf("reordered: %+v", g)
	}
	if got := Apply(d4, a); !reflect.DeepEqual(got, d4) {
		t.Errorf("Apply on a workflow it can't read must change nothing")
	}
}

// A wrong answer is refused with the question it belongs to.
func TestGuideCheckNamesTheQuestion(t *testing.T) {
	a := Fill(Answers{Format: "mpeg2", Original: OriginalOneFolder, Skip: Skip{MinResolution: 900}, Languages: []string{"english"}})
	iss := Check(a)
	var fields []string
	for _, i := range iss {
		fields = append(fields, i.Field)
	}
	if strings.Join(fields, ",") != "folder,format,languages,skip.minResolution" {
		t.Errorf("%v: %+v", fields, iss)
	}
	if iss[1].Message != "'mpeg2' is not a choice here." {
		t.Errorf("%q", iss[1].Message)
	}
}

// The summary says what the defaults do, in the sentences the last page shows; a running-time question
// saved without its seconds reads with the default.
func TestGuideSummary(t *testing.T) {
	got := Summary(DefaultAnswers(GoalSmaller))
	want := []string{
		"Shrink videos to HEVC at quality 3, keeping the picture size.",
		"Keep a stereo track and the surround track as it is, in your languages from Settings; tracks with no language set are kept.",
		"Keep every subtitle.",
		"Save as MKV in a folder beside each library; the original stays where it is. If the new file is bigger, the original is kept.",
		"Files already HEVC, AV1 or VP9 are left alone, and so are Dolby Vision files.",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	got = Summary(Answers{Goal: GoalEveryDevice, Original: OriginalReplace, Languages: []string{"eng", "ger"}, Skip: Skip{MinSizeGB: 0.5, MinMinutes: 20},
		After: After{Notify: true, NotifyTarget: "Phone"}})
	for _, s := range []string{"Shrink videos to H.264 at quality 3", "surround becomes stereo, in eng, ger;", "Keep text subtitles only",
		"replace the original; the original is kept in the trash", "Files smaller than 0.5 GB, or shorter than 20 minutes are left alone.", "Afterwards, send a message to Phone."} {
		if !strings.Contains(strings.Join(got, "\n"), s) {
			t.Errorf("missing %q in\n%s", s, strings.Join(got, "\n"))
		}
	}
}

func stepTypesOf(d Doc) []string {
	var out []string
	for _, n := range d.Nodes {
		out = append(out, n.Type)
	}
	return out
}

func nodeOf(d Doc, typ string) Node {
	for _, n := range d.Nodes {
		if n.Type == typ {
			return n
		}
	}
	return Node{}
}

func guideConfig(d Doc, typ string) map[string]any { return nodeOf(d, typ).Config }

// allFormats lets the test ffmpeg make every format, the way a real one with x264, SVT-AV1 and libvpx does.
func allFormats(t *testing.T) {
	t.Helper()
	rules.SetAvailableFormats(map[string]bool{"libx265": true, "libx264": true, "libsvtav1": true, "libvpx-vp9": true, "aac": true, "libopus": true, "eac3": true, "ac3": true, "srt": true})
	t.Cleanup(func() { rules.SetAvailableFormats(nil) })
}
