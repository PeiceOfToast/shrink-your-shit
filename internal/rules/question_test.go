// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

func sample() media.Info {
	return media.Info{Container: "matroska,webm", Ext: ".mkv", VideoCodec: "h264", BitrateKbps: 20000, DurationSec: 95 * 60, SizeBytes: 150 << 20, Chapters: 12,
		Streams: []media.Stream{{Type: "video", Codec: "h264", Width: 1920, Height: 1080, FrameRate: 24, PixFmt: "yuv420p10le", BitDepth: 10},
			{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "eng"}, {Index: 2, Type: "audio", Codec: "aac", Channels: 2, Language: "deu"},
			{Index: 3, Type: "subtitle", Codec: "subrip", Language: "eng"}}}
}

// FB1b: every kind of question answers from the probe or the run's facts, and says why.
func TestQuestionsAsk(t *testing.T) {
	info := sample()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	f := Facts{Vars: map[string]string{"profile": "remux", "score": "42"}, Path: "/media/movies/Harrow Lane (2019)/Harrow.Lane.2019.1080p.Remux.mkv",
		Library: "Movies", LibraryID: "7", HardLinks: true, ModTime: now.AddDate(0, 0, -30), Now: now}
	cases := []struct {
		name string
		q    Question
		yes  bool
	}{
		{"file: 10-bit", Question{VideoCheck: VideoCheck{BitDepth: 10}}, true},
		{"file: 8-bit", Question{VideoCheck: VideoCheck{BitDepth: 8}}, false},
		{"file: audio codec", Question{VideoCheck: VideoCheck{AudioCodecs: []string{"dts", "eac3"}}}, true},
		{"file: channels", Question{VideoCheck: VideoCheck{MinChannels: 8}}, false},
		{"file: audio language", Question{VideoCheck: VideoCheck{AudioLanguages: []string{"deu"}}}, true},
		{"file: subtitle language", Question{VideoCheck: VideoCheck{SubtitleLanguages: []string{"fra"}}}, false},
		{"file: minutes", Question{VideoCheck: VideoCheck{MinMinutes: 90}}, true},
		{"file: chapters", Question{VideoCheck: VideoCheck{Chapters: "no"}}, false},
		{"file: two conditions, one fails", Question{VideoCheck: VideoCheck{Resolution: 1080, HDR: "yes"}}, false},
		{"name: contains", Question{Kind: "name", Pattern: "remux", Match: "contains"}, true},
		{"name: case", Question{Kind: "name", Pattern: "remux", Match: "contains", CaseSensitive: true}, false},
		{"name: starts", Question{Kind: "name", Pattern: "Harrow", Match: "starts"}, true},
		{"name: ends", Question{Kind: "name", Pattern: ".mp4", Match: "ends"}, false},
		{"name: glob", Question{Kind: "name", Pattern: "*.1080p.*", Match: "glob"}, true},
		{"name: regex", Question{Kind: "name", Pattern: `\.20\d\d\.`, Match: "regex"}, true},
		{"name: bad regex is No", Question{Kind: "name", Pattern: `(`, Match: "regex"}, false},
		{"name: regex keeps its escapes", Question{Kind: "name", Pattern: `^\S+\.mkv$`, Match: "regex"}, true},
		{"name: regex keeps unicode classes", Question{Kind: "name", Pattern: `^[\p{Lu}][\p{Ll}]+\.`, Match: "regex"}, true},
		{"name: regex insensitive", Question{Kind: "name", Pattern: `^harrow`, Match: "regex"}, true},
		{"name: regex sensitive", Question{Kind: "name", Pattern: `^harrow`, Match: "regex", CaseSensitive: true}, false},
		{"variable: value names a variable", Question{Kind: "variable", Variable: "profile", Op: "equals", Value: "{{profile}}"}, true},
		{"variable: value names another", Question{Kind: "variable", Variable: "profile", Op: "contains", Value: "{{score}}"}, false},
		{"path: contains", Question{Kind: "name", Pattern: "/media/movies/", Match: "contains", Where: "path"}, true},
		{"age: older", Question{Kind: "age", OlderDays: 20}, true},
		{"age: not that old", Question{Kind: "age", OlderDays: 60}, false},
		{"age: younger", Question{Kind: "age", YoungerDays: 60}, true},
		{"age: between", Question{Kind: "age", OlderDays: 10, YoungerDays: 20}, false},
		{"variable: set", Question{Kind: "variable", Variable: "profile", Op: "set"}, true},
		{"variable: unset", Question{Kind: "variable", Variable: "missing", Op: "unset"}, true},
		{"variable: equals", Question{Kind: "variable", Variable: "profile", Op: "equals", Value: "Remux"}, true},
		{"variable: contains", Question{Kind: "variable", Variable: "profile", Op: "contains", Value: "mux"}, true},
		{"variable: more", Question{Kind: "variable", Variable: "score", Op: "more", Value: "40"}, true},
		{"variable: less", Question{Kind: "variable", Variable: "score", Op: "less", Value: "40"}, false},
		{"variable: not a number", Question{Kind: "variable", Variable: "profile", Op: "more", Value: "1"}, false},
		{"variable: missing equals", Question{Kind: "variable", Variable: "missing", Op: "equals", Value: "x"}, false},
		{"library: yes", Question{Kind: "library", Libraries: []string{"3", "7"}}, true},
		{"library: no", Question{Kind: "library", Libraries: []string{"3"}}, false},
		{"hardlinks", Question{Kind: "hardlinks"}, true},
	}
	for _, c := range cases {
		yes, why := c.q.Ask(info, f)
		if yes != c.yes || why == "" {
			t.Errorf("%s: %v (%q), want %v", c.name, yes, why, c.yes)
		}
	}
	// no facts: a variable question answers No and says so; the file's own facts still answer
	if yes, why := (Question{Kind: "variable", Variable: "profile", Op: "set"}).Ask(info, Facts{}); yes || why == "" {
		t.Errorf("no facts: %v %q", yes, why)
	}
	if yes, _ := (Question{VideoCheck: VideoCheck{Resolution: 1080}}).Ask(info, Facts{}); !yes {
		t.Error("the file's facts need no run facts")
	}
	if yes, why := (Question{Kind: "age", OlderDays: 1}).Ask(info, Facts{}); yes || why == "" {
		t.Errorf("no date: %v %q", yes, why)
	}
}

