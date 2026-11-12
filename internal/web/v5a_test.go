// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// V5a: a workflow whose Save step is "Next to each library" publishes; each library joining it needs a
// usable folder beside it (design's words when it has none), never inside any library, and no library
// may hold another's folder for new files.
func TestV5aNextToLibrary(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	sendIf := func(method, path, body, ifMatch string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if ifMatch != "" {
			req.Header.Set("If-Match", ifMatch)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	send := func(method, path, body string) (int, string) { t.Helper(); return sendIf(method, path, body, "") }
	_, body := send("POST", "/api/v2/workflows", `{"name":"Output first"}`)
	wf := decode[map[string]any](t, body)["id"].(string)
	_, body = send("GET", "/api/v2/workflows/"+wf+"/draft", "")
	etag := decode[v2Rev](t, body).Etag
	if code, body := send("POST", "/api/v2/workflows/"+wf+"/publish", `{"etag":"`+etag+`"}`); code != 200 {
		t.Fatalf("publish the preset (next to each library): %d %s", code, body)
	}
	join := func(name, path string) (int, string) {
		l, err := st.AddLibrary(name, path, rules.SliderTemplate(3), false)
		if err != nil {
			t.Fatal(err)
		}
		return send("PUT", "/api/libraries/"+strconv.FormatInt(l.ID, 10)+"/flow", `{"flowId":"`+wf+`"}`)
	}

	media := t.TempDir()
	movies := filepath.Join(media, "movies")
	os.Mkdir(movies, 0o755)
	if code, body := join("Movies", movies); code != 200 {
		t.Fatalf("a library with a usable folder beside it: %d %s", code, body)
	}

	// at a disk's root: no default, in design's words
	root := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		root = filepath.VolumeName(media) + `\`
	}
	code, body := join("Top", filepath.Join(root, "syp-v5a-test-library"))
	if want := "No folder for new files for Top yet: " + root + " is the container's own disk. Pick a folder in the library's Output tab, or choose One folder for all. Nothing was changed."; code != 400 || errCode(body) != "no_default_folder" || !strings.Contains(body, strconv.Quote(want)[1:len(strconv.Quote(want))-1]) {
		t.Errorf("container root: %d %s", code, body)
	}

	// a library at Movies' folder for new files (it overlaps no library): refused, on any flow
	shrunk := movies + " (shrunk)"
	os.Mkdir(shrunk, 0o755)
	if code, body := send("POST", "/api/libraries", `{"name":"Shrunk","path":`+strconv.Quote(shrunk)+`}`); code != 400 || errCode(body) != "inside_library" || !strings.Contains(body, "Movies's new files go") {
		t.Errorf("a library holding another's folder for new files: %d %s", code, body)
	}

	// a library on another flow whose folder would sit inside Movies: refused
	tv := filepath.Join(movies, "tv")
	os.Mkdir(tv, 0o755)
	if code, body := join("TV", tv); code != 400 || errCode(body) != "inside_library" {
		t.Errorf("a folder for new files inside another library: %d %s", code, body)
	}

	// a draft with an error can't be published
	_, body = send("GET", "/api/v2/workflows/"+wf+"/draft", "")
	d := decode[v2Rev](t, body)
	for i := range d.Workflow.Nodes {
		if d.Workflow.Nodes[i].Config["policy"] != nil {
			d.Workflow.Nodes[i].Config = map[string]any{"policy": "separate_folder", "folderMode": "one_folder", "folder": ""}
		}
	}
	put, _ := json.Marshal(map[string]any{"workflow": d.Workflow, "layout": d.Layout})
	_, body = sendIf("PUT", "/api/v2/workflows/"+wf+"/draft", string(put), d.Etag)
	if code, body := send("POST", "/api/v2/workflows/"+wf+"/publish", `{"etag":"`+decode[v2Rev](t, body).Etag+`"}`); code != 400 || errCode(body) != "has_errors" {
		t.Errorf("publish with errors: %d %s", code, body)
	}
}
