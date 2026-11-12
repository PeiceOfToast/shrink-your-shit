// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

type v2Library struct {
	ID, Name, WorkflowID, Status string
	Paths                        []string
	Enabled                      bool
	Priority                     int
	Counts                       map[string]int
	Output                       struct {
		Policy         string
		Folder         *string
		OverrideFolder *string
		Problem        *struct{ Code, Message string }
	}
	Discovery map[string]any
}

// V5b: the libraries in the contract's shape; pause and resume (also by the old pages' Start), priority,
// the folder for new files; adding, renaming and the workflow through v1's checks.
func TestV2Libraries(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	send := func(method, path, body string) (int, string) {
		t.Helper()
		code, b, _ := do(t, nil, srv, call{method: method, path: path, body: body, ctype: "application/json"})
		return code, b
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, "movies")
	os.Mkdir(dir, 0o755)
	code, body := send("POST", "/api/v2/libraries", `{"name":"Movies","paths":[`+strconv.Quote(dir)+`]}`)
	lib := decode[v2Library](t, body)
	if code != 201 || lib.Name != "Movies" || len(lib.Paths) != 1 || lib.Enabled || lib.Status != "disabled" || lib.Output.Policy != "replace" ||
		lib.Discovery["subfolders"] != true || lib.Discovery["followLinks"] != false {
		t.Fatalf("add %d %s", code, body)
	}
	path := "/api/v2/libraries/" + lib.ID
	if code, body := send("POST", "/api/v2/libraries", `{"name":"Two","paths":["/a","/b"]}`); code != 400 {
		t.Errorf("two folders: %d %s", code, body)
	}
	if code, body := send("POST", "/api/v2/libraries", `{"name":"Movies again","paths":[`+strconv.Quote(dir)+`]}`); code < 400 {
		t.Errorf("v1's checks (the same folder): %d %s", code, body)
	}

	// resume, a waiting job, then pause: the job stays, is library_disabled and isn't taken
	if code, body := send("PATCH", path, `{"enabled":true,"priority":7,"name":"Films"}`); code != 200 || !strings.Contains(body, `"enabled":true`) ||
		!strings.Contains(body, `"priority":7`) || !strings.Contains(body, `"name":"Films"`) {
		t.Fatalf("resume %d %s", code, body)
	}
	id, _ := strconv.ParseInt(lib.ID, 10, 64)
	job, err := st.Enqueue(jobs.Job{Library: dir, LibraryID: id, Path: filepath.Join(dir, "a.mkv")})
	if err != nil {
		t.Fatal(err)
	}
	if j, _ := st.Get(job); j.ID == 0 {
		t.Fatal("no job")
	}
	if code, body := send("PATCH", path, `{"enabled":false}`); code != 200 || decode[v2Library](t, body).Status != "disabled" {
		t.Fatalf("pause %d %s", code, body)
	}
	if _, body := send("GET", "/api/v2/jobs?tab=waiting", ""); !strings.Contains(body, `"subState":"library_disabled"`) {
		t.Errorf("paused library's job %s", body)
	}
	if _, err := st.Claim("w"); err != jobs.ErrNoJob {
		t.Errorf("a paused library's job was taken: %v", err)
	}
	if code, _ := send("POST", "/api/libraries/"+lib.ID+"/scan", `{}`); code != 200 { // the old pages' Start resumes it
		t.Errorf("old Start %d", code)
	}
	if _, body := send("GET", path, ""); !strings.Contains(body, `"enabled":true`) {
		t.Errorf("after the old Start %s", body)
	}
	if j, err := st.Claim("w"); err != nil || j.ID != job { // claimable again (priority reaching jobs: TestLibraryPriority)
		t.Errorf("after resume %+v %v", j, err)
	}

	// the folder for new files: a library on an output-first workflow, its override, and the checks
	flow, _ := st.AddFlow("Output first", (&rules.Selectors{Quality: 3, Speed: "normal", Format: "hevc", Resolution: "keep", MaxBitrate: "none",
		Audio: "keep", Subtitles: "keep", FileType: "keep", AfterConverting: "folder", FolderMode: rules.NextToLibrary}).Template())
	if code, body := send("PATCH", path, `{"workflowId":"`+strconv.FormatInt(flow.ID, 10)+`"}`); code != 200 {
		t.Fatalf("workflow %d %s", code, body)
	}
	_, body = send("GET", path, "")
	l := decode[v2Library](t, body)
	// beside the library's folder as the server keeps it (links resolved: macOS /var is /private/var,
	// Windows' short names are long), not the test's raw temp path (CI 01:47Z)
	if l.Output.Policy != "separate_folder" || l.Output.Folder == nil || len(l.Paths) != 1 || *l.Output.Folder != l.Paths[0]+" (shrunk)" || l.Output.Problem != nil {
		t.Errorf("default folder %s", body)
	}
	own := t.TempDir()
	if code, body := send("PATCH", path, `{"overrideFolder":`+strconv.Quote(own)+`}`); code != 200 || !strings.Contains(body, strconv.Quote(own)) {
		t.Errorf("override %d %s", code, body)
	}
	for _, bad := range []string{`{"overrideFolder":` + strconv.Quote(filepath.Join(dir, "x")) + `}`, `{"overrideFolder":"relative"}`, `{"priority":"high"}`,
		`{"enabled":"yes"}`, `{"colour":"red"}`} {
		if code, b := send("PATCH", path, bad); code != 400 {
			t.Errorf("%s: %d %s", bad, code, b)
		}
	}
	if code, b := send("PATCH", path, `{"discovery":{"subfolders":false}}`); code != 400 || errCode(b) != "not_available" {
		t.Errorf("discovery: %d %s", code, b)
	}
	if code, body := send("PATCH", path, `{"overrideFolder":null}`); code != 200 || !strings.Contains(body, `"overrideFolder":null`) {
		t.Errorf("back to the default %d %s", code, body)
	}
	if _, body := send("GET", "/api/v2/libraries?workflowId="+strconv.FormatInt(flow.ID, 10), ""); !strings.Contains(body, `"total":1`) {
		t.Errorf("filter %s", body)
	}
	if code, _ := send("GET", "/api/v2/libraries/999", ""); code != 404 {
		t.Errorf("no such library %d", code)
	}
	if code, body := send("DELETE", path, ""); code >= 300 {
		t.Errorf("delete %d %s", code, body)
	}
}

