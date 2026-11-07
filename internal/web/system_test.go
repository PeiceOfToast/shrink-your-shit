// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
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
