// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

func doctorServer(t *testing.T, exposed bool) (*httptest.Server, *jobs.Store, *Access) {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetPaused(false) // the test's worker is started (S2: a new install starts stopped)
	t.Cleanup(func() { st.Close() })
	a, _, _ := OpenAccess(t.TempDir(), exposed)
	a.FailDelay = 0
	fake := &media.FakeExec{Handler: func(name string, args []string) (string, string, error) {
		switch a := strings.Join(args, " "); {
		case strings.Contains(a, "-version"):
			return "ffmpeg version 7.1.5", "", nil
		case strings.Contains(a, "-encoders"):
			return " V....D libx265              HEVC\n", "", nil
		case strings.Contains(a, "-filters"):
			return " ... ssim              VV->V      SSIM\n", "", nil
		}
		return "", "", fmt.Errorf("no")
	}}
	chk := &doctor.Checker{Exec: fake, FFmpeg: "/usr/bin/ffmpeg", FFprobe: "/usr/bin/ffprobe", FreeSpace: func(string) (uint64, error) { return 1 << 40, nil }}
	logs := &doctor.LogTail{}
	logs.Write([]byte("started\n"))
	srv := httptest.NewServer(Handler(st, a, Engine{Doctor: chk, DataDir: t.TempDir(), Version: "1.2.3", Logs: logs}))
	t.Cleanup(srv.Close)
	return srv, st, a
}

func TestLibraryCheck(t *testing.T) {
	srv, _, _ := doctorServer(t, false)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.mkv"), make([]byte, 2048), 0o644)
	code, body := post(t, srv, "POST", "/api/libraries/check", fmt.Sprintf(`{"path":%q}`, dir))
	var r doctor.FolderReport
	json.Unmarshal([]byte(body), &r)
	if code != 200 || !r.OK || r.VideoCount != 1 || r.TotalBytes != 2048 || r.Blockers == nil || r.Notes == nil {
		t.Fatalf("%d %s", code, body)
	}
	code, body = post(t, srv, "POST", "/api/libraries/check", fmt.Sprintf(`{"path":%q}`, t.TempDir()))
	if json.Unmarshal([]byte(body), &r); code != 200 || r.OK || r.Blockers[0].Code != "no_videos" {
		t.Errorf("empty folder: %d %s", code, body)
	}
	if code, body = post(t, srv, "POST", "/api/libraries/check", `{"path":"relative"}`); code != 400 || errCode(body) != "invalid_path" {
		t.Errorf("relative: %d %s", code, body)
	}
}

// A folder for new files inside the library is refused by the check too, with the add's own code and
// words, so first run says it before it publishes the workflow (ui-shop 23:37Z).
func TestLibraryCheckRefusesAnOutputFolderInside(t *testing.T) {
	srv, st, _ := doctorServer(t, false)
	lib := t.TempDir()
	os.WriteFile(filepath.Join(lib, "a.mkv"), make([]byte, 2048), 0o644)
	out := filepath.Join(lib, "converted")
	check := func(folder string) doctor.FolderReport {
		code, body := post(t, srv, "POST", "/api/libraries/check", fmt.Sprintf(`{"path":%q,"outputFolder":%q}`, lib, folder))
		var r doctor.FolderReport
		if json.Unmarshal([]byte(body), &r); code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		return r
	}
	r := check(out)
	if r.OK || len(r.Blockers) != 1 || r.Blockers[0].Code != "inside_library" {
		t.Fatalf("inside: %+v", r)
	}
	sel := rules.DefaultSelectors(3)
	sel.Speed, sel.AfterConverting, sel.OutputDir = "slower", "folder", out
	flow, _ := st.AddFlow("Inside", sel.Template())
	_, body := post(t, srv, "POST", "/api/libraries", `{"name":"Movies","path":`+strconv.Quote(lib)+`,"flowId":"`+itoa(flow.ID)+`"}`)
	if errCode(body) != "inside_library" || !strings.Contains(body, strconv.Quote(r.Blockers[0].Message)) {
		t.Errorf("the add's refusal %s, the check's %q", body, r.Blockers[0].Message)
	}
	if r := check(t.TempDir()); !r.OK || len(r.Blockers) != 0 { // elsewhere: fine
		t.Errorf("outside: %+v", r)
	}
	if r := check(""); !r.OK { // none given: the folder check alone
		t.Errorf("none: %+v", r)
	}
}