// A stored check from before FB1b reads as a Question of the file's facts, and writes back the same bytes.
func TestQuestionKeepsOldChecks(t *testing.T) {
	old := `{"nodeId":"c1","port":"match","check":{"name":"Is it 4K?","resolution":2160}}`
	var bc BranchCheck
	if err := json.Unmarshal([]byte(old), &bc); err != nil || bc.Check.Kind != "" || bc.Check.Resolution != 2160 || bc.Check.Name != "Is it 4K?" {
		t.Fatalf("%v %+v", err, bc)
	}
	b, _ := json.Marshal(bc)
	if string(b) != old {
		t.Errorf("writes back %s", b)
	}
	if yes, _ := bc.Check.Ask(media.Info{Streams: []media.Stream{{Type: "video", Width: 3840, Height: 2160}}}, Facts{}); !yes {
		t.Error("an old check no longer answers")
	}
	// a plan without before-cards or a fail verdict carries neither key, so stored plans and fingerprints are unchanged
	p := Plan{Kind: KindBranches, Skip: "left alone"}
	if b, _ := json.Marshal(p); strings.Contains(string(b), `"before"`) || strings.Contains(string(b), `"fail"`) {
		t.Errorf("plan: %s", b)
	}
	b2, _ := json.Marshal(Branch{End: "x"})
	if strings.Contains(string(b2), `"before"`) || strings.Contains(string(b2), `"fail"`) {
		t.Errorf("branch: %s", b2)
	}
}

// Empty says when a question would answer Yes for every file.
func TestQuestionEmpty(t *testing.T) {
	for _, q := range []Question{{}, {Kind: "name"}, {Kind: "age"}, {Kind: "variable"}, {Kind: "library"}} {
		if !q.Empty() {
			t.Errorf("%+v should be empty", q)
		}
	}
	for _, q := range []Question{{VideoCheck: VideoCheck{Chapters: "yes"}}, {Kind: "name", Pattern: "x"}, {Kind: "age", YoungerDays: 1}, {Kind: "variable", Variable: "v"}, {Kind: "library", Libraries: []string{"1"}}, {Kind: "hardlinks"}} {
		if q.Empty() {
			t.Errorf("%+v should not be empty", q)
		}
	}
}
