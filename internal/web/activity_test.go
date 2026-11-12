// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// done makes a finished job: original size before, new size after, output at out.
func done(t *testing.T, st *jobs.Store, libID int64, path, out string, before, after int64) int64 {
	t.Helper()
	id, err := st.Enqueue(jobs.Job{Library: "/m", LibraryID: libID, Path: path, SizeBefore: before})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Claim("t"); err != nil {
		t.Fatal(err)
	}
	if err := st.Complete(id, out, after); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDoneAndKeptLists(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	kept := done(t, st, l.ID, "/m/kept.mkv", "/m/kept.mkv", 1000, 400)
	oid, _ := st.BeginSwap(jobs.Original{JobID: kept, LibraryID: l.ID, Path: "/m/kept.mkv", Trash: "/m/.syp-trash/kept.1.mkv", Staged: "s", Bytes: 1000, Hash: "h", NewBytes: 400})
	st.FinishSwap(oid, time.Now().Add(7*24*time.Hour-time.Minute))
	done(t, st, l.ID, "/m/gone.mkv", "/m/gone.mkv", 500, 300)          // replaced, nothing kept (0 days)
	done(t, st, l.ID, "/m/beside.mkv", "/m/beside.hevc.mkv", 800, 200) // beside: original untouched

	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/done"})
	var d struct {
		Items []map[string]any
		Total int
	}
	json.Unmarshal([]byte(body), &d)
	if d.Total != 3 || len(d.Items) != 3 {
		t.Fatalf("%s", body)
	}
	byPath := map[string]map[string]any{}
	for _, i := range d.Items {
		byPath[i["path"].(string)] = i
	}
	if k := byPath["/m/kept.mkv"]; k["originalState"] != "kept" || k["keptDaysLeft"] != 7.0 || k["keptId"] != fmt.Sprint(oid) || k["library"] != "Movies" || k["beforeBytes"] != 1000.0 || k["afterBytes"] != 400.0 {
		t.Errorf("kept: %v", k)
	}
	if g := byPath["/m/gone.mkv"]; g["originalState"] != "deleted" || g["keptId"] != nil || g["keptDaysLeft"] != nil {
		t.Errorf("gone: %v", g)
	}
	if b := byPath["/m/beside.mkv"]; b["originalState"] != "kept" || b["keptId"] != nil {
		t.Errorf("beside: %v", b)
	}

	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/activity/kept"})
	var k struct {
		KeptBytes, Count, Days int
		Items                  []map[string]any
	}
	json.Unmarshal([]byte(body), &k)
	if k.KeptBytes != 1000 || k.Count != 1 || k.Days != 7 || k.Items[0]["path"] != "/m/kept.mkv" || k.Items[0]["nowBytes"] != 400.0 || k.Items[0]["library"] != "Movies" {
		t.Errorf("%s", body)
	}

	// F1: freed = originals gone (500-300); waiting = originals kept (1000-400) and, since V5c (design
	// call 3), the beside one's original still there (another 600)
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/libraries"})
	if !strings.Contains(body, `"freedBytes":200`) || !strings.Contains(body, `"waitingBytes":1200`) {
		t.Errorf("%s", body)
	}

	// the recorded trash file does not exist here: refused, nothing moved
	code, body := post(t, srv, "POST", fmt.Sprintf("/api/kept/%d/restore", oid), `{}`)
	if code != 409 || !strings.Contains(body, "original_missing") || !strings.Contains(body, "Nothing was changed") {
		t.Errorf("%d %s", code, body)
	}
	if code, body := post(t, srv, "POST", "/api/kept/999/restore", `{}`); code != 404 {
		t.Errorf("%d %s", code, body)
	}
}

func TestActivitySummaryQueueFailedSkipped(t *testing.T) {
	srv, st, _, fe := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	// the library is stored resolved (/private/var on macOS, long names on Windows), and the scanner
	// records files under that stored path, so the test's files are too
	dir := l.Path
	// done, original gone: 300 freed; done, original kept: 600 waiting
	os.WriteFile(dir+"/gone.mkv", make([]byte, 200), 0o644) // the converted file, so its time is recorded
	done(t, st, l.ID, dir+"/gone.mkv", dir+"/gone.mkv", 500, 200)
	kept := done(t, st, l.ID, dir+"/kept.mkv", dir+"/kept.mkv", 1000, 400)
	oid, _ := st.BeginSwap(jobs.Original{JobID: kept, LibraryID: l.ID, Path: dir + "/kept.mkv", Trash: "t", Staged: "s", Bytes: 1000, Hash: "h", NewBytes: 400})
	st.FinishSwap(oid, time.Now().Add(time.Hour))
	// one failed, one kept by a check
	f1, _ := st.Enqueue(jobs.Job{Library: dir, LibraryID: l.ID, Path: dir + "/broken.mkv"})
	st.Claim("t")
	st.Fail(f1, "convert_failed", "ffmpeg stopped: invalid data")
	f2, _ := st.Enqueue(jobs.Job{Library: dir, LibraryID: l.ID, Path: dir + "/short.mkv"})
	st.Claim("t")
	st.Keep(f2, "kept_original", "the new file is 3 minutes shorter than the original")
	// one running at 50%, two waiting
	run, _ := st.Enqueue(jobs.Job{Library: dir, LibraryID: l.ID, Path: dir + "/now.mkv", SizeBefore: 8000})
	st.Claim("t")
	st.Progress(run, 50)
	st.Enqueue(jobs.Job{Library: dir, LibraryID: l.ID, Path: dir + "/next1.mkv"})
	st.Enqueue(jobs.Job{Library: dir, LibraryID: l.ID, Path: dir + "/next2.mkv"})
	// skipped files
	st.PutFile(jobs.File{Path: dir + "/hevc.mkv", Decision: "skip: HEVC is already an efficient format", Reason: rules.SkipAlreadyHEVC})
	st.PutFile(jobs.File{Path: dir + "/lean.mkv", Decision: "skip: already efficient: 0.02 bits per pixel", Reason: rules.SkipAlreadyEfficient})
	st.PutFile(jobs.File{Path: "/elsewhere/x.mkv", Decision: "unreadable", Reason: rules.SkipUnreadable})
	st.PutFile(jobs.File{Path: dir + "/queued.mkv", Decision: "queued: H.264"}) // not skipped
	// a converted file, which the scanner then skips as already HEVC: done, not "skipped"
	skippedHEVC(st, dir+"/gone.mkv")

	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/summary"})
	if body != `{"counts":{"failed":2,"skipped":3},"freedBytes":300,"paused":false,"waitingBytes":600,"waitingFiles":2,"workersRunning":true,"workingFiles":1}`+"\n" {
		t.Errorf("summary %s", body)
	}

	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/activity/queue"})
	var q struct {
		Working []map[string]any
		UpNext  []map[string]any
	}
	json.Unmarshal([]byte(body), &q)
	if len(q.Working) != 1 || q.Working[0]["phase"] != "converting" || q.Working[0]["percent"] != 50.0 || q.Working[0]["fromBytes"] != 8000.0 ||
		q.Working[0]["library"] != "Movies" || q.Working[0]["etaSeconds"] == nil {
		t.Errorf("working %s", body)
	}
	if len(q.UpNext) != 1 || q.UpNext[0]["count"] != 2.0 || q.UpNext[0]["order"] != "newest" {
		t.Errorf("up next %s", body)
	}

	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/activity/failed"})
	var f struct {
		Items []map[string]any
		Total int
	}
	json.Unmarshal([]byte(body), &f)
	if f.Total != 2 || f.Items[0]["title"] != "A check said no, so the original was kept" || f.Items[0]["detail"] != "the new file is 3 minutes shorter than the original" ||
		f.Items[1]["title"] != "Converting did not finish" || f.Items[1]["originalUntouched"] != true {
		t.Errorf("failed %s", body)
	}

	var k struct {
		Items []map[string]any
		Total int
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/activity/skipped"})
	json.Unmarshal([]byte(body), &k)
	if k.Total != 3 || len(k.Items) != 3 {
		t.Fatalf("skipped %s", body)
	}
	byPath := map[string]map[string]any{}
	for _, i := range k.Items {
		byPath[i["path"].(string)] = i
	}
	if h := byPath[dir+"/hevc.mkv"]; h["reason"] != "already_hevc" || h["detail"] != "It already uses a modern, efficient format." || h["library"] != "Movies" || h["libraryId"] != fmt.Sprint(l.ID) {
		t.Errorf("hevc %v", h)
	}
	if x := byPath["/elsewhere/x.mkv"]; x["libraryId"] != nil || x["reason"] != "unreadable" {
		t.Errorf("outside any library %v", x)
	}
	if strings.Contains(body, "bits per pixel") {
		t.Error("details are in plain words")
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/activity/skipped?reason=already_efficient&limit=1"})
	k.Items = nil
	json.Unmarshal([]byte(body), &k)
	if k.Total != 1 || len(k.Items) != 1 || k.Items[0]["path"] != dir+"/lean.mkv" {
		t.Errorf("filtered %s", body)
	}

	id := byPath[dir+"/hevc.mkv"]["id"].(string)
	scans := fe.scans
	if code, b := post(t, srv, "POST", "/api/skipped/"+id+"/convert-again", `{}`); code != 200 || fe.scans != scans+1 {
		t.Errorf("convert again: %d %s, scans %d", code, b, fe.scans-scans)
	}
	if f, _ := st.GetFile(dir + "/hevc.mkv"); f != nil {
		t.Error("its decision is forgotten, so the next scan looks again")
	}
	if code, _ := post(t, srv, "POST", "/api/skipped/"+id+"/convert-again", `{}`); code != 404 {
		t.Errorf("again: %d", code)
	}
	if code, b := post(t, srv, "POST", "/api/system/backup", `{}`); code != 501 || !strings.Contains(b, "Nothing was downloaded") {
		t.Errorf("backup %d %s", code, b)
	}
}

// A converted file is "done", not "skipped", only while it is the file the job wrote: a different file
// put at that path later and skipped is listed (review ce336a6).
func TestSkippedListsADifferentFileAtAConvertedPath(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	p := filepath.Join(l.Path, "a.mkv")
	os.WriteFile(p, make([]byte, 400), 0o644)
	done(t, st, l.ID, p, p, 1000, 400)
	skippedHEVC(st, p)
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/skipped"}); !strings.Contains(body, `"total":0`) {
		t.Errorf("the converted file itself: %s", body)
	}
	st.PutFile(jobs.File{Path: p, Size: 777, ModTime: 2, Decision: "skip: HEVC is already an efficient format", Reason: rules.SkipAlreadyHEVC})
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/skipped"}); !strings.Contains(body, `"total":1`) {
		t.Errorf("a different file at that path: %s", body)
	}
}

// Up next is per library, also when two libraries have the same name (review ce336a6).
func TestUpNextIsPerLibraryNotPerName(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	a, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	b, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	st.Enqueue(jobs.Job{Library: a.Path, LibraryID: a.ID, Path: filepath.Join(a.Path, "1.mkv")})
	st.Enqueue(jobs.Job{Library: b.Path, LibraryID: b.ID, Path: filepath.Join(b.Path, "2.mkv")})
	st.Enqueue(jobs.Job{Library: b.Path, LibraryID: b.ID, Path: filepath.Join(b.Path, "3.mkv")})
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/queue"})
	var q struct{ UpNext []map[string]any }
	json.Unmarshal([]byte(body), &q)
	if len(q.UpNext) != 2 || q.UpNext[0]["count"] != 1.0 || q.UpNext[1]["count"] != 2.0 {
		t.Errorf("%s", body)
	}
}

// doneWithFile finishes a job whose output is the real file p (so its time is recorded).
func doneWithFile(t *testing.T, st *jobs.Store, libID int64, p string) int64 {
	t.Helper()
	id, _ := st.Enqueue(jobs.Job{Library: filepath.Dir(p), LibraryID: libID, Path: p, SizeBefore: 1000})
	st.Claim("t")
	fi, _ := os.Stat(p)
	if err := st.Complete(id, p, fi.Size()); err != nil {
		t.Fatal(err)
	}
	return id
}

func skippedHEVC(st *jobs.Store, p string) {
	fi, _ := os.Stat(p)
	st.PutFile(jobs.File{Path: p, Size: fi.Size(), ModTime: fi.ModTime().UnixNano(), Decision: "skip: HEVC is already an efficient format", Reason: rules.SkipAlreadyHEVC})
}

// Retention 0 (no kept original, so no journal row): a different file of the same size later at the
// converted path is listed; the converted file itself is not (review 3c66783).
func TestSkippedAfterRetentionZeroSeesAReplacement(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	p := filepath.Join(l.Path, "a.mkv")
	os.WriteFile(p, []byte("converted"), 0o644)
	doneWithFile(t, st, l.ID, p)
	skippedHEVC(st, p)
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/skipped"}); !strings.Contains(body, `"total":0`) {
		t.Errorf("the converted file: %s", body)
	}
	os.WriteFile(p, []byte("different"), 0o644) // same size
	later := time.Now().Add(time.Hour)
	os.Chtimes(p, later, later)
	skippedHEVC(st, p)
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/skipped"}); !strings.Contains(body, `"total":1`) {
		t.Errorf("a same-size replacement: %s", body)
	}
}

