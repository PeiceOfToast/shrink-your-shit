// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
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