// Review a118b1b's P3s (V4a): a non-number slot is invalid_setting; stop answers paused.
func TestV2WorkerReviewP3s(t *testing.T) {
	srv, _ := workersServer(t)
	if code, b, _ := do(t, nil, srv, call{method: "PATCH", path: "/api/v2/workers/local", ctype: "application/json", body: `{"slots":{"cpu":"four"}}`}); code != 400 || errCode(b) != "invalid_setting" {
		t.Errorf("non-number slot: %d %s", code, b)
	}
}

// ui-shop 01:31Z: the summary's workersRunning is a count (the contract's number), not yes/no.
func TestV2SummaryWorkersRunningIsACount(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	if _, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/summary"}); !strings.Contains(b, `"workersRunning":1`) {
		t.Errorf("started: %s", b)
	}
	st.SetPaused(true)
	if _, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/summary"}); !strings.Contains(b, `"workersRunning":0`) {
		t.Errorf("stopped: %s", b)
	}
}

// V5c (ui-shop 01:31Z): the summary's bytes per library, and freed in the last 7 days, dated by when a
// left-in-place original was found gone.
func TestV2SummaryFreedBytes(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	other, _ := st.AddLibrary("TV", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv"), SizeBefore: 1000})
	st.Claim("w")
	st.Complete(id, filepath.Join(lib.Path+" (shrunk)", "a.mkv"), 400) // output-first: the original stays
	want := func(s string) {
		t.Helper()
		if _, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/summary"}); !strings.Contains(b, s) {
			t.Errorf("want %s in %s", s, b)
		}
	}
	movies, tv := strconv.FormatInt(lib.ID, 10), strconv.FormatInt(other.ID, 10)
	// left where it was: could free, not waiting (design 01:55Z)
	want(`{"couldFree":600,"freed":0,"grew":0,"libraryId":"` + movies + `","waitingToFree":0}`)
	want(`{"couldFree":0,"freed":0,"grew":0,"libraryId":"` + tv + `","waitingToFree":0}`)
	want(`"couldFree":600,"freed":0,"freedLast7d":0,"grew":0`)
	st.SetOriginalGone(id) // a scan found the original gone
	want(`{"couldFree":0,"freed":600,"grew":0,"libraryId":"` + movies + `","waitingToFree":0}`)
	want(`"couldFree":0,"freed":600,"freedLast7d":600,"grew":0`)
}