// A swap rolled back (its journal row records another new file), then a successful retry: the retry's
// output is recognised, not listed as skipped (review 3c66783).
func TestSkippedAfterRollbackAndRetry(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	p := filepath.Join(l.Path, "a.mkv")
	os.WriteFile(p, []byte("converted"), 0o644)
	id, _ := st.Enqueue(jobs.Job{Library: l.Path, LibraryID: l.ID, Path: p, SizeBefore: 1000})
	st.Claim("t")
	oid, _ := st.BeginSwap(jobs.Original{JobID: id, Path: p, Trash: "t", Staged: "s", Bytes: 1000, Hash: "h", NewBytes: 9, NewMTime: 12345})
	st.SetSwapState(oid, jobs.SwapRolledBack)
	st.Release(id) // queued again, then converted successfully
	st.Claim("t")
	fi, _ := os.Stat(p)
	st.Complete(id, p, fi.Size())
	skippedHEVC(st, p)
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/skipped"}); !strings.Contains(body, `"total":0`) {
		t.Errorf("the retry's output: %s", body)
	}
}

// Get original back through the API, for a library whose record was deleted since (its kept originals
// stay restorable): the original is back, the file shows as restored in Done, and it is not kept any more.
func TestRestoreThroughTheAPI(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	dir := t.TempDir()
	l, _ := st.AddLibrary("Movies", dir, rules.SliderTemplate(3), true)
	path := filepath.Join(dir, "a.mkv")
	trash := filepath.Join(dir, ".syp-trash", "a.1.mkv")
	os.MkdirAll(filepath.Dir(trash), 0o755)
	os.WriteFile(trash, []byte("original-bytes"), 0o644)
	os.WriteFile(path, []byte("new"), 0o644)
	jid := done(t, st, l.ID, path, path, 14, 3)
	h, _ := worker.SampleHash(trash)
	nh, _ := worker.SampleHash(path)
	ost, _ := os.Stat(trash)
	nst, _ := os.Stat(path)
	oid, _ := st.BeginSwap(jobs.Original{JobID: jid, LibraryID: l.ID, Path: path, Trash: trash, Staged: "s", Bytes: 14, ModTime: ost.ModTime().UnixNano(), Hash: h, NewBytes: 3, NewMTime: nst.ModTime().UnixNano(), NewHash: nh})
	st.FinishSwap(oid, time.Now().Add(7*24*time.Hour))
	if err := st.DeleteLibrary(l.ID); err != nil {
		t.Fatal(err)
	}
	code, body := post(t, srv, "POST", fmt.Sprintf("/api/kept/%d/restore", oid), `{}`)
	if code != 200 || !strings.Contains(body, `"identityConfirmed":true`) || !strings.Contains(body, "identity is confirmed") {
		t.Fatalf("%d %s", code, body)
	}
	if b, _ := os.ReadFile(path); string(b) != "original-bytes" {
		t.Errorf("at the name: %q", b)
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/activity/done"})
	if !strings.Contains(body, `"originalState":"restored"`) {
		t.Errorf("%s", body)
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/activity/kept"})
	if !strings.Contains(body, `"count":0`) {
		t.Errorf("%s", body)
	}
	// listed under Skipped as restored (the page offers Convert again there), although a done job wrote it
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/activity/skipped?reason=restored"})
	if !strings.Contains(body, `"total":1`) || !strings.Contains(body, "you put its original back") && !strings.Contains(body, "You put its original back") {
		t.Errorf("skipped: %s", body)
	}
}

// Pause all / Resume and Stop this file through the API (slice 24).
func TestPauseResumeAndStop(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetPaused(false) // the test's worker is started (S2: a new install starts stopped)
	defer st.Close()
	a, _, _ := OpenAccess(t.TempDir(), false)
	a.FailDelay = 0
	// the fake worker: "stop" ends its job as stopped, "finish" means it was already being put in place
	// (it ends done), "slow" means it has not ended yet
	paused, running := 0, map[int64]string{}
	srv := httptest.NewServer(Handler(st, a, Engine{PauseNow: func() { paused++ },
		StopFile: func(id int64) bool {
			switch running[id] {
			case "stop":
				go st.UserStopped(id)
			case "finish":
				go st.Complete(id, "/m/x.mkv", 1)
			}
			return running[id] != ""
		}}))
	defer func(d time.Duration) { stopWait = d }(stopWait)
	stopWait = 300 * time.Millisecond
	defer srv.Close()

	if code, b := post(t, srv, "POST", "/api/queue/pause", `{}`); code != 200 || paused != 1 || !st.Paused() {
		t.Errorf("pause %d %s", code, b)
	}
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/summary"}); !strings.Contains(body, `"paused":true`) {
		t.Errorf("%s", body)
	}
	if code, _ := post(t, srv, "POST", "/api/queue/resume", `{}`); code != 200 || st.Paused() {
		t.Errorf("resume %d", code)
	}

	q, _ := st.Enqueue(jobs.Job{Library: "/m", Path: "/m/q.mkv"})
	if code, b := post(t, srv, "POST", fmt.Sprintf("/api/jobs/%d/stop", q), `{}`); code != 200 {
		t.Errorf("stop queued %d %s", code, b)
	}
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/skipped?reason=user_stopped"}); !strings.Contains(body, `"total":1`) || !strings.Contains(body, "You stopped it") {
		t.Errorf("skipped %s", body)
	}
	r, _ := st.Enqueue(jobs.Job{Library: "/m", Path: "/m/r.mkv"})
	st.Claim("w")
	running[r] = "slow" // a worker has it and has not ended it yet: "stopping", never "stopped"
	if code, b, resp := do(t, nil, srv, call{method: "POST", path: fmt.Sprintf("/api/jobs/%d/stop", r), body: `{}`, ctype: "application/json"}); code != 202 ||
		strings.TrimSpace(b) != `{"status":"stopping"}` || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Errorf("stop running, not ended yet: %d %q %q", code, b, resp.Header.Get("Content-Type"))
	}
	if j, err := st.Get(r); err != nil || j.Status != jobs.Running {
		t.Errorf("a running job's record must stay until its worker ends it: %+v %v", j, err)
	}
	// Only "slow" needs the short wait above. The end states below answer as soon as the worker ends the
	// job, so a long deadline costs nothing, and a slow runner's write (Windows CI) can't turn them into
	// "stopping" (flake: activity_test.go:398, runs 42454682163 and 42547029235).
	stopWait = 15 * time.Second
	running[r] = "stop" // the worker ends it as stopped: the answer is "stopped"
	if code, b := post(t, srv, "POST", fmt.Sprintf("/api/jobs/%d/stop", r), `{}`); code != 200 {
		t.Errorf("stop running %d %s", code, b)
	}
	f, _ := st.Enqueue(jobs.Job{Library: "/m", Path: "/m/f.mkv"})
	st.Claim("w")
	running[f] = "finish" // already being put in place: it ends done, and the answer says so
	if code, b := post(t, srv, "POST", fmt.Sprintf("/api/jobs/%d/stop", f), `{}`); code != 409 || !strings.Contains(b, "already_finished") {
		t.Errorf("stop a file that finishes: %d %s", code, b)
	}
	d, _ := st.Enqueue(jobs.Job{Library: "/m", Path: "/m/d.mkv"})
	st.Claim("w")
	st.Complete(d, "/m/d.mkv", 1)
	if code, b := post(t, srv, "POST", fmt.Sprintf("/api/jobs/%d/stop", d), `{}`); code != 409 || !strings.Contains(b, "not_stoppable") {
		t.Errorf("stop done %d %s", code, b)
	}
	if code, _ := post(t, srv, "POST", "/api/jobs/999/stop", `{}`); code != 404 {
		t.Errorf("stop missing %d", code)
	}
}

