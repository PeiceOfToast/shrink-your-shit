// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

func roundTrip(t *testing.T, d Doc) Doc {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var out Doc
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func files() []media.Info {
	v := func(codec string, w, h, kbps, depth int, extra ...media.Stream) media.Info {
		s := append([]media.Stream{{Type: "video", Codec: codec, Width: w, Height: h, FrameRate: 24, BitDepth: depth, BitrateKbps: kbps}}, extra...)
		return media.Info{Container: "matroska,webm", Ext: ".mkv", VideoCodec: codec, BitrateKbps: kbps + 640, DurationSec: 3600, Streams: s}
	}
	aac := media.Stream{Type: "audio", Codec: "aac", Channels: 2, BitrateKbps: 192, Index: 1, Language: "eng"}
	ac3 := media.Stream{Type: "audio", Codec: "ac3", Channels: 6, BitrateKbps: 448, Index: 1, Language: "eng"}
	sub := media.Stream{Type: "subtitle", Codec: "subrip", Index: 2, Language: "eng"}
	mp4 := v("h264", 1920, 1080, 9000, 8, aac)
	mp4.Container, mp4.Ext = "mov,mp4,m4a,3gp,3g2,mj2", ".mp4"
	return []media.Info{
		v("h264", 1920, 1080, 9000, 8, aac, sub),
		v("mpeg2video", 720, 576, 6000, 8, ac3),
		v("hevc", 3840, 2160, 40000, 10, ac3, sub),
		v("h264", 1280, 720, 1200, 8, aac),
		mp4,
	}
}

// The migration keeps every flow's behaviour (proposal, Migration): its workflow, saved and read back,
// runs as the same template, and compiles to the same Plan for every file.
func TestMigrationKeepsBehaviour(t *testing.T) {
	rules.SetAvailableFormats(map[string]bool{"libx265": true, "libx264": true, "libsvtav1": true, "libvpx-vp9": true})
	defer rules.SetAvailableFormats(nil)
	out := t.TempDir() // a full path on every OS (/out is not one on Windows)
	var templates []rules.Template
	for q := 1; q <= 5; q++ {
		templates = append(templates, rules.SliderTemplate(q))
	}
	basic := rules.SliderTemplate(2)
	basic.Params.Audio = "keep_all"
	templates = append(templates, basic)
	for _, f := range []func(*rules.Selectors){
		func(s *rules.Selectors) {},
		func(s *rules.Selectors) { s.Format, s.MaxBitrate, s.Resolution = "h264", "medium", "720" },
		func(s *rules.Selectors) { s.Audio, s.OnlyMyLanguages, s.FileType = "eac3_surround", false, "mp4" },
		func(s *rules.Selectors) { s.Format, s.Audio, s.FileType = "vp9", "stereo_opus", "webm" },
		func(s *rules.Selectors) {
			s.AfterConverting, s.OutputDir, s.IncludeDolbyVision = "folder", out, true
		},
		func(s *rules.Selectors) {
			s.AfterConverting, s.Ignore, s.Subtitles = "beside", []string{"Extras"}, "keep_all"
		},
	} {
		s := rules.DefaultSelectors(3)
		f(&s)
		templates = append(templates, rules.Template{Kind: rules.KindSelectors, Selectors: &s, CompilerVersion: rules.CompilerVersion})
	}
	templates = append(templates, rules.LegacyTemplate(rules.Default(), "replace"), rules.LegacyTemplate(rules.Default(), "beside"))
	for i, tm := range templates {
		d := roundTrip(t, FromTemplate(tm, nil))
		r := Validate(d)
		if r.Errors() || !r.Stackable {
			t.Fatalf("%d (%s): issues %+v stackable %v", i, tm.Kind, r.Issues, r.Stackable)
		}
		got, issues := ToTemplate(d, tm)
		if len(issues) > 0 {
			t.Fatalf("%d: %+v", i, issues)
		}
		if !reflect.DeepEqual(got, tm) {
			t.Errorf("%d (%s): runs as another template:\n got %+v\nwant %+v", i, tm.Kind, got, tm)
		}
		fresh, _ := ToTemplate(d, rules.Template{}) // as if the flow's template were unknown: the same plans
		for j, info := range files() {
			a, b := tm.Compile(info), fresh.Compile(info)
			a.Kind, b.Kind = "", "" // which editor made it (slider/selectors): a label, not how the file is converted
			if !reflect.DeepEqual(a, b) {
				t.Errorf("%d (%s) file %d: plans differ:\n got %+v\nwant %+v", i, tm.Kind, j, b, a)
			}
		}
	}
}

// A workflow read again keeps its node and edge ids (FromTemplate reuses them by type).
func TestIDsStayAcrossRevisions(t *testing.T) {
	first := FromTemplate(rules.SliderTemplate(3), nil)
	again := FromTemplate(rules.SliderTemplate(4), &first)
	if !reflect.DeepEqual(ids(first), ids(again)) {
		t.Errorf("ids changed: %v %v", ids(first), ids(again))
	}
	if LogicHash(first) == LogicHash(again) {
		t.Error("different settings, same logic hash")
	}
}

func ids(d Doc) []string {
	var out []string
	for _, n := range d.Nodes {
		out = append(out, n.ID)
	}
	for _, e := range d.Edges {
		out = append(out, e.ID)
	}
	return out
}

// The preset's default Save the result (next to each library) is saved, but can't be published
// before V5; the other choices can.
func TestPresetDefaults(t *testing.T) {
	d := roundTrip(t, Preset())
	r := Validate(d)
	if !r.Stackable || len(r.Issues) != 1 || r.Issues[0].Code != "not_runnable_yet" || r.Issues[0].ConfigPath != "/folderMode" {
		t.Fatalf("%+v", r)
	}
	if r.StepSummaries[d.Nodes[2].ID] != "HEVC · Balanced quality · same resolution" || !strings.Contains(r.StepSummaries[d.Nodes[3].ID], "in your languages") {
		t.Errorf("summaries %v", r.StepSummaries)
	}
	shrunk := t.TempDir() // a full path on every OS
	d.Nodes[7].Config = map[string]any{"policy": "separate_folder", "folderMode": "one_folder", "folder": shrunk}
	if r := Validate(d); r.Errors() {
		t.Errorf("one folder: %+v", r.Issues)
	}
	if tm, _ := ToTemplate(d, rules.Template{}); tm.Selectors == nil || tm.Selectors.AfterConverting != "folder" || tm.Selectors.OutputDir != shrunk {
		t.Errorf("template %+v", tm.Selectors)
	}
}

// Spec 5.6: the graph's mistakes are found and named, on the node or edge they concern.
func TestValidateGraph(t *testing.T) {
	base := func() Doc { return roundTrip(t, FromTemplate(rules.SliderTemplate(3), nil)) }
	codes := func(d Doc) []string {
		var c []string
		for _, i := range Validate(d).Issues {
			c = append(c, i.Code)
		}
		slices.Sort(c)
		return slices.Compact(c)
	}
	for name, c := range map[string]struct {
		edit func(*Doc)
		want string
	}{
		"no trigger":    {func(d *Doc) { d.Nodes = d.Nodes[1:]; d.Edges = d.Edges[1:] }, "no_trigger"},
		"two triggers":  {func(d *Doc) { n := d.Nodes[0]; n.ID = NewID(); d.Nodes = append(d.Nodes, n) }, "multiple_triggers"},
		"unknown type":  {func(d *Doc) { d.Nodes[4].Type = "video.magic" }, "unknown_node_type"},
		"dangling edge": {func(d *Doc) { d.Edges[2].To = "nowhere" }, "dangling_edge"},
		"unknown port":  {func(d *Doc) { d.Edges[2].FromPort = "maybe" }, "unknown_port"},
		"cycle": {func(d *Doc) {
			d.Edges = append(d.Edges, Edge{ID: NewID(), From: d.Nodes[5].ID, FromPort: "success", To: d.Nodes[2].ID, ToPort: "in"})
		}, "cycle_without_loop"},
		"bad choice":           {func(d *Doc) { d.Nodes[2].Config["format"] = "mpeg1" }, "invalid_config"},
		"always on turned off": {func(d *Doc) { d.Nodes[6].Enabled = false }, "invalid_config"},
		"a branch can't run yet": {func(d *Doc) {
			d.Edges = append(d.Edges, Edge{ID: NewID(), From: d.Nodes[2].ID, FromPort: "skipped", To: d.Nodes[7].ID, ToPort: "in"})
		}, "not_runnable_yet"},
	} {
		d := base()
		c.edit(&d)
		if got := codes(d); !slices.Contains(got, c.want) {
			t.Errorf("%s: %v, want %s", name, got, c.want)
		}
	}
	d := base()
	d.Nodes[3].Config["volume"] = 11
	r := Validate(d)
	if r.Errors() || len(r.Issues) != 1 || r.Issues[0].Code != "unknown_config_key" || r.Issues[0].Severity != "warning" {
		t.Errorf("unknown key: %+v", r.Issues)
	}
	d = base()
	d.Edges = append(d.Edges, Edge{ID: NewID(), From: d.Nodes[2].ID, FromPort: "skipped", To: d.Nodes[7].ID, ToPort: "in"})
	if Validate(d).Stackable {
		t.Error("a branch is not stackable")
	}
}

// Turning off audio, subtitles or file type passes the original's through.
func TestTurnedOffSteps(t *testing.T) {
	d := roundTrip(t, FromTemplate(rules.SliderTemplate(3), nil))
	for _, i := range []int{3, 4, 5} {
		d.Nodes[i].Enabled = false
	}
	tm, issues := ToTemplate(d, rules.Template{})
	if len(issues) > 0 || tm.Selectors.Audio != "as_is" || tm.Selectors.OnlyMyLanguages || tm.Selectors.Subtitles != "keep_all" || tm.Selectors.FileType != "keep" {
		t.Errorf("%+v %+v", tm.Selectors, issues)
	}
}

// The registry: every node type has a definition with a config schema and ports.
func TestRegistry(t *testing.T) {
	for _, typ := range append(slices.Clone(PresetOrder), TypeLegacy) {
		d, ok := Definition(typ)
		if !ok || d.ConfigSchema["type"] != "object" || len(d.Outputs) == 0 || d.TypeVersion != 1 {
			t.Errorf("%s: %+v", typ, d)
		}
	}
	if _, err := json.Marshal(Definitions()); err != nil {
		t.Fatal(err)
	}
}

// The Convert step's summary names each slider position once (ui-shop: "Better quality quality").
func TestQualityWords(t *testing.T) {
	want := []string{"Smallest file", "Smaller", "Balanced quality", "Better quality", "Best quality"}
	for i, p := range rules.Slider {
		if got := qualityWords(p.Name); got != want[i] {
			t.Errorf("%d %q: %q, want %q", p.Position, p.Name, got, want[i])
		}
		n := Node{Type: TypeConvert, Enabled: true, Config: map[string]any{"quality": p.Position}}
		if s := summary(n); strings.Count(strings.ToLower(s), "quality") > 1 || !strings.Contains(s, want[i]) {
			t.Errorf("summary %q", s)
		}
	}
}
