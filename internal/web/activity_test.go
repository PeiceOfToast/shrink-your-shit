// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	// F1: freed = originals gone (500-300); waiting = originals kept (1000-400); beside saves nothing
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/libraries"})
	if !strings.Contains(body, `"freedBytes":200`) || !strings.Contains(body, `"waitingBytes":600`) {
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
	st.Fail(f1, "ffmpeg stopped: invalid data")
	f2, _ := st.Enqueue(jobs.Job{Library: dir, LibraryID: l.ID, Path: dir + "/short.mkv"})
	st.Claim("t")
	st.Keep(f2, "the new file is 3 minutes shorter than the original")
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
	if body != `{"counts":{"failed":2,"skipped":3},"freedBytes":300,"paused":false,"waitingBytes":600,"waitingFiles":2,"workingFiles":1}`+"\n" {
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
	if _, err := st.DeleteLibrary(l.ID); err != nil {
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