// DELETE /api/libraries/{id}: either the library is gone with no job of it running on, or everything is
// as it was (409). A conversion inside its swap finishes before the answer; an ordinary running one is put
// back in the queue and goes with the library; no scan or start reopens it meanwhile (reviews 1f54465,
// c7551fc, 5cdd27f).
func TestDeleteLibraryLeavesNothingHalfDone(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetPaused(false) // the test's worker is started (S2: a new install starts stopped)
	defer st.Close()
	a, _, _ := OpenAccess(t.TempDir(), false)
	a.FailDelay = 0
	var mu sync.Mutex
	inSwap := map[int64]bool{} // its worker cannot be stopped any more
	srv := httptest.NewServer(Handler(st, a, Engine{RequeueJob: func(id int64) {
		mu.Lock()
		defer mu.Unlock()
		if !inSwap[id] {
			st.Release(id) // as the worker does: back in the queue, original untouched
		}
	}}))
	defer srv.Close()
	defer func(d time.Duration) { libraryStopWait = d }(libraryStopWait)
	libraryStopWait = 2 * time.Second
	del := func(id int64) (int, string) { return post(t, srv, "DELETE", fmt.Sprintf("/api/libraries/%d", id), "") }
	gone := func(id int64, jobIDs ...int64) {
		t.Helper()
		if _, err := st.GetLibrary(id); err != jobs.ErrNotFound {
			t.Errorf("library %d still there: %v", id, err)
		}
		for _, j := range jobIDs {
			if got, err := st.Get(j); err == nil && got.Status != jobs.Done {
				t.Errorf("job %d left behind: %+v", j, got)
			}
		}
	}

	// 1. inside its swap: the answer comes after the job is done
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	j, _ := st.Enqueue(jobs.Job{Library: l.Path, LibraryID: l.ID, Path: l.Path + "/a.mkv"})
	st.Claim("w")
	mu.Lock()
	inSwap[j] = true
	mu.Unlock()
	done := make(chan time.Time, 1)
	go func() {
		time.Sleep(300 * time.Millisecond)
		// meanwhile a scan or Start cannot reopen it, and nothing of it is claimed
		if err := st.StartLibrary(l.ID); err != jobs.ErrLibraryBusy {
			t.Errorf("started during delete: %v", err)
		}
		if code, b := post(t, srv, "POST", fmt.Sprintf("/api/libraries/%d/scan", l.ID), "{}"); code != 409 || errCode(b) != "busy" {
			t.Errorf("scan during delete: %d %s", code, b)
		}
		if late, err := st.Enqueue(jobs.Job{Library: l.Path, LibraryID: l.ID, Path: l.Path + "/b.mkv"}); err == nil {
			if c, err := st.Claim("w2"); err == nil {
				t.Errorf("job %d (enqueued %d) was claimed during delete", c.ID, late)
			}
		}
		st.Complete(j, l.Path+"/a.mkv", 1)
		done <- time.Now()
	}()
	code, b := del(l.ID)
	answered := time.Now()
	if finished := <-done; code != 200 || answered.Before(finished) {
		t.Errorf("1: %d %s, answered %v before the swap finished", code, b, finished.Sub(answered))
	}
	gone(l.ID, j)
	if j2, _ := st.List(100); len(j2) != 1 {
		t.Errorf("1: jobs left: %+v", j2)
	}

	// 2. an ordinary running job: back in the queue, then removed with the library
	l2, _ := st.AddLibrary("Shows", t.TempDir(), rules.SliderTemplate(3), true)
	r2, _ := st.Enqueue(jobs.Job{Library: l2.Path, LibraryID: l2.ID, Path: l2.Path + "/c.mkv"})
	st.Claim("w")
	if code, b := del(l2.ID); code != 200 {
		t.Errorf("2: %d %s", code, b)
	}
	gone(l2.ID, r2)

	// 3. a swap that does not end in time: 409, the library intact and startable, its job still running
	l3, _ := st.AddLibrary("Music videos", t.TempDir(), rules.SliderTemplate(3), true)
	r3, _ := st.Enqueue(jobs.Job{Library: l3.Path, LibraryID: l3.ID, Path: l3.Path + "/d.mkv"})
	q3, _ := st.Enqueue(jobs.Job{Library: l3.Path, LibraryID: l3.ID, Path: l3.Path + "/e.mkv"})
	st.Claim("w")
	mu.Lock()
	inSwap[r3] = true
	mu.Unlock()
	libraryStopWait = 200 * time.Millisecond
	if code, b := del(l3.ID); code != 409 || errCode(b) != "busy" {
		t.Errorf("3: %d %s", code, b)
	}
	if got, err := st.GetLibrary(l3.ID); err != nil || !got.Started {
		t.Errorf("3: the library must stay as it was: %+v %v", got, err)
	}
	if err := st.StartLibrary(l3.ID); err != nil {
		t.Errorf("3: the removal must be over: %v", err)
	}
	if j, _ := st.Get(r3); j.Status != jobs.Running {
		t.Errorf("3: the job is still finishing: %s", j.Status)
	}
	if j, err := st.Claim("w3"); err != nil || j.ID != q3 {
		t.Errorf("3: its waiting job is claimable again: %+v %v", j, err)
	}
}

