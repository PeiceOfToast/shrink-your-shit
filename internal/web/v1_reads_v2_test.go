// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// ui-shop 11:59Z: the old pages (v1) read a workflow saved through v2 with "next to each library", "one
// folder" or branches without breaking; the old editor's save, which can't say "next to each library",
// is refused with words saying so instead of changing it.
func TestV1ReadsV2Folders(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	sel := func(mode, dir string) rules.Template {
		s := rules.DefaultSelectors(3)
		s.AfterConverting, s.FolderMode, s.OutputDir = "folder", mode, dir
		return s.Template()
	}
	next, _ := st.AddFlow("Next to each library", sel(rules.NextToLibrary, ""))
	one, _ := st.AddFlow("One folder", sel("", t.TempDir()))
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	if _, err := st.SetLibraryFlow(lib.ID, next.ID); err != nil {
		t.Fatal(err)
	}
	libPath := "/api/libraries/" + strconv.FormatInt(lib.ID, 10)
	for _, p := range []string{"/api/libraries", "/api/flows", libPath, libPath + "/setting",
		"/api/flows/" + strconv.FormatInt(next.ID, 10) + "/setting", "/api/flows/" + strconv.FormatInt(one.ID, 10) + "/setting"} {
		if code, body, _ := do(t, nil, srv, call{method: "GET", path: p}); code != 200 {
			t.Errorf("GET %s: %d %s", p, code, body)
		}
	}
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: libPath + "/setting"}); !strings.Contains(body, `"folderMode":"next_to_library"`) {
		t.Errorf("the setting hides its folder mode: %s", body)
	}
	// the old Standard editor's save: every choice, the folder for new files empty
	old := `{"quality":3,"speed":"slower","format":"hevc","resolution":"keep","maxBitrate":"none","audio":"stereo_aac","onlyMyLanguages":false,"subtitles":"keep_all","fileType":"mkv","afterConverting":"folder","outputDir":"","ignore":[],"includeDolbyVision":false}`
	for _, p := range []string{libPath + "/setting", "/api/flows/" + strconv.FormatInt(next.ID, 10) + "/setting"} {
		code, body, _ := do(t, nil, srv, call{method: "PUT", path: p, body: old, ctype: "application/json"})
		if code != 409 || errCode(body) != "edit_in_new_ui" || !strings.Contains(body, "next to each library") {
			t.Errorf("PUT %s: %d %s", p, code, body)
		}
	}
	if f, _ := st.GetFlow(next.ID); f.Template.Selectors == nil || f.Template.Selectors.FolderMode != rules.NextToLibrary || f.Template.Selectors.Speed != "normal" {
		t.Errorf("changed: %+v", f.Template.Selectors)
	}
	// choosing another place on purpose is a change the old page can make
	replace := strings.Replace(old, `"afterConverting":"folder","outputDir":""`, `"afterConverting":"replace"`, 1)
	if code, body, _ := do(t, nil, srv, call{method: "PUT", path: "/api/flows/" + strconv.FormatInt(next.ID, 10) + "/setting", body: replace, ctype: "application/json"}); code != 200 {
		t.Errorf("replace on purpose: %d %s", code, body)
	}
}
