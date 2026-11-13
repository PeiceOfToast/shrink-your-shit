// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	st.Fail(failed, "convert_failed", "Converting stopped with an error.")
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

// The folder for new files (S6): check-output says what blocks a folder, and a save with one is refused.
func TestCheckOutput(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), false)
	path := "/api/flows/" + itoa(lib.FlowID)
	check := func(dir string) string {
		t.Helper()
		_, body := post(t, srv, "POST", path+"/check-output", `{"path":`+strconv.Quote(dir)+`}`)
		var v struct{ Blockers []map[string]string }
		json.Unmarshal([]byte(body), &v)
		var codes []string
		for _, b := range v.Blockers {
			codes = append(codes, b["code"])
		}
		return strings.Join(codes, ",")
	}
	free := t.TempDir()
	if got := check(free); got != "" && got != "low_space" { // a small test disk may be low on space
		t.Errorf("a free folder: %s", got)
	}
	if got := check(filepath.Join(lib.Path, "converted")); !strings.Contains(got, "inside_library") {
		t.Errorf("inside the library: %s", got)
	}
	if got := check("relative/dir"); got != "not_writable" {
		t.Errorf("relative: %s", got)
	}
	ro := filepath.Join(t.TempDir(), "ro")
	os.MkdirAll(ro, 0o555)
	if os.Getuid() != 0 && runtime.GOOS != "windows" { // root writes anywhere; Windows ignores a folder's read-only mode
		if got := check(ro); !strings.Contains(got, "not_writable") {
			t.Errorf("read-only: %s", got)
		}
	}
	// a save with a blocked folder is refused with its code; a missing folder too
	if code, body := post(t, srv, "PUT", path+"/setting", `{"quality":3,"speed":"slower","afterConverting":"folder","outputDir":`+strconv.Quote(filepath.Join(lib.Path, "x"))+`}`); code != 400 || errCode(body) != "inside_library" {
		t.Errorf("save inside: %d %s", code, body)
	}
	// ui-shop QA (qa-standard d): a system folder is refused, at the check and on Save, whoever the server runs as
	if got := check(jobs.SystemDirForTest()); got != "system_folder" {
		t.Errorf("a system folder: %q", got)
	}
	if code, body := post(t, srv, "PUT", path+"/setting", `{"quality":3,"speed":"slower","afterConverting":"folder","outputDir":`+strconv.Quote(jobs.SystemDirForTest())+`}`); code != 400 || errCode(body) != "system_folder" {
		t.Errorf("save with a system folder: %d %s", code, body)
	}
	if code, body := post(t, srv, "PUT", path+"/setting", `{"quality":3,"speed":"slower","afterConverting":"folder"}`); code != 400 || errCode(body) != "output_dir_needed" {
		t.Errorf("save without folder: %d %s", code, body)
	}
}

// The rough-picture warning (S6): POST /api/flows/{id}/warnings for a setting being edited, and the same
// on the setting's GET, from the videos of the flow's libraries.
func TestRoughPictureWarning(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	// the v1 setting cannot write a level (user review 11-13 item A: the new UI does), so a level flow is seeded
	s := rules.DefaultSelectors(3)
	s.FileType, s.MaxBitrate, s.RateControl = "keep", "low", "level"
	lib, _ := st.AddLibrary("Movies", t.TempDir(), s.Template(), false)
	st.PutProbe(filepath.Join(lib.Path, "uhd.mkv"), 1, 1, `{"streams":[{"type":"video","codec":"hevc","Width":3840,"Height":2160,"bitDepth":10,"frameRate":60,"colorTransfer":"smpte2084"}]}`)
	path := "/api/flows/" + itoa(lib.FlowID)
	_, b, _ := do(t, nil, srv, call{method: "GET", path: path + "/setting"})
	if !strings.Contains(b, "Low may look blocky in dark or fast scenes for 4K HDR files at 50-60 fps (7.8 Mbps).") {
		t.Errorf("GET warnings: %s", b)
	}
	_, w := post(t, srv, "POST", path+"/warnings", `{"quality":3,"maxBitrate":"none","fileType":"keep"}`)
	if !strings.Contains(w, `"warnings":[]`) {
		t.Errorf("no limit: %s", w)
	}
	if f, _ := st.GetFlow(lib.FlowID); f.Template.Selectors == nil || f.Template.Selectors.MaxBitrate != "low" {
		t.Error("the warnings check saved something")
	}
}

