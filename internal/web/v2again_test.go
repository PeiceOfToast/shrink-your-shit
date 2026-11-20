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

// Convert again (design 11:45Z): a done job's detail says whether it can run again; POST queues it;
// a kept original that was deleted refuses it with the date.
func TestV2ConvertAgain(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	type again struct {
		Possible                      bool
		Policy, KeptUntil, OutputPath *string
		Reason                        *againReason
	}
	detail := func(id int64) again {
		t.Helper()
		_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10)})
		var d struct{ ConvertAgain again }
		json.Unmarshal([]byte(body), &d)
		return d.ConvertAgain
	}
	post := func(id int64) (int, string) {
		code, body, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10) + "/convert-again", body: `{}`, ctype: "application/json"})
		return code, body
	}
	// the original stayed where it was (output-first)
	src := filepath.Join(lib.Path, "a.mkv")
	os.WriteFile(src, []byte("original"), 0o644)
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: src})
	if a := detail(id); a.Possible || a.Reason == nil || a.Reason.Code != jobs.AgainNotDone || a.Policy != nil || a.OutputPath != nil {
		t.Errorf("waiting: %+v", a)
	}
	st.Claim("w")
	out := filepath.Join(lib.Path+" (shrunk)", "a.mkv")
	st.Complete(id, out, 4)
	// orch-lead 17:12Z: what the run did, for the button's words; a folder for new files names that file
	if a := detail(id); !a.Possible || a.Reason != nil || str(a.Policy) != "separate_folder" || str(a.OutputPath) != out || a.KeptUntil != nil {
		t.Errorf("done: %+v", a)
	}
	if code, body := post(id); code != 200 {
		t.Fatalf("convert again %d %s", code, body)
	}
	if j, _ := st.Get(id); j.Status != jobs.Queued || st.AgainFrom(id) != 0 {
		t.Errorf("queued from the original in place: %s %d", j.Status, st.AgainFrom(id))
	}
	if code, body := post(id); code != 409 || errCode(body) != jobs.AgainNotDone {
		t.Errorf("not done: %d %s", code, body)
	}
	// replaced, and its kept original deleted since
	b := filepath.Join(lib.Path, "b.mkv")
	os.WriteFile(b, []byte("new"), 0o644)
	other, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: b})
	st.Claim("w") // the first one, queued again
	if j, _ := st.Claim("w"); j.ID != other {
		t.Fatalf("claimed %d", j.ID)
	}
	row, _ := st.BeginSwap(jobs.Original{JobID: other, Path: b, Trash: filepath.Join(lib.Path, ".syp-trash", "b.2.mkv")})
	deleteOn := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	st.FinishSwap(row, deleteOn)
	st.Complete(other, b, 3)
	// replaced, its original kept: until when (the purge's date: kept plus the setting now, not the date
	// recorded when it was kept), and no other file (the new one has the file's own name)
	kept, _ := st.OriginalByID(row)
	purgeDay, _ := jobs.KeptUntil(kept.KeptAt, 7)
	if a := detail(other); str(a.Policy) != "replace" || str(a.KeptUntil) != purgeDay.UTC().Format(time.RFC3339) || a.OutputPath != nil {
		t.Errorf("kept: %+v", a)
	}
	st.SetSwapState(row, jobs.OriginalPurged)
	a := detail(other)
	at, _ := time.Parse(time.RFC3339, againDeletedAt(a.Reason))
	if a.Possible || a.Reason == nil || a.Reason.Code != jobs.AgainOriginalGone || time.Since(at) > time.Minute || // when it really went (#163 QA)
		str(a.Policy) != "replace" || a.KeptUntil != nil || a.OutputPath != nil {
		t.Errorf("purged: %+v", a)
	}
	if code, body := post(other); code != 409 || errCode(body) != jobs.AgainOriginalGone {
		t.Errorf("purged post: %d %s", code, body)
	}
	// beside: the new file next to its original
	c := filepath.Join(lib.Path, "c.mkv")
	os.WriteFile(c, []byte("original"), 0o644)
	third, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: c})
	if j, _ := st.Claim("w"); j.ID != third {
		t.Fatalf("claimed %d", j.ID)
	}
	next := filepath.Join(lib.Path, "c.shrunk.mkv")
	st.Complete(third, next, 3)
	if a := detail(third); !a.Possible || str(a.Policy) != "beside" || str(a.OutputPath) != next || a.KeptUntil != nil {
		t.Errorf("beside: %+v", a)
	}
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