// still_changing's detail is design's sentence exactly (slice 23).
func TestStillChangingDetail(t *testing.T) {
	want := "This file changed since we last looked (it may still be downloading). We'll look at it again on the next scan."
	if got := upperFirst(rules.SkipWords[rules.SkipStillChanging]) + "."; got != want {
		t.Errorf("%q", got)
	}
}

// Replace mode with another file type (25a-2): the Done and Kept items say the converted file's new
// name (newPath) and the Done item is not "beside"; Convert again on a record of the old name, which
// is gone, is refused with design's sentence and changes nothing.
func TestRenamedByANewFileType(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	dir := t.TempDir()
	l, _ := st.AddLibrary("Movies", dir, rules.SliderTemplate(3), true)
	oldName, newName := filepath.Join(dir, "old.mp4"), filepath.Join(dir, "old.mkv")
	id := done(t, st, l.ID, oldName, newName, 1000, 400)
	oid, _ := st.BeginSwap(jobs.Original{JobID: id, LibraryID: l.ID, Path: oldName, NewPath: newName, Trash: filepath.Join(dir, ".syp-trash", "old.1.mp4"), Staged: "s", Bytes: 1000, Hash: "h", NewBytes: 400})
	st.FinishSwap(oid, time.Now().Add(7*24*time.Hour))

	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/done"})
	var d struct{ Items []map[string]any }
	json.Unmarshal([]byte(body), &d)
	if len(d.Items) != 1 || d.Items[0]["newPath"] != newName || d.Items[0]["originalState"] != "kept" || d.Items[0]["keptId"] != fmt.Sprint(oid) {
		t.Errorf("done %s", body)
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/activity/kept"})
	var k struct{ Items []map[string]any }
	json.Unmarshal([]byte(body), &k)
	if len(k.Items) != 1 || k.Items[0]["newPath"] != newName || k.Items[0]["path"] != oldName {
		t.Errorf("kept %s", body)
	}

	st.PutFile(jobs.File{Path: oldName, Size: 1000, Decision: "skip: x", Reason: rules.SkipUnreadable})
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/activity/skipped"})
	var s struct{ Items []map[string]any }
	json.Unmarshal([]byte(body), &s)
	if len(s.Items) != 1 {
		t.Fatalf("skipped %s", body)
	}
	code, b := post(t, srv, "POST", "/api/skipped/"+s.Items[0]["id"].(string)+"/convert-again", `{}`)
	if code != 409 || errCode(b) != "renamed" || !strings.Contains(b, "old.mp4 no longer exists: it was converted and is now old.mkv. Nothing was changed.") {
		t.Errorf("convert again: %d %s", code, b)
	}
	if f, _ := st.GetFile(oldName); f == nil {
		t.Error("the record was forgotten although nothing was to change")
	}
}

// DD-2: a job waiting to be tried again is listed under "retrying" on the queue (so it does not look
// stuck), still counted in upNext; a failed item says how many tries it had (attempts; 1 for one).
func TestRetryingAndAttempts(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{LibraryID: l.ID, Path: "/m/a.mkv"})
	st.Claim("w")
	at := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	st.RetryLater(id, at, "convert_failed", "The disk is full, so converting stopped.")

	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/queue"})
	var q struct {
		UpNext   []map[string]any
		Retrying []map[string]any
	}
	json.Unmarshal([]byte(body), &q)
	if len(q.Retrying) != 1 || q.Retrying[0]["jobId"] != fmt.Sprint(id) || q.Retrying[0]["retryAt"] != at.Format(time.RFC3339) ||
		q.Retrying[0]["attempts"] != 1.0 || q.Retrying[0]["detail"] != "The disk is full, so converting stopped." || q.Retrying[0]["library"] != "Movies" {
		t.Errorf("retrying: %s", body)
	}
	if len(q.UpNext) != 1 || q.UpNext[0]["count"] != 1.0 {
		t.Errorf("upNext: %s", body)
	}

	once, _ := st.Enqueue(jobs.Job{LibraryID: l.ID, Path: "/m/b.mkv"})
	st.Claim("w") // takes b (a waits an hour)
	st.Fail(once, "convert_failed", "Converting stopped with an error.")
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/activity/failed"})
	var f struct{ Items []map[string]any }
	json.Unmarshal([]byte(body), &f)
	if len(f.Items) != 1 || f.Items[0]["attempts"] != 1.0 {
		t.Errorf("failed: %s", body)
	}
	if _, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/activity/queue"}); !strings.Contains(body, `"retrying":[{`) {
		t.Errorf("a still waits: %s", body)
	}
}

