// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A remote worker offers its graphics encoder until three falls in a row pause it, then it is offered the
// processor ("cpu") until the pause ends. The pause's expiry is covered in internal/jobs: the store's clock is not
// reachable from here.
func TestRemoteMainEncoderIsCPUWhilePaused(t *testing.T) {
	st, err := jobs.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.SetPaused(false)
	rw := jobs.RemoteWorker{ID: "rw1", Capabilities: `[{"key":"encoder:hevc_qsv","kind":"encoder","slot":"gpu","test":"passed"}]`}
	id, err := st.Enqueue(jobs.Job{Path: "/m/a.mkv", SizeBefore: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Claim("rw1"); err != nil {
		t.Fatal(err)
	}
	enc, err := remoteMainEncoder(st, rw)
	if err != nil || enc != "hevc_qsv" {
		t.Fatalf("before falls: encoder %q, %v; want hevc_qsv", enc, err)
	}
	for i := 0; i < 3; i++ {
		if _, err := st.EncoderFell(id, "rw1", "hevc_qsv", "Error while opening encoder"); err != nil {
			t.Fatal(err)
		}
	}
	enc, err = remoteMainEncoder(st, rw)
	if err != nil || enc != "cpu" {
		t.Fatalf("after three falls: encoder %q, %v; want cpu", enc, err)
	}
}

// The overview lists each worker's last graphics-encoder fall-back with its last error and the count since.
func TestOverviewListsEncoderAndLastError(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	id, err := st.Enqueue(jobs.Job{Path: "/m/a.mkv", SizeBefore: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Claim("builtin"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EncoderFell(id, "builtin", "hevc_nvenc", "Error while opening encoder"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EncoderFell(id, "builtin", "h264_qsv", "Unknown encoder 'h264_qsv'"); err != nil {
		t.Fatal(err)
	}
	code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/overview"})
	if code != 200 {
		t.Fatalf("overview: status %d %s", code, body)
	}
	var out struct {
		EncoderFallbacks []struct {
			WorkerID  string `json:"workerId"`
			Encoder   string `json:"encoder"`
			LastError string `json:"lastError"`
			Count     int    `json:"count"`
		} `json:"encoderFallbacks"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(out.EncoderFallbacks) != 1 {
		t.Fatalf("encoderFallbacks %+v, want one row", out.EncoderFallbacks)
	}
	r := out.EncoderFallbacks[0]
	if r.Encoder != "h264_qsv" || r.LastError != "Unknown encoder 'h264_qsv'" || r.Count != 2 {
		t.Errorf("row %+v: want encoder h264_qsv, lastError Unknown encoder 'h264_qsv', count 2", r)
	}
}

// A completed file a graphics encoder failed on carries its warning in the Jobs list (ui-shop 11-19), so the list
// can flag it; a plain completed file has warning null.
func TestJobsListCarriesTheFallbackWarning(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	fell := done(t, st, l.ID, "/m/a.mkv", "/m/a.mkv", 1000, 400)
	if err := st.AddStep(fell, jobs.EncoderFallbackStep, false, "hevc_qsv failed; converted on the processor."); err != nil {
		t.Fatal(err)
	}
	done(t, st, l.ID, "/m/b.mkv", "/m/b.mkv", 1000, 400)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=completed&limit=200"})
	var r struct{ Items []map[string]any }
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for _, it := range r.Items {
		w, ok := it["warning"]
		if !ok {
			t.Errorf("item %v has no warning field", it["id"])
		}
		got[it["id"].(string)] = w
	}
	if len(got) != 2 || got[strconv.FormatInt(fell, 10)] != "hevc_qsv failed; converted on the processor." {
		t.Errorf("warnings by id %v, want the fallen job's words and null for the other\n%s", got, body)
	}
	for id, w := range got {
		if id != strconv.FormatInt(fell, 10) && w != nil {
			t.Errorf("job %s: warning %v, want null", id, w)
		}
	}
}
