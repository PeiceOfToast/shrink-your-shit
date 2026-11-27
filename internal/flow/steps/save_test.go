// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

func saveSettings(container, where, suffix string) SaveSettings {
	return SaveSettings{Container: container, Where: where, NameSuffix: suffix}
}

// videoWorking is a plan the video step has already filled, so Save has something to write.
func videoWorking() encode.Plan {
	crf := 23
	return encode.Plan{Video: encode.VideoPlan{Codec: encode.CodecHEVC, CRF: &crf}}
}

func mp4Info() media.Info {
	return media.Info{
		Path:   "/library/Movies/media.mp4",
		Format: media.Format{Names: []string{"mov", "mp4", "m4a", "3gp", "3g2", "mj2"}},
		Video:  []media.VideoTrack{{Index: 0, Codec: "h264", Width: 1920, Height: 1080}},
	}
}

func TestSaveKeepsTheFileType(t *testing.T) {
	p := videoWorking()
	d, err := SaveStep{}.Plan(saveSettings("keep", "next_to", "shrunk"), flow.PlanInput{Info: mp4Info()}, &p)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !d.Work || d.Reason != "save.next_to" {
		t.Errorf("decision = %+v, want a save", d)
	}
	if p.Container != encode.ContainerMP4 {
		t.Errorf("container = %q, want mp4", p.Container)
	}
	if filepath.ToSlash(p.Output) != "/library/Movies/media-shrunk.mp4" {
		t.Errorf("output = %q, want media-shrunk.mp4", p.Output)
	}
	if d.Label != "Saving next to the original as MP4" {
		t.Errorf("label = %q", d.Label)
	}
}

func TestSaveContainerChoices(t *testing.T) {
	p := videoWorking()
	if _, err := (SaveStep{}).Plan(saveSettings("mkv", "next_to", "x"), flow.PlanInput{Info: mp4Info()}, &p); err != nil {
		t.Fatalf("mkv: %v", err)
	}
	if filepath.ToSlash(p.Output) != "/library/Movies/media-x.mkv" {
		t.Errorf("mkv output = %q", p.Output)
	}
	webm := media.Info{Path: "/library/a.webm", Format: media.Format{Names: []string{"matroska", "webm"}}}
	p = videoWorking()
	if _, err := (SaveStep{}).Plan(saveSettings("keep", "next_to", "x"), flow.PlanInput{Info: webm}, &p); err != nil {
		t.Fatalf("webm: %v", err)
	}
	if p.Container != encode.ContainerWebM || filepath.ToSlash(p.Output) != "/library/a-x.webm" {
		t.Errorf("webm = %q %q, want webm/a-x.webm", p.Container, p.Output)
	}
}

func TestSaveNothingWhenTheVideoIsUntouched(t *testing.T) {
	var p encode.Plan
	d, err := SaveStep{}.Plan(saveSettings("mkv", "next_to", "shrunk"), flow.PlanInput{Info: mp4Info()}, &p)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if d.Work || d.Reason != "save.nothing" {
		t.Errorf("decision = %+v, want nothing", d)
	}
	if filepath.ToSlash(p.Output) != "/library/Movies/media-shrunk.mkv" {
		t.Errorf("output = %q, want it named even when nothing is written", p.Output)
	}
}

// TestSaveRefusesToLandOnTheSource is the brief's suffix case: an empty suffix with the source's
// own file type composes exactly the source path.
func TestSaveRefusesToLandOnTheSource(t *testing.T) {
	info := media.Info{Path: "/library/Movies/media.mkv", Format: media.Format{Names: []string{"matroska", "webm"}}}
	for _, suffix := range []string{"", "///"} {
		p := videoWorking()
		_, err := SaveStep{}.Plan(saveSettings("keep", "next_to", suffix), flow.PlanInput{Info: info}, &p)
		var pe *flow.PlanError
		if !errors.As(err, &pe) || pe.Code != "save.would_land_on_source" {
			t.Errorf("suffix %q: error = %v, want save.would_land_on_source", suffix, err)
		}
	}
	// A different file type landed on the same stem is fine.
	p := videoWorking()
	if _, err := (SaveStep{}).Plan(saveSettings("mp4", "next_to", ""), flow.PlanInput{Info: info}, &p); err != nil {
		t.Fatalf("mkv source to mp4: %v", err)
	}
	if filepath.ToSlash(p.Output) != "/library/Movies/media.mp4" {
		t.Errorf("output = %q", p.Output)
	}
}

func TestSaveUnknownContainer(t *testing.T) {
	info := media.Info{Path: "/library/a.avi", Format: media.Format{Names: []string{"avi"}}}
	p := videoWorking()
	_, err := SaveStep{}.Plan(saveSettings("keep", "next_to", "x"), flow.PlanInput{Info: info}, &p)
	var pe *flow.PlanError
	if !errors.As(err, &pe) || pe.Code != "save.unknown_container" {
		t.Errorf("error = %v, want save.unknown_container", err)
	}
}

func TestSaveCleansTheSuffix(t *testing.T) {
	cases := map[string]string{
		"shrunk":    "shrunk",
		"a b<>":     "a b",
		"  ..x..  ": "x",
		"a/b\\c:d":  "abcd",
		"ok~name!":  "ok~name!", // legal on the platforms this runs on
		"\x01bad":   "bad",
		"trailing.": "trailing",
	}
	for raw, want := range cases {
		if got := cleanSuffix(raw); got != want {
			t.Errorf("cleanSuffix(%q) = %q, want %q", raw, got, want)
		}
	}
	p := videoWorking()
	if _, err := (SaveStep{}).Plan(saveSettings("mkv", "next_to", "a b<>"), flow.PlanInput{Info: mp4Info()}, &p); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if filepath.ToSlash(p.Output) != "/library/Movies/media-a b.mkv" {
		t.Errorf("output = %q, want the cleaned suffix", p.Output)
	}
}

