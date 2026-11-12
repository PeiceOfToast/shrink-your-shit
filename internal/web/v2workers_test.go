// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// workersServer has a doctor whose ffmpeg lists x265, x264, NVENC and VA-API; a test frame works with
// x265 and NVENC (10-bit too), fails with x264 and VA-API.
func workersServer(t *testing.T) (*httptest.Server, *jobs.Store) {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a, _, _ := OpenAccess(t.TempDir(), false)
	fake := &media.FakeExec{Handler: func(name string, args []string) (string, string, error) {
		switch a := strings.Join(args, " "); {
		case strings.Contains(a, "-version"):
			return "ffmpeg version 7.1.5", "", nil
		case strings.Contains(a, "-encoders"):
			return " V....D libx265  HEVC\n V....D libx264  H.264\n V....D hevc_nvenc  NVENC\n V....D hevc_vaapi  VAAPI\n", "", nil
		case strings.Contains(a, "-filters"):
			return " ... ssim              VV->V      SSIM\n", "", nil
		case strings.Contains(a, "-c:v libx265"), strings.Contains(a, "-c:v hevc_nvenc"):
			return "", "", nil
		}
		return "", "", errors.New("exit status 1")
	}}
	chk := &doctor.Checker{Exec: fake, FFmpeg: "/usr/bin/ffmpeg", FFprobe: "/usr/bin/ffprobe", FreeSpace: func(string) (uint64, error) { return 1 << 40, nil }}
	srv := httptest.NewServer(Handler(st, a, Engine{Doctor: chk, DataDir: t.TempDir(), Version: "1.2.3", Logs: &doctor.LogTail{}}))
	t.Cleanup(srv.Close)
	return srv, st
}

type v2Worker struct {
	ID, Name, Kind, Status, AppVersion string
	Slots                              struct{ CPU, GPU struct{ Used, Total int } }
	Capabilities                       []struct{ Key, Slot, Test, Detail string }
	CurrentJobIDs                      []string `json:"currentJobIds"`
	Tags                               []string
	Pool                               *string
	Schedule                           *struct{ From, To string }
	Gpus                               []struct{ Vendor string }
}