// Workers (S2): this computer's worker, as agreed (proposal + 20:25Z amendments). Stopped: nothing is
// claimed (the scanner still queues). Stop after the current files lets them finish; stop now puts them
// back in the queue. Start clears the update notice; so does notice/dismiss. Files at once is parallelJobs (1-8).
func TestWorkersAPI(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, _, _ := OpenAccess(t.TempDir(), false)
	a.FailDelay = 0
	pausedNow := 0
	srv := httptest.NewServer(Handler(st, a, Engine{PauseNow: func() { pausedNow++ }}))
	defer srv.Close()
	get := func() (map[string]any, string) {
		_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/workers"})
		var out struct{ Workers []map[string]any }
		json.Unmarshal([]byte(body), &out)
		if len(out.Workers) != 1 {
			t.Fatalf("workers %s", body)
		}
		return out.Workers[0], body
	}
	summary := func() any {
		_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/summary"})
		var s map[string]any
		json.Unmarshal([]byte(body), &s)
		return s["workersRunning"]
	}
	// a new install: stopped, no notice, gpu null without the checks, quietUntil null
	w, body := get()
	if w["id"] != "this" || w["name"] != "This computer" || w["state"] != "stopped" || w["filesAtOnce"] != 1.0 || w["gpu"] != nil ||
		!strings.Contains(body, `"quietUntil":null`) || !strings.Contains(body, `"converting":[]`) ||
		!strings.Contains(body, `"stoppedByUpdate":false`) || !strings.Contains(body, `"basicFlowsChanged":false`) || summary() != false {
		t.Fatalf("new install: %s", body)
	}
	id, _ := st.Enqueue(jobs.Job{Path: "/m/a.mkv"})
	if _, err := st.Claim("w"); !errors.Is(err, jobs.ErrNoJob) {
		t.Fatalf("a job was claimed while the worker is stopped: %v", err)
	}
	// start: idle (running, waiting for files), the update notice cleared
	st.SetMeta("stopped_by_update", "1")
	if code, body := post(t, srv, "POST", "/api/workers/this/start", `{}`); code != 200 || !strings.Contains(body, `"state":"idle"`) {
		t.Fatalf("start: %d %s", code, body)
	}
	if st.StoppedByUpdate() || summary() != true {
		t.Error("start must clear the notice and say a worker runs")
	}
	st.Claim("w")
	st.Progress(id, 40)
	if w, _ := get(); w["state"] != "converting" {
		t.Errorf("with a file: %v", w["state"])
	}
	// stop after the current files: they finish (nothing put back), the worker is stopping
	if code, body := post(t, srv, "POST", "/api/workers/this/stop", `{"when":"after_current"}`); code != 200 || !strings.Contains(body, `"state":"stopping"`) ||
		!strings.Contains(body, `"jobId":"`+fmt.Sprint(id)+`","path":"/m/a.mkv","percent":40`) || pausedNow != 0 {
		t.Fatalf("stop after current: %d %s (put back %d)", code, body, pausedNow)
	}
	st.Complete(id, "/m/a.mkv", 1)
	if w, _ := get(); w["state"] != "stopped" {
		t.Errorf("after the file finished: %v", w["state"])
	}
	// stop now: the running files go back to the queue; stop with no "when" means after_current
	post(t, srv, "POST", "/api/workers/this/start", `{}`)
	if code, _ := post(t, srv, "POST", "/api/workers/this/stop", `{"when":"now"}`); code != 200 || pausedNow != 1 || !st.Paused() {
		t.Errorf("stop now: %d, put back %d", code, pausedNow)
	}
	post(t, srv, "POST", "/api/workers/this/start", `{}`)
	if code, body := post(t, srv, "POST", "/api/workers/this/stop", `{}`); code != 200 || !strings.Contains(body, `"state":"stopped"`) || pausedNow != 1 {
		t.Errorf("stop with nothing converting: %d %s", code, body)
	}
	// files at once (1-8)
	if code, body := post(t, srv, "PATCH", "/api/workers/this", `{"filesAtOnce":6}`); code != 200 || !strings.Contains(body, `"filesAtOnce":6`) {
		t.Errorf("files at once: %d %s", code, body)
	}
	if s, _ := LoadSettings(st); s.ParallelJobs != 6 {
		t.Errorf("parallelJobs %d", s.ParallelJobs)
	}
	for _, bad := range []string{`{"filesAtOnce":0}`, `{"filesAtOnce":9}`} {
		if code, body := post(t, srv, "PATCH", "/api/workers/this", bad); code != 400 || errCode(body) != "invalid_setting" || !strings.Contains(body, "from 1 to 8") {
			t.Errorf("%s: %d %s", bad, code, body)
		}
	}
	// Got it
	st.SetMeta("stopped_by_update", "1")
	if code, body := post(t, srv, "POST", "/api/workers/notice/dismiss", `{}`); code != 200 || st.StoppedByUpdate() || body != `{"stoppedByUpdate":false}`+"\n" {
		t.Errorf("dismiss: %d %s", code, body)
	}
	// refusals
	if code, body := post(t, srv, "POST", "/api/workers/other/start", `{}`); code != 404 || errCode(body) != "not_found" {
		t.Errorf("unknown worker: %d %s", code, body)
	}
	if code, body := post(t, srv, "POST", "/api/workers/this/stop", `{"when":"later"}`); code != 400 || errCode(body) != "invalid" {
		t.Errorf("bad when: %d %s", code, body)
	}
	// gpu: null when the hardware check has not run (no ffmpeg, or not finished yet: never waited for),
	// {found:false, vendor:null} once it ran and found none (reviews e1cd926; ui-shop's 1.5 s stop answer)
	slow := &media.FakeExec{Handler: func(name string, args []string) (string, string, error) {
		time.Sleep(300 * time.Millisecond) // every ffmpeg call of the checks is slow
		return "ffmpeg version 7.1", "", nil
	}}
	for _, c := range []struct {
		ffmpeg, want string
	}{{"", `"gpu":null`}, {"/usr/bin/ffmpeg", `"gpu":{"found":false,"vendor":null}`}} {
		chk := &doctor.Checker{Exec: slow, FFmpeg: c.ffmpeg, FFprobe: "/usr/bin/ffprobe"}
		s2 := httptest.NewServer(Handler(st, a, Engine{Doctor: chk}))
		start := time.Now()
		code, body := post(t, s2, "POST", "/api/workers/this/stop", `{"when":"after_current"}`)
		if took := time.Since(start); code != 200 || took > 200*time.Millisecond || !strings.Contains(body, `"gpu":null`) {
			t.Errorf("ffmpeg %q: stop answered in %s: %d %s", c.ffmpeg, took, code, body)
		}
		var body2 string
		for i := 0; i < 100; i++ { // the check finishes in the background
			_, body2, _ = do(t, nil, s2, call{method: "GET", path: "/api/workers"})
			if strings.Contains(body2, c.want) && (c.ffmpeg == "" || !strings.Contains(body2, `"gpu":null`)) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		s2.Close()
		if !strings.Contains(body2, c.want) {
			t.Errorf("ffmpeg %q: %s", c.ffmpeg, body2)
		}
	}
	// the old Resume starts this worker and clears the notice too (one release, until ui-shop switches)
	post(t, srv, "POST", "/api/workers/this/stop", `{}`)
	st.SetMeta("stopped_by_update", "1")
	if code, _ := post(t, srv, "POST", "/api/queue/resume", `{}`); code != 200 || st.Paused() || st.StoppedByUpdate() {
		t.Errorf("resume: %d paused %v notice %v", code, st.Paused(), st.StoppedByUpdate())
	}
}

// Several reasons as one list (contract 22:30Z): design's "Already in this format" asks for
// already_hevc and already_in_format together; they page as one list by path, with one total.
func TestSkippedTakesSeveralReasons(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	for p, reason := range map[string]string{"/m/a.mkv": "already_hevc", "/m/b.mkv": "already_in_format", "/m/c.mkv": "already_hevc",
		"/m/d.mkv": "already_in_format", "/m/e.mkv": "already_hevc", "/m/f.mkv": "no_video"} {
		st.PutFile(jobs.File{Path: p, Size: 1, ModTime: 1, Decision: "skip: x", Reason: reason})
	}
	list := func(q string) (paths []string, total int) {
		t.Helper()
		_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/skipped?" + q})
		var k struct {
			Items []map[string]any
			Total int
		}
		json.Unmarshal([]byte(body), &k)
		for _, i := range k.Items {
			paths = append(paths, filepath.Base(i["path"].(string)))
		}
		return paths, k.Total
	}
	for q, want := range map[string]string{
		"reason=already_hevc,already_in_format&limit=3":             "[a.mkv b.mkv c.mkv] 5",
		"reason=already_hevc,%20already_in_format&limit=3&offset=3": "[d.mkv e.mkv] 5", // across the page boundary
		"reason=already_in_format":                                  "[b.mkv d.mkv] 2", // one reason, as before
		"reason=already_hevc,nonsense":                              "[a.mkv c.mkv e.mkv] 3",
		"reason=nonsense":                                           "[] 0", // unknown codes match nothing
		"reason=,,":                                                 "[a.mkv b.mkv c.mkv d.mkv e.mkv f.mkv] 6",
		"":                                                          "[a.mkv b.mkv c.mkv d.mkv e.mkv f.mkv] 6",
	} {
		paths, total := list(q)
		if got := fmt.Sprint(paths, " ", total); got != want {
			t.Errorf("%q: %s, want %s", q, got, want)
		}
	}
}