func TestSaveValidate(t *testing.T) {
	check := SaveStep{}.Validate(saveSettings("mkv", "next_to", "shrunk"), flow.ValidateContext{})
	if len(check.Problems) != 0 {
		t.Errorf("problems = %+v, want none", check.Problems)
	}
	if check.Summary != "MKV · next to the original" {
		t.Errorf("summary = %q", check.Summary)
	}

	for where, why := range map[string]string{"replace": flow.ReplaceWhy, "folder": flow.FolderWhy} {
		check = SaveStep{}.Validate(saveSettings("mkv", where, "shrunk"), flow.ValidateContext{})
		if len(check.Problems) == 0 || check.Problems[0].Field != "where" ||
			check.Problems[0].Code != "save.not_yet" || check.Problems[0].Message != why {
			t.Errorf("%s problems = %+v, want the not-yet problem first with %q", where, check.Problems, why)
		}
	}
	// A folder it is told to use but that is empty is its own problem, and a folder it has fills it.
	set := saveSettings("mkv", "folder", "shrunk")
	set.Folder = "/library/Shrunk"
	if check = (SaveStep{}).Validate(set, flow.ValidateContext{}); len(check.Problems) != 1 || check.Problems[0].Field != "where" {
		t.Errorf("filled folder problems = %+v, want only the not-yet problem", check.Problems)
	}
	// WebM itself is not a problem here: what a file type can hold is the flow's cross-step rule
	// (flow/containers.go), which knows Video's format and Audio's codec.
	check = SaveStep{}.Validate(saveSettings("webm", "next_to", "shrunk"), flow.ValidateContext{})
	if len(check.Problems) != 0 {
		t.Errorf("webm problems = %+v, want the container rule to own it", check.Problems)
	}

	check = SaveStep{}.Validate(saveSettings("keep", "next_to", "///"), flow.ValidateContext{})
	if len(check.Problems) != 1 || check.Problems[0].Field != "nameSuffix" ||
		check.Problems[0].Code != "save.empty_suffix" {
		t.Errorf("problems = %+v, want the empty-suffix problem", check.Problems)
	}
	// An explicit container may change the extension, so an empty suffix is not a problem by itself;
	// the plan refuses the one source type whose name it would really take (an MKV source, here).
	check = SaveStep{}.Validate(saveSettings("mkv", "next_to", "///"), flow.ValidateContext{})
	if len(check.Problems) != 0 {
		t.Errorf("problems = %+v, want none for an explicit container", check.Problems)
	}

	// An unknown file type is the tag's business.
	check = SaveStep{}.Validate(saveSettings("avi", "next_to", "x"), flow.ValidateContext{})
	if len(check.Problems) != 1 || check.Problems[0].Code != "setting.choice" {
		t.Errorf("problems = %+v, want the choice problem", check.Problems)
	}
}

// TestSaveChoicesAreGreyed is item 10's placeholder: the page draws "Replace the original" and "A
// different folder" as choices it cannot pick, and the check greys the same choices with the same
// sentences. A stored value still blocks publishing with its own sentence, so a saved flow cannot
// quietly fall back to next-to (lead, 11-27 20:19Z).
func TestSaveChoicesAreGreyed(t *testing.T) {
	form, err := flow.BuildForm(SaveStep{}.Def(), flow.Env{})
	if err != nil {
		t.Fatalf("form: %v", err)
	}
	formWhy := map[string]string{}
	for _, o := range form.Schema.Properties["where"].Layout.Choices {
		if o.Available {
			continue
		}
		formWhy[fmt.Sprint(o.Value)] = o.Why
	}
	for value, why := range map[string]string{"replace": replaceWhy, "folder": folderWhy} {
		if formWhy[value] != why {
			t.Errorf("form greys %s with %q, want %q", value, formWhy[value], why)
		}
	}

	check := SaveStep{}.Validate(saveSettings("mkv", "next_to", "shrunk"), flow.ValidateContext{})
	checkWhy := map[string]string{}
	for _, d := range check.Disabled {
		if d.Field == "where" {
			checkWhy[d.Value] = d.Why
		}
	}
	for value, why := range map[string]string{"replace": replaceWhy, "folder": folderWhy} {
		if checkWhy[value] != why {
			t.Errorf("check greys %s with %q, want the form's %q", value, checkWhy[value], why)
		}
	}

	// A stored value is still a problem, with its own sentence: the greyed choice is a promise,
	// not a silent rewrite of what was saved.
	for where, why := range map[string]string{"replace": replaceWhy, "folder": folderWhy} {
		check = SaveStep{}.Validate(saveSettings("mkv", where, "shrunk"), flow.ValidateContext{})
		blocked := false
		for _, p := range check.Problems {
			if p.Field == "where" && p.Code == "save.not_yet" && p.Message == why {
				blocked = true
			}
		}
		if !blocked {
			t.Errorf("%s problems = %+v, want save.not_yet with %q", where, check.Problems, why)
		}
	}
}

func TestSaveDefaults(t *testing.T) {
	s, err := flow.DecodeSettings(SaveStep{}.Def(), []byte(`{}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := s.(SaveSettings)
	if got.Container != "mkv" || got.Where != "next_to" || got.Folder != "" || got.NameSuffix != "" {
		t.Errorf("defaults = %+v", got)
	}
}