func TestSystemHealth(t *testing.T) {
	srv, st, _ := doctorServer(t, false)
	st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/system/health"})
	var h doctor.Health
	json.Unmarshal([]byte(body), &h)
	if code != 200 || h.Version != "1.2.3" || len(h.LogsTail) != 1 || h.LogsTail[0] != "started" || len(h.Checks) < 7 {
		t.Fatalf("%d %s", code, body)
	}
	for _, c := range h.Checks {
		if c.ID == "" || c.Label == "" || (c.Status != "ok" && c.Status != "warn" && c.Status != "bad") {
			t.Errorf("check %+v", c)
		}
	}
}

func TestSetupChecksNeedSignIn(t *testing.T) {
	srv, _, a := doctorServer(t, true)
	for _, k := range []call{
		{method: "POST", path: "/api/libraries/check", body: `{"path":"/"}`, ctype: "application/json"},
		{method: "GET", path: "/api/system/health"},
	} {
		if code, body, _ := do(t, nil, srv, k); code != 401 || errCode(body) != "auth" {
			t.Errorf("%s: %d %s", k.path, code, body)
		}
	}
	if code, _, _ := do(t, nil, srv, call{method: "GET", path: "/api/system/health", bearer: a.Token()}); code != 200 {
		t.Errorf("with the token: %d", code)
	}
}

// IA.md First run: before the library exists, the check says whether a folder "next to it" can be used,
// and if not, why as a code and which folder, besides the sentence; so do the refusals and the library.
func TestNoDefaultFolderFields(t *testing.T) {
	srv, st, _ := doctorServer(t, false)
	root := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		root = filepath.VolumeName(t.TempDir()) + `\`
	}
	lib := filepath.Join(root, "syp-ndf-test")
	_, body := post(t, srv, "POST", "/api/libraries/check", fmt.Sprintf(`{"path":%q,"nextToLibrary":true,"name":"Top"}`, lib))
	var r doctor.FolderReport
	json.Unmarshal([]byte(body), &r)
	var found *doctor.Issue
	for i := range r.Blockers {
		if r.Blockers[i].Code == "no_default_folder" {
			found = &r.Blockers[i]
		}
	}
	if found == nil || found.Folder != root || found.Reason != "container_root" || !strings.HasPrefix(found.Message, "No folder for new files for Top yet: ") {
		t.Errorf("check: %s", body)
	}
	ok := filepath.Join(t.TempDir(), "movies") // a usable one: no such blocker
	os.Mkdir(ok, 0o755)
	_, body = post(t, srv, "POST", "/api/libraries/check", fmt.Sprintf(`{"path":%q,"nextToLibrary":true}`, ok))
	if strings.Contains(body, "no_default_folder") {
		t.Errorf("usable: %s", body)
	}
	// the refusal when such a library joins a "Next to each library" workflow
	sel := rules.DefaultSelectors(3)
	sel.AfterConverting, sel.FolderMode = "folder", rules.NextToLibrary
	flow, _ := st.AddFlow("Next to", sel.Template())
	l, _ := st.AddLibrary("Top", lib, rules.SliderTemplate(3), false)
	code, body := post(t, srv, "PUT", "/api/libraries/"+strconv.FormatInt(l.ID, 10)+"/flow", `{"flowId":"`+strconv.FormatInt(flow.ID, 10)+`"}`)
	if code != 400 || !strings.Contains(body, `"reason":"container_root"`) || !strings.Contains(body, `"folder":`+strconv.Quote(root)) {
		t.Errorf("refusal %d %s", code, body)
	}
}