// orch-lead 18:15Z (ui-shop review): a new file bigger than its original (kept by design) saves nothing
// rather than a negative amount, and its growth shows as grew; seen waitingToFree -463,050 after a
// Convert again at higher quality. One file shrank by 600, one grew by 300, both originals kept.
func TestV2SpaceBucketsNeverNegative(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	for i, after := range []int64{400, 1300} {
		p := filepath.Join(lib.Path, strconv.Itoa(i)+".mkv")
		id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: p, SizeBefore: 1000})
		st.Claim("w")
		row, _ := st.BeginSwap(jobs.Original{JobID: id, Path: p, Trash: filepath.Join(lib.Path, ".syp-trash", strconv.Itoa(i)+".mkv")})
		st.FinishSwap(row, time.Now().Add(7*24*time.Hour))
		st.Complete(id, p, after)
	}
	movies := strconv.FormatInt(lib.ID, 10)
	_, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/summary"})
	if !strings.Contains(b, `{"couldFree":0,"freed":0,"grew":300,"libraryId":"`+movies+`","waitingToFree":600}`) ||
		!strings.Contains(b, `"couldFree":0,"freed":0,"freedLast7d":0,"grew":300,`) || !strings.Contains(b, `"waitingToFree":600`) {
		t.Errorf("summary: %s", b)
	}
	if _, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/libraries/" + movies}); !strings.Contains(b, `"bytes":{"couldFree":0,"freed":0,"grew":300,"waitingToFree":600}`) {
		t.Errorf("library: %s", b)
	}
}

// A library's discovery shows the interval the scanner runs with: a saved Settings value wins over the
// flag (#136).
func TestV2LibraryScanEveryFollowsSettings(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	if code, b, _ := do(t, nil, srv, call{method: "PATCH", path: "/api/settings", body: `{"scanEvery":120}`, ctype: "application/json"}); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	if _, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/libraries/" + strconv.FormatInt(lib.ID, 10)}); !strings.Contains(b, `"scanEverySeconds":120`) ||
		!strings.Contains(b, `"scheduleLabel":"Every 2 minutes"`) {
		t.Errorf("%s", b)
	}
}

// Review 5d8b1f9 P2: a paused library's waiting jobs have no place in the queue (position null, listed
// after the ready ones), as Claim doesn't take them; the ready jobs start at 1.
func TestPausedLibraryJobsHaveNoPosition(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	movies, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	tv, _ := st.AddLibrary("TV", t.TempDir(), rules.SliderTemplate(3), true)
	st.Enqueue(jobs.Job{Library: movies.Path, LibraryID: movies.ID, Path: filepath.Join(movies.Path, "m.mkv")}) // older: first otherwise
	st.Enqueue(jobs.Job{Library: tv.Path, LibraryID: tv.ID, Path: filepath.Join(tv.Path, "t.mkv")})
	st.StopLibrary(movies.ID)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=waiting&sort=queue"})
	var l struct {
		Items []struct {
			Name     string
			Position *int
			SubState string
		}
	}
	json.Unmarshal([]byte(body), &l)
	if len(l.Items) != 2 || l.Items[0].Name != "t.mkv" || l.Items[0].Position == nil || *l.Items[0].Position != 1 ||
		l.Items[1].Name != "m.mkv" || l.Items[1].Position != nil || l.Items[1].SubState != "library_disabled" {
		t.Errorf("%s", body)
	}
}

// User review 11-12 item 2 (ui-shop 14:01Z): every worker at once, answered with the workers list.
func TestV2AllWorkersSwitch(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	act := func(action string) (int, string) {
		code, body, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/all/" + action, body: `{}`, ctype: "application/json"})
		return code, body
	}
	if code, body := act("drain"); code != 200 || !st.Paused() || !strings.Contains(body, `"items":[`) || !strings.Contains(body, `"total":1`) {
		t.Errorf("drain %d %s", code, body)
	}
	if code, body := act("start"); code != 200 || st.Paused() || !strings.Contains(body, `"status":"online"`) {
		t.Errorf("start %d %s", code, body)
	}
	if code, body := act("stop"); code != 200 || !st.Paused() || !strings.Contains(body, `"status":"paused"`) {
		t.Errorf("stop %d %s", code, body)
	}
	if code, body := act("explode"); code != 404 {
		t.Errorf("unknown %d %s", code, body)
	}
}
