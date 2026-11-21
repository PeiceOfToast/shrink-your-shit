// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Wording (test matrix item 1): a folder that holds the library and a folder inside it each get their own words.
func TestOutputFolderWordsSayTheRightRelation(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), false)
	path := "/api/flows/" + itoa(lib.FlowID)
	messages := func(dir string) string {
		t.Helper()
		_, body := post(t, srv, "POST", path+"/check-output", `{"path":`+strconv.Quote(dir)+`}`)
		var v struct{ Blockers []map[string]string }
		json.Unmarshal([]byte(body), &v)
		for _, b := range v.Blockers {
			if b["code"] == "inside_library" {
				return b["message"]
			}
		}
		return ""
	}
	holds := messages(filepath.Dir(lib.Path))
	want := "This folder contains the library. Choose a folder outside it, or the new files would be scanned and converted again."
	if holds != want {
		t.Errorf("folder holds the library: %q, want %q", holds, want)
	}
	inside := messages(filepath.Join(lib.Path, "converted"))
	if want := "This folder is inside the library, so new files would be converted again. Pick a folder outside " + lib.Path + "."; inside != want {
		t.Errorf("folder inside the library: %q, want %q", inside, want)
	}
}
