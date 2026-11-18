// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The slider and More options save "kind" on Convert video. It is a known setting marked readOnly, so no
// form shows it and validation does not call it unknown.
func TestConvertKindIsAKnownSetting(t *testing.T) {
	convertOf := func(d Doc) *Node {
		for i := range d.Nodes {
			if d.Nodes[i].Type == TypeConvert {
				return &d.Nodes[i]
			}
		}
		t.Fatal("no Convert video step")
		return nil
	}
	for _, kind := range []string{"slider", "selectors"} {
		d := FromTemplate(rules.SliderTemplate(2), nil)
		convertOf(d).Config["kind"] = kind
		if r := Validate(d); len(r.Issues) != 0 {
			t.Errorf("kind %q: %+v", kind, r.Issues)
		}
	}
	d := FromTemplate(rules.SliderTemplate(2), nil)
	convertOf(d).Config["kind"] = "bogus"
	var got []Issue
	for _, i := range Validate(d).Issues {
		if i.Code == "invalid_config" && i.Field == "kind" {
			got = append(got, i)
		}
	}
	if len(got) != 1 || got[0].Severity != "error" {
		t.Errorf("kind bogus: %+v", got)
	}
	if def, _ := Definition(TypeConvert); def.ConfigSchema["properties"].(map[string]any)["kind"].(map[string]any)["readOnly"] != true {
		t.Error("kind must be readOnly so forms do not show it")
	}
}
