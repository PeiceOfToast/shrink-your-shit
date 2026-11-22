// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A page loads a library's setting, changes nothing (or one field) and saves the whole answer back.
func TestLibrarySettingSavesItsGetBodyBack(t *testing.T) {
	for _, c := range []struct {
		name string
		t    rules.Template
	}{
		{"slider", rules.SliderTemplate(3)},
		{"basic", rules.BasicTemplate(2, "stereo_aac")},
		{"legacy", rules.LegacyTemplate(rules.Rule{FromCodec: "h264", CRF: 23, Preset: "medium"}, "replace")},
	} {
		srv, st, _, _ := libServer(t, false)
		l, _ := st.AddLibrary("Movies", t.TempDir(), c.t, true)
		path := fmt.Sprintf("/api/libraries/%d/setting", l.ID)
		_, before, _ := do(t, nil, srv, call{method: "GET", path: path})

		code, out := post(t, srv, "PUT", path, before)
		if code != 200 {
			t.Fatalf("%s: PUT of the GET body: %d %s", c.name, code, out)
		}
		if _, after, _ := do(t, nil, srv, call{method: "GET", path: path}); after != before {
			t.Errorf("%s: GET after the save differs from the GET before it", c.name)
		}
		if f, _ := st.GetFlow(l.FlowID); f.Template != c.t {
			t.Errorf("%s: a save of nothing changed the flow: %+v", c.name, f.Template)
		}
	}
}

// Read-only fields are not written, and a body with one choice changed changes only that choice.
func TestLibrarySettingReadOnlyFieldsAreNotWritten(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	path := fmt.Sprintf("/api/libraries/%d/setting", l.ID)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: path})
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	m["editableHere"], m["reason"], m["quality"] = false, map[string]string{"code": "x"}, 5
	b, _ := json.Marshal(m)
	if code, out := post(t, srv, "PUT", path, string(b)); code != 200 {
		t.Fatalf("PUT with one choice changed: %d %s", code, out)
	}
	if f, _ := st.GetFlow(l.FlowID); f.Template != rules.SliderTemplate(5) {
		t.Errorf("only quality should change: %+v", f.Template)
	}
}