// V4a: the local worker in the contract's shape, its capabilities from real test frames.
func TestV2Workers(t *testing.T) {
	srv, st := workersServer(t)
	code, body, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/local/capabilities/test", body: "{}", ctype: "application/json"})
	wk := decode[v2Worker](t, body)
	if code != 200 || wk.ID != "local" || wk.Kind != "local" || wk.AppVersion != "1.2.3" || wk.Status != "paused" { // a new install starts stopped
		t.Fatalf("%d %s", code, body)
	}
	tests := map[string]string{}
	for _, c := range wk.Capabilities {
		tests[c.Key] = c.Slot + "/" + c.Test
		if c.Key == "encoder:hevc_nvenc" && c.Detail != "10-bit too (HDR)." {
			t.Errorf("nvenc detail %q", c.Detail)
		}
	}
	want := map[string]string{"encoder:libx265": "cpu/passed", "encoder:libx264": "cpu/failed", "encoder:hevc_nvenc": "gpu/passed",
		"encoder:hevc_vaapi": "gpu/failed", "tool:ffprobe": "cpu/passed", "filter:ssim": "cpu/passed"}
	if len(tests) != len(want) {
		t.Errorf("capabilities %v", tests)
	}
	for k, v := range want {
		if tests[k] != v {
			t.Errorf("%s: %q, want %q", k, tests[k], v)
		}
	}
	if wk.Slots.CPU.Total != 1 || wk.Slots.GPU.Total != 1 || len(wk.Gpus) != 1 || wk.Gpus[0].Vendor != "nvidia" { // a working NVENC: 1 graphics slot
		t.Errorf("slots %+v gpus %+v", wk.Slots, wk.Gpus)
	}

	// PATCH: name, tags, pool and graphics slots are the worker's; processor slots and the schedule are Settings'
	code, body, _ = do(t, nil, srv, call{method: "PATCH", path: "/api/v2/workers/local", ctype: "application/json",
		body: `{"name":" Garage PC ","slots":{"cpu":3,"gpu":0},"tags":["fast"," fast ",""],"pool":"home","schedule":{"from":"22:00","to":"07:00"}}`})
	wk = decode[v2Worker](t, body)
	if code != 200 || wk.Name != "Garage PC" || wk.Slots.CPU.Total != 3 || wk.Slots.GPU.Total != 0 || strings.Join(wk.Tags, ",") != "fast" ||
		wk.Pool == nil || *wk.Pool != "home" || wk.Schedule == nil || wk.Schedule.From != "22:00" {
		t.Fatalf("patch %d %s", code, body)
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/settings"})
	if !strings.Contains(body, `"parallelJobs":3`) || !strings.Contains(body, `"from":"22:00"`) {
		t.Errorf("settings %s", body)
	}
	for _, bad := range []string{`{"slots":{"cpu":0}}`, `{"slots":{"cpu":9}}`, `{"slots":{"gpu":-1}}`, `{"name":"  "}`, `{"schedule":{"from":"22:00","to":""}}`} {
		if code, b, _ := do(t, nil, srv, call{method: "PATCH", path: "/api/v2/workers/local", ctype: "application/json", body: bad}); code != 400 || errCode(b) != "invalid_setting" {
			t.Errorf("%s: %d %s", bad, code, b)
		}
	}
	if _, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workers/local"}); decode[v2Worker](t, b).Slots.CPU.Total != 3 {
		t.Errorf("a refused PATCH changed something: %s", b)
	}

	// start, a job running (busy), drain (draining while it finishes), stop
	act := func(a string) v2Worker {
		t.Helper()
		code, b, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/local/" + a, body: "{}", ctype: "application/json"})
		if code != 200 {
			t.Fatalf("%s: %d %s", a, code, b)
		}
		return decode[v2Worker](t, b)
	}
	if w := act("start"); w.Status != "online" {
		t.Errorf("started: %s", w.Status)
	}
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv")})
	st.Claim("w")
	_, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workers"})
	var list struct {
		Items []v2Worker
		Total int
	}
	json.Unmarshal([]byte(b), &list)
	if list.Total != 1 || list.Items[0].Status != "busy" || list.Items[0].Slots.CPU.Used != 1 || len(list.Items[0].CurrentJobIDs) != 1 {
		t.Errorf("busy %s", b)
	}
	if w := act("drain"); w.Status != "draining" {
		t.Errorf("drain: %s", w.Status)
	}
	st.Complete(id, filepath.Join(lib.Path, "a.mkv"), 1)
	if w := act("stop"); w.Status != "paused" {
		t.Errorf("stop: %s", w.Status)
	}
	if code, _, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workers/other"}); code != 404 {
		t.Errorf("other worker: %d", code)
	}
	if code, _, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/local/explode", body: "{}", ctype: "application/json"}); code != 404 {
		t.Errorf("unknown action: %d", code)
	}
	// the summary has it too
	if _, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/summary"}); !strings.Contains(b, `"workersOffline":0`) || !strings.Contains(b, `"name":"Garage PC"`) {
		t.Errorf("summary %s", b)
	}
}

// The live stream says the worker is there every few seconds (worker.heartbeat, no id).
func TestV2WorkerHeartbeat(t *testing.T) {
	old := eventHeartbeat
	eventHeartbeat = 50 * time.Millisecond
	defer func() { eventHeartbeat = old }()
	srv, _ := workersServer(t)
	c := stream(t, srv.URL, "")
	m := nextMessage(t, c)
	for m.event != "worker.heartbeat" {
		m = nextMessage(t, c)
	}
	if m.id != "" || !strings.Contains(m.data, `"workerId":"local"`) || !strings.Contains(m.data, `"slots":{"cpu":{"total":1,"used":0}`) {
		t.Errorf("%+v", m)
	}
}