type againReason struct {
	Code, Message string
	DeletedAt     *string
}

func againDeletedAt(r *againReason) string {
	if r == nil || r.DeletedAt == nil {
		return ""
	}
	return *r.DeletedAt
}

// A file converted with the library's current workflow is current: its detail refuses convert again with
// current_workflow. After the workflow changes, the older file is earlier and offered; the file converted
// after the change stays current, and a bulk POST queues only the older one, once.
func TestV2ConvertAgainCurrentWorkflow(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	convert := func(name string) int64 {
		t.Helper()
		p := filepath.Join(lib.Path, name)
		os.WriteFile(p, []byte("original"), 0o644)
		id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: p})
		if j, _ := st.Claim("w"); j.ID != id {
			t.Fatalf("claimed %d", j.ID)
		}
		if err := st.Complete(id, filepath.Join(lib.Path, "new-"+name), 4); err != nil {
			t.Fatal(err)
		}
		return id
	}
	libPath := "/api/v2/libraries/" + strconv.FormatInt(lib.ID, 10) + "/convert-again"
	type group struct {
		Code, Message string
		Count         int
	}
	type libAgain struct {
		Earlier, Possible int
		NotPossible       []group
	}
	libGET := func() libAgain {
		t.Helper()
		code, body, _ := do(t, nil, srv, call{method: "GET", path: libPath})
		var a libAgain
		if code != 200 {
			t.Fatalf("GET %d %s", code, body)
		}
		json.Unmarshal([]byte(body), &a)
		return a
	}

	old := convert("old.mkv")
	code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(old, 10)})
	var d struct {
		ConvertAgain struct {
			Possible bool
			Reason   *againReason
		}
	}
	json.Unmarshal([]byte(body), &d)
	if code != 200 || d.ConvertAgain.Possible || d.ConvertAgain.Reason == nil ||
		d.ConvertAgain.Reason.Code != jobs.AgainCurrent || d.ConvertAgain.Reason.Message != "Already converted with this workflow." ||
		d.ConvertAgain.Reason.DeletedAt != nil {
		t.Errorf("detail of a current file: %d %+v", code, d.ConvertAgain)
	}
	if code, body, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/jobs/" + strconv.FormatInt(old, 10) + "/convert-again", body: `{}`, ctype: "application/json"}); code != 409 || errCode(body) != jobs.AgainCurrent {
		t.Errorf("POST of a current file: %d %s", code, body)
	}
	if a := libGET(); a.Earlier != 0 || a.Possible != 0 || len(a.NotPossible) != 0 {
		t.Errorf("nothing earlier yet: %+v", a)
	}

	if _, err := st.SetFlowTemplate(lib.FlowID, rules.SliderTemplate(5)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureRevision(lib.FlowID); err != nil {
		t.Fatal(err)
	}
	if a := libGET(); a.Earlier != 1 || a.Possible != 1 || len(a.NotPossible) != 0 {
		t.Errorf("after a workflow change: %+v", a)
	}
	fresh := convert("fresh.mkv") // converted with the new workflow: current
	if a := libGET(); a.Earlier != 1 || a.Possible != 1 {
		t.Errorf("a file converted after the change is counted: %+v", a)
	}

	code, body, _ = do(t, nil, srv, call{method: "POST", path: libPath, body: `{}`, ctype: "application/json"})
	var q struct {
		Queued      int
		NotPossible []group
	}
	json.Unmarshal([]byte(body), &q)
	if code != 200 || q.Queued != 1 || len(q.NotPossible) != 0 {
		t.Fatalf("bulk POST: %d %s", code, body)
	}
	if j, _ := st.Get(old); j.Status != jobs.Queued {
		t.Errorf("the earlier file is %s, not queued", j.Status)
	}
	if j, _ := st.Get(fresh); j.Status != jobs.Done {
		t.Errorf("the current-workflow file was touched: %s", j.Status)
	}
	if code, body, _ := do(t, nil, srv, call{method: "POST", path: libPath, body: `{}`, ctype: "application/json"}); code != 200 || !strings.Contains(body, `"queued":0`) {
		t.Errorf("second bulk POST: %d %s", code, body)
	}
}