// A migrated flow's notes (S6, contract 23:33Z) are on the setting's GET until the next save.
func TestFlowNotesUntilSaved(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), false)
	sel := rules.DefaultSelectors(3)
	sel.Speed, sel.Notes = "slower", []string{"Audio choices changed in this update: your flow now uses Stereo AAC."}
	st.SetFlowTemplate(lib.FlowID, sel.Template())
	path := "/api/flows/" + itoa(lib.FlowID) + "/setting"
	if _, b, _ := do(t, nil, srv, call{method: "GET", path: path}); !strings.Contains(b, `"notes":["Audio choices changed`) {
		t.Errorf("GET: %s", b)
	}
	post(t, srv, "PUT", path, `{"quality":3,"speed":"slower","notes":["sent back by the page"]}`)
	if _, b, _ := do(t, nil, srv, call{method: "GET", path: path}); strings.Contains(b, `"notes"`) {
		t.Errorf("after a save: %s", b)
	}
}

// Review 6cafc6f: a link can't hide a folder inside a library, and the folder is checked again when a
// library joins a folder flow (chosen, added on it) or moves.
func TestFolderOutputStaysOutsideLibraries(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), false)
	alias := filepath.Join(t.TempDir(), "alias")
	os.Symlink(lib.Path, alias)
	path := "/api/flows/" + itoa(lib.FlowID)
	if _, b := post(t, srv, "POST", path+"/check-output", `{"path":`+strconv.Quote(filepath.Join(alias, "new"))+`}`); !strings.Contains(b, "inside_library") {
		t.Errorf("a link into the library: %s", b)
	}
	// a folder flow with no library yet, then libraries that would hold its folder
	out := t.TempDir()
	sel := rules.DefaultSelectors(3)
	sel.Speed, sel.AfterConverting, sel.OutputDir = "slower", "folder", filepath.Join(out, "converted")
	flow, _ := st.AddFlow("Elsewhere", sel.Template())
	other, _ := st.AddLibrary("Other", t.TempDir(), rules.SliderTemplate(3), false)
	if code, b := post(t, srv, "PUT", "/api/libraries/"+itoa(other.ID)+"/flow", `{"flowId":"`+itoa(flow.ID)+`"}`); code != 200 {
		t.Fatalf("a library elsewhere: %d %s", code, b)
	}
	if code, b := post(t, srv, "PATCH", "/api/libraries/"+itoa(other.ID), `{"path":`+strconv.Quote(out)+`}`); code != 400 || errCode(b) != "inside_library" {
		t.Errorf("moved around the folder: %d %s", code, b)
	}
	if code, b := post(t, srv, "POST", "/api/libraries", `{"name":"Outer","path":`+strconv.Quote(out)+`,"flowId":"`+itoa(flow.ID)+`"}`); code != 400 || errCode(b) != "inside_library" {
		t.Errorf("added on the flow: %d %s", code, b)
	}
	if code, b := post(t, srv, "PUT", "/api/libraries/"+itoa(lib.ID)+"/flow", `{"flowId":"`+itoa(flow.ID)+`"}`); code != 200 {
		t.Errorf("Movies is elsewhere too: %d %s", code, b)
	}
}

// Row 06:28Z: the Skipped detail for already_in_format is the plan's own sentence (its format, file
// type and audio), in both lists, not a fixed "HEVC in MKV".
func TestAlreadyInFormatDetailIsThePlans(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	st.PutFile(jobs.File{Path: filepath.Join(lib.Path, "a.webm"), Size: 5, Decision: "skip: it is already VP9 in WebM with the audio you chose", Reason: rules.SkipAlreadyInFormat})
	for _, path := range []string{"/api/activity/files?show=skipped", "/api/activity/skipped"} {
		_, body, _ := do(t, nil, srv, call{method: "GET", path: path})
		if !strings.Contains(body, `"It is already VP9 in WebM with the audio you chose."`) {
			t.Errorf("%s: %s", path, body)
		}
	}
}
