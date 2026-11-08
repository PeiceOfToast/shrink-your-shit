// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/runlog"
)

// The Files view (S5): one list by card, with every card's fields, the counts, and a job's run log.
func TestFilesView(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetPaused(false) // a new install starts with its worker stopped (S2); these jobs are claimed
	a, _, _ := OpenAccess(t.TempDir(), false)
	logs := &runlog.Logs{Dir: t.TempDir()}
	srv := httptest.NewServer(Handler(st, a, Engine{RunLogs: logs}))
	defer srv.Close()
	lib, _ := st.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	m := lib.Path // the stored (absolute) folder: D:\m on Windows
	done, _ := st.Enqueue(jobs.Job{Library: m, LibraryID: lib.ID, Path: filepath.Join(m, "Film.mkv"), SizeBefore: 1000})
	st.Claim("w")
	st.Complete(done, filepath.Join(m, "Film.mkv"), 400)
	failed, _ := st.Enqueue(jobs.Job{Library: m, LibraryID: lib.ID, Path: filepath.Join(m, "Bad.mkv")})
	st.Claim("w")
	st.Fail(failed, "Converting stopped with an error.")
	st.Enqueue(jobs.Job{Library: m, LibraryID: lib.ID, Path: filepath.Join(m, "Season 1", "Next.mkv")})
	st.PutFile(jobs.File{Path: filepath.Join(m, "Old.mkv"), Size: 5, Decision: "skip: x", Reason: rules.SkipAlreadyHEVC})
	st.PutProbe(filepath.Join(m, "Film.mkv"), 400, 1, `{"durationSec":60,"streams":[{"type":"video","codec":"hevc","Width":1920,"Height":1080}]}`)
	st.AddStep(done, "found", true, "Found in /m.")
	logs.Append(done, "12:00:00 started")

	get := func(q string) (map[string]any, []map[string]any) {
		t.Helper()
		_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/files?" + q})
		var v struct {
			Items  []map[string]any
			Total  int
			Counts map[string]any
		}
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatalf("%s: %s", q, body)
		}
		return v.Counts, v.Items
	}
	counts, items := get("show=processed")
	if counts["processed"] != 1.0 || counts["failed"] != 1.0 || counts["unprocessed"] != 1.0 || counts["skipped"] != 1.0 || counts["processing"] != 0.0 {
		t.Errorf("counts %v", counts)
	}
	if len(items) != 1 {
		t.Fatalf("processed %v", items)
	}
	p := items[0]
	if p["name"] != "Film.mkv" || p["folder"] != "." || p["library"] != "Movies" || p["codec"] != "HEVC" || p["resolution"] != "1080p" ||
		p["durationSec"] != 60.0 || p["beforeBytes"] != 1000.0 || p["afterBytes"] != 400.0 || p["bytes"] != 400.0 || p["originalState"] == nil {
		t.Errorf("processed item %v", p)
	}
	if f, _ := p["flow"].(map[string]any); f == nil || f["name"] != "Movies" {
		t.Errorf("flow %v", p["flow"])
	}
	if _, items := get("show=failed"); len(items) != 1 || items[0]["title"] != "Converting did not finish" || items[0]["attempts"] != 1.0 {
		t.Errorf("failed %v", items)
	}
	if _, items := get("show=unprocessed"); len(items) != 1 || items[0]["status"] != "waiting" || items[0]["codec"] != nil || items[0]["folder"] != "Season 1" {
		t.Errorf("unprocessed (never probed: null) %v", items)
	}
	if _, items := get("show=skipped"); len(items) != 1 || items[0]["reason"] != rules.SkipAlreadyHEVC || items[0]["libraryId"] == nil || !strings.HasPrefix(items[0]["id"].(string), "skip-") {
		t.Errorf("skipped %v", items)
	}
	if code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/files?show=nope"}); code != 400 {
		t.Errorf("bad card: %d %s", code, body)
	}
	// the run log: the summary and the text from an offset
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/files/" + itoa(done) + "/log"})
	var lg struct {
		Summary []map[string]any
		Text    string
		Next    int
		Live    bool
	}
	json.Unmarshal([]byte(body), &lg)
	if len(lg.Summary) != 1 || lg.Text != "12:00:00 started\n" || lg.Next != len(lg.Text) || lg.Live {
		t.Errorf("log %s", body)
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/activity/files/" + itoa(failed) + "/log"})
	if !strings.Contains(body, `"step":"converted","at"`) || !strings.Contains(body, "Converting stopped") { // from before S5: built from the record
		t.Errorf("old job's log %s", body)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

// Review 43e6f66: a Beside job's processed row shows the new file's probe, not the original's; a job
// from before S5 that was kept beside says so in its summary.
func TestFilesViewBeside(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetPaused(false)
	a, _, _ := OpenAccess(t.TempDir(), false)
	srv := httptest.NewServer(Handler(st, a, Engine{RunLogs: &runlog.Logs{Dir: t.TempDir()}}))
	defer srv.Close()
	lib, _ := st.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: lib.ID, Path: "/m/Film.mkv", SizeBefore: 1000})
	st.Claim("w")
	st.Complete(id, "/m/Film.hevc.mkv", 400)
	st.PutProbe("/m/Film.mkv", 1000, 1, `{"streams":[{"type":"video","codec":"h264","Width":1280,"Height":720}]}`)
	st.PutProbe("/m/Film.hevc.mkv", 400, 1, `{"streams":[{"type":"video","codec":"hevc","Width":1920,"Height":1080}]}`)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/files?show=processed"})
	if !strings.Contains(body, `"codec":"HEVC"`) || !strings.Contains(body, `"resolution":"1080p"`) || !strings.Contains(body, `"originalState":"kept_beside"`) {
		t.Errorf("beside row %s", body)
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/activity/files/" + itoa(id) + "/log"})
	if !strings.Contains(body, `"step":"kept"`) || strings.Contains(body, `"step":"replaced"`) {
		t.Errorf("summary %s", body)
	}
}

// Live QA 03:03Z: a Replace job's processed row (an originals row, whose aside column is TEXT) and a
// Beside one list together; scanning aside as a bool made every Replace row a 500.
func TestFilesViewReplaceAndBeside(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetPaused(false)
	a, _, _ := OpenAccess(t.TempDir(), false)
	srv := httptest.NewServer(Handler(st, a, Engine{RunLogs: &runlog.Logs{Dir: t.TempDir()}}))
	defer srv.Close()
	lib, _ := st.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	m := lib.Path
	replaced, _ := st.Enqueue(jobs.Job{Library: m, LibraryID: lib.ID, Path: filepath.Join(m, "Replaced.mkv"), SizeBefore: 1000})
	st.Claim("w")
	o, err := st.BeginSwap(jobs.Original{JobID: replaced, LibraryID: lib.ID, Path: filepath.Join(m, "Replaced.mkv"),
		Trash: filepath.Join(m, ".syp-trash", "Replaced.mkv"), NewPath: filepath.Join(m, "Replaced.mkv")})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishSwap(o, time.Now().Add(72*time.Hour)); err != nil {
		t.Fatal(err)
	}
	st.Complete(replaced, filepath.Join(m, "Replaced.mkv"), 400)
	beside, _ := st.Enqueue(jobs.Job{Library: m, LibraryID: lib.ID, Path: filepath.Join(m, "Beside.mkv"), SizeBefore: 1000})
	st.Claim("w")
	st.Complete(beside, filepath.Join(m, "Beside.hevc.mkv"), 400)

	code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/files?show=processed"})
	if code != 200 {
		t.Fatalf("processed: %d %s", code, body)
	}
	var v struct{ Items []map[string]any }
	if err := json.Unmarshal([]byte(body), &v); err != nil || len(v.Items) != 2 {
		t.Fatalf("processed items: %v %s", err, body)
	}
	states := map[any]any{}
	for _, it := range v.Items {
		states[it["name"]] = it["originalState"]
	}
	if states["Replaced.mkv"] != jobs.OriginalKept || states["Beside.mkv"] != "kept_beside" {
		t.Errorf("original states %v", states)
	}
}
