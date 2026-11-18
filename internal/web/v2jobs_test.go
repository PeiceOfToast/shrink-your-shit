// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// V2 (contract v2, Jobs): the queue by tab in spec 7's states, a job's detail with its history and the
// revision it ran, a removed job's history, and bulk actions with one result per job.
func TestV2Jobs(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	st.SetPaused(false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	add := func(name string) int64 {
		id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, name), SizeBefore: 1000})
		return id
	}
	done, failed := add("done.mkv"), add("failed.mkv")
	st.Claim("w")
	st.Complete(done, filepath.Join(lib.Path, "done.mkv"), 400)
	st.Claim("w")
	st.Fail(failed, "convert_failed", "Converting did not finish")
	retry := add("retry.mkv")
	st.Claim("w")
	st.RetryLater(retry, time.Now().Add(time.Hour), "convert_failed", "busy")
	waiting := add("waiting.mkv")
	skipped := add("skipped.mkv")
	st.Skip(skipped, "it is already HEVC in MKV", rules.SkipAlreadyInFormat)

	list := func(tab string) v2List {
		t.Helper()
		_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=" + tab})
		return decode[v2List](t, body)
	}
	w := list("waiting")
	states := map[string]string{}
	for _, it := range w.Items {
		states[it["name"].(string)] = it["state"].(string) + "/" + it["subState"].(string)
	}
	if w.Total != 2 || states["retry.mkv"] != "retry_wait/retrying" || states["waiting.mkv"] != "queued/waiting" {
		t.Errorf("waiting %v %d", states, w.Total)
	}
	if c := list("completed"); c.Total != 1 || c.Items[0]["state"] != "completed" {
		t.Errorf("completed %+v", c.Items)
	}
	if f := list("failed"); f.Total != 1 || f.Items[0]["state"] != "failed" {
		t.Errorf("failed %+v", f.Items)
	}
	if s := list("skipped"); s.Total != 1 || s.Items[0]["state"] != "skipped" || s.Items[0]["origin"] != "scan" {
		t.Errorf("skipped %+v", s.Items)
	}
	// every item's reason is {code, message} | null, as every v2 reason (ui-shop 23:27Z)
	want := map[string]any{
		"failed.mkv":  map[string]any{"code": "ffmpeg_error", "message": "ffmpeg stopped with an error."},
		"retry.mkv":   map[string]any{"code": "ffmpeg_error", "message": "busy"},
		"skipped.mkv": map[string]any{"code": rules.SkipAlreadyInFormat, "message": "It is already HEVC in MKV."},
		"waiting.mkv": nil, "done.mkv": nil,
	}
	for _, tab := range []string{"waiting", "completed", "failed", "skipped"} {
		for _, it := range list(tab).Items {
			if r, ok := it["reason"]; !ok || !reflect.DeepEqual(r, want[it["name"].(string)]) {
				t.Errorf("%s: reason %#v, want %#v", it["name"], r, want[it["name"].(string)])
			}
		}
	}
	if code, _, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=everything"}); code != 400 {
		t.Errorf("bad tab %d", code)
	}

	// detail: history and the revision it ran
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(done, 10)})
	if !strings.Contains(body, `"state":"completed"`) || !strings.Contains(body, `"to":"running"`) || !strings.Contains(body, `"version":1`) {
		t.Errorf("detail %s", body)
	}
	// a skipped job's row is gone (as before V2) but its history isn't
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(skipped, 10)})
	if !strings.Contains(body, `"removed":true`) || !strings.Contains(body, `"state":"skipped"`) || !strings.Contains(body, rules.SkipAlreadyInFormat) {
		t.Errorf("removed %s", body)
	}

	// bulk actions: one result each; cancel never deletes a file
	ids := func(ns ...int64) string {
		var s []string
		for _, n := range ns {
			s = append(s, `"`+strconv.FormatInt(n, 10)+`"`)
		}
		return "[" + strings.Join(s, ",") + "]"
	}
	code, body := post(t, srv, "POST", "/api/v2/jobs/actions", `{"action":"reprioritize","ids":`+ids(waiting, done)+`,"priority":5}`)
	if code != 200 || !strings.Contains(body, `"ok":true`) || !strings.Contains(body, `"not_waiting"`) {
		t.Errorf("reprioritize %d %s", code, body)
	}
	if j, _ := st.Claim("w2"); j.ID != waiting {
		t.Errorf("the higher priority job is taken first: got %d, want %d", j.ID, waiting)
	}
	code, body = post(t, srv, "POST", "/api/v2/jobs/actions", `{"action":"retry","ids":`+ids(failed)+`}`)
	if code != 200 || !strings.Contains(body, `"ok":true`) {
		t.Errorf("retry %d %s", code, body)
	}
	// a retry wait goes now; a plain waiting job is already waiting (review 66806b3)
	code, body = post(t, srv, "POST", "/api/v2/jobs/actions", `{"action":"retry","ids":`+ids(retry)+`}`)
	if j, _ := st.Get(retry); code != 200 || !strings.Contains(body, `"ok":true`) || j.RetryAt != "" {
		t.Errorf("retry a retry wait: %d %s %q", code, body, j.RetryAt)
	}
	st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "plain.mkv")})
	plain, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "plain2.mkv")})
	if code, body := post(t, srv, "POST", "/api/v2/jobs/actions", `{"action":"retry","ids":`+ids(plain)+`}`); code != 200 || !strings.Contains(body, `"already_waiting"`) {
		t.Errorf("retry a waiting job: %d %s", code, body)
	}
	code, body = post(t, srv, "POST", "/api/v2/jobs/actions", `{"action":"cancel","ids":`+ids(retry, 999)+`}`)
	if code != 200 || strings.Count(body, `"ok":true`) != 1 || !strings.Contains(body, `"ok":false`) {
		t.Errorf("cancel %d %s", code, body)
	}
	if code, _ := post(t, srv, "POST", "/api/v2/jobs/actions", `{"action":"delete","ids":["1"]}`); code != 400 {
		t.Errorf("unknown action %d", code)
	}
}

// Lead 22:23Z: a job that waited across a publish shows the revision it ran with (the one published
// when it started), not the one at queue time. Steps a second apart: times are kept to the second.
func TestV2JobRevisionIsWhenItStarted(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	st.SetPaused(false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	if _, err := st.EnsureRevision(lib.FlowID); err != nil { // version 1, before the job
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv"), SizeBefore: 1000})
	time.Sleep(1100 * time.Millisecond)
	if _, err := st.SetFlowTemplate(lib.FlowID, rules.SliderTemplate(4)); err != nil { // version 2 while it waits
		t.Fatal(err)
	}
	path := "/api/v2/jobs/" + strconv.FormatInt(id, 10)
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: path}); !strings.Contains(body, `"version":2`) {
		t.Errorf("waiting: it runs the newest published when it starts: %s", body) // lead 22:41Z
	}
	time.Sleep(1100 * time.Millisecond)
	st.Claim("w")
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: path}); !strings.Contains(body, `"version":2`) {
		t.Errorf("started after version 2: %s", body)
	}
	time.Sleep(1100 * time.Millisecond)
	st.SetFlowTemplate(lib.FlowID, rules.SliderTemplate(5)) // version 3 while it runs: it ran with 2
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: path}); !strings.Contains(body, `"version":2`) {
		t.Errorf("running, version 3 published since: %s", body)
	}
}

// The Waiting tab in the order workers take files (sort=queue): each job's position is its place in
// Claim's order over the whole queue (also in a filtered list); one waiting to retry has none and comes
// last. The positions are what Claim then actually takes.
func TestV2JobsQueueOrder(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	movies, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	tv, _ := st.AddLibrary("TV", t.TempDir(), rules.SliderTemplate(3), true)
	add := func(l jobs.Library, name string) int64 {
		id, _ := st.Enqueue(jobs.Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, name)})
		return id
	}
	retry := add(movies, "retry.mkv")
	st.Claim("w")
	st.RetryLater(retry, time.Now().Add(time.Hour), jobs.ReasonLowDiskSpace, "not enough free disk space")
	a, b, c := add(movies, "a.mkv"), add(tv, "b.mkv"), add(movies, "c.mkv")
	st.SetPriority(c, 5) // moved to the top

	type item struct {
		Name     string
		Position *int
		Priority *int
	}
	list := func(query string) []item {
		t.Helper()
		code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=waiting&sort=queue" + query})
		if code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		var l struct{ Items []item }
		json.Unmarshal([]byte(body), &l)
		return l.Items
	}
	str := func(items []item) string {
		var out []string
		for _, it := range items {
			p := "null"
			if it.Position != nil {
				p = strconv.Itoa(*it.Position)
			}
			out = append(out, it.Name+":"+p)
		}
		return strings.Join(out, " ")
	}
	all := list("")
	if got := str(all); got != "c.mkv:1 a.mkv:2 b.mkv:3 retry.mkv:null" {
		t.Errorf("queue order %q", got)
	}
	for _, it := range all { // each row has its priority (ui-shop's First/Last chip)
		if want := map[string]int{"c.mkv": 5}[it.Name]; it.Priority == nil || *it.Priority != want {
			t.Errorf("%s: priority %v, want %d", it.Name, it.Priority, want)
		}
	}
	if got := str(list("&libraryId=" + strconv.FormatInt(tv.ID, 10))); got != "b.mkv:3" { // its place in the whole queue
		t.Errorf("filtered %q", got)
	}
	if got := str(list("&limit=1&offset=1")); got != "a.mkv:2" {
		t.Errorf("second page %q", got)
	}
	for _, want := range []int64{c, a, b} { // what Claim takes, one by one
		if j, err := st.Claim("w"); err != nil || j.ID != want {
			t.Fatalf("claimed %d %v, want %d", j.ID, err, want)
		}
	}
	if _, err := st.Claim("w"); err == nil {
		t.Error("the job waiting to retry was taken")
	}
	for _, bad := range []string{"/api/v2/jobs?tab=failed&sort=queue", "/api/v2/jobs?tab=waiting&sort=size"} {
		if code, _, _ := do(t, nil, srv, call{method: "GET", path: bad}); code != 400 {
			t.Errorf("%s: %d", bad, code)
		}
	}
}

// Design 03:22Z: a job the person put First or Last keeps that place over any library priority; the
// queue's positions are what Claim takes; "none" puts it back in its library's order.
func TestV2JobsPinsOverLibraryPriority(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	a, _ := st.AddLibrary("A", t.TempDir(), rules.SliderTemplate(3), true)
	b, _ := st.AddLibrary("B", t.TempDir(), rules.SliderTemplate(3), true)
	add := func(l jobs.Library, name string) int64 {
		id, _ := st.Enqueue(jobs.Job{Library: l.Path, LibraryID: l.ID, Path: filepath.Join(l.Path, name)})
		return id
	}
	a1, a2, b1 := add(a, "a1.mkv"), add(a, "a2.mkv"), add(b, "b1.mkv")
	act := func(body string) (int, string) {
		code, out, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/jobs/actions", body: body, ctype: "application/json"})
		return code, out
	}
	if code, out := act(`{"action":"reprioritize","ids":["` + strconv.FormatInt(a2, 10) + `"],"pin":"first"}`); code != 200 || strings.Contains(out, `"ok":false`) {
		t.Fatalf("first %d %s", code, out)
	}
	act(`{"action":"reprioritize","ids":["` + strconv.FormatInt(a1, 10) + `"],"pin":"last"}`)
	for _, lp := range []struct {
		id int64
		p  string
	}{{a.ID, "50"}, {b.ID, "20"}} { // the pinned jobs' own library changes too: their pins stay
		if code, out, _ := do(t, nil, srv, call{method: "PATCH", path: "/api/v2/libraries/" + strconv.FormatInt(lp.id, 10), body: `{"priority":` + lp.p + `}`, ctype: "application/json"}); code != 200 {
			t.Fatalf("library priority %d %s", code, out)
		}
	}
	type item struct {
		Name     string
		Position *int
		Pin      *string
		Priority *int
	}
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=waiting&sort=queue"})
	var l struct{ Items []item }
	json.Unmarshal([]byte(body), &l)
	var got []string
	for _, it := range l.Items {
		pin := "-"
		if it.Pin != nil {
			pin = *it.Pin
		}
		got = append(got, fmt.Sprintf("%s:%d:%s:%d", it.Name, *it.Position, pin, *it.Priority))
	}
	if strings.Join(got, " ") != "a2.mkv:1:first:50 b1.mkv:2:-:20 a1.mkv:3:last:50" {
		t.Errorf("queue %q", got)
	}
	for _, want := range []int64{a2, b1, a1} { // what Claim takes
		if j, err := st.Claim("w"); err != nil || j.ID != want {
			t.Fatalf("claimed %d %v, want %d", j.ID, err, want)
		}
	}
	// back in its library's order
	c := add(a, "c.mkv")
	act(`{"action":"reprioritize","ids":["` + strconv.FormatInt(c, 10) + `"],"pin":"first"}`)
	act(`{"action":"reprioritize","ids":["` + strconv.FormatInt(c, 10) + `"],"pin":"none"}`)
	if _, body := act(`{"action":"reprioritize","ids":["` + strconv.FormatInt(c, 10) + `"],"pin":"sideways"}`); !strings.Contains(body, "invalid") {
		t.Errorf("a bad pin: %s", body)
	}
	if code, _ := act(`{"action":"reprioritize","ids":["` + strconv.FormatInt(c, 10) + `"],"priority":5000}`); code != 400 {
		t.Errorf("a priority in the pins' range: %d", code)
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=waiting&sort=queue"})
	if !strings.Contains(body, `"pin":null`) {
		t.Errorf("unpinned %s", body)
	}
}

// User review 11-13 item E: the Done tab's count and its list agree (the preview showed counts.completed 4 with
// items []): four completed jobs are four items under tab=completed.
func TestCompletedTabListsWhatItCounts(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	for _, p := range []string{"/m/a.mkv", "/m/b.mkv", "/m/c.mkv", "/m/d.mkv"} {
		done(t, st, l.ID, p, p, 1000, 400)
	}
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=completed&limit=200"})
	var r struct {
		Items  []map[string]any
		Total  int
		Counts map[string]int
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	if r.Counts["completed"] != 4 || r.Total != 4 || len(r.Items) != 4 {
		t.Errorf("completed: counts %v total %d items %d\n%s", r.Counts, r.Total, len(r.Items), body)
	}
}

// User review 11-13 item E: a failed job's list item and detail carry its failure summary (step, exit code, the
// last five error lines) and its log state; a job with no summary has failure null.
func TestFailureSummaryInTheAPI(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	f1, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/broken.mkv"})
	st.Claim("t")
	st.RecordRunStep(f1, jobs.RunStep{Index: 0, Name: "encode", Outcome: "failed", Command: "ffmpeg -i x"})
	st.AppendLogLine(f1, 0, "stderr", "Invalid data found when processing input")
	st.SetFailureSummary(f1, jobs.FailureSummary{Step: 0, StepCount: 1, AfterMs: 4000, MediaTime: "00:41:12", ExitCode: 1,
		KeyLines: []string{"a", "b", "c", "d", "e", "f", "Invalid data found when processing input"}})
	st.Fail(f1, "convert_failed", "Converting stopped with an error.")
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=failed&limit=200"})
	var r struct{ Items []map[string]any }
	json.Unmarshal([]byte(body), &r)
	if len(r.Items) != 1 {
		t.Fatalf("failed tab: %s", body)
	}
	f := r.Items[0]["failure"].(map[string]any)
	if f["step"] != "encode" || f["exitCode"] != 1.0 || f["mediaTime"] != "00:41:12" || r.Items[0]["logState"] != "complete" ||
		f["hint"] != "Play the file around that point to check it. Retry won't help with the same file." ||
		r.Items[0]["reason"].(map[string]any)["message"] != "The source file is damaged at 00:41:12." {
		t.Errorf("failure %v logState %v", f, r.Items[0]["logState"])
	}
	if lines := f["keyLines"].([]any); len(lines) != 5 || lines[4] != "Invalid data found when processing input" {
		t.Errorf("key lines %v", lines)
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(f1, 10)})
	var d map[string]any
	json.Unmarshal([]byte(body), &d)
	if d["failure"] == nil || d["logState"] != "complete" {
		t.Errorf("detail: %s", body)
	}
}

// User review 11-13 item E: the job's log endpoint gives its steps (command, timing, the failed step's output) and
// the download gives every line as text.
func TestJobLogEndpoint(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/a.mkv"})
	st.Claim("t")
	st.AppendLogLine(id, 0, "app", "started")
	st.RecordRunStep(id, jobs.RunStep{Index: 0, Name: "encode", StartedAt: "t0", TookMs: 1500, Outcome: "failed",
		Command: "ffmpeg -i in.mkv out.mkv", Probe: `{"codec":"h264"}`, OutputLines: 1})
	st.AppendLogLine(id, 0, "stderr", "Invalid data found when processing input")
	st.SetFailureSummary(id, jobs.FailureSummary{Step: 0, StepCount: 1, ExitCode: 1, KeyLines: []string{"Invalid data found when processing input"}})
	st.Fail(id, "convert_failed", "Converting stopped with an error.")
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10) + "/log"})
	var r struct {
		State string
		Final string
		Steps []map[string]any
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	if r.State != "complete" || r.Final != "failed" || len(r.Steps) != 1 || r.Steps[0]["command"] != "ffmpeg -i in.mkv out.mkv" ||
		r.Steps[0]["tookMs"] != 1500.0 || r.Steps[0]["probe"] == nil {
		t.Errorf("log %s", body)
	}
	if out := r.Steps[0]["output"].([]any); len(out) != 1 || out[0] != "Invalid data found when processing input" {
		t.Errorf("failed step output %v", out)
	}
	_, text, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10) + "/log/download"})
	if !strings.Contains(text, "started") || !strings.Contains(text, "Invalid data found") {
		t.Errorf("download: %s", text)
	}
}

// User review 11-13 item E: ?path= lists one file's jobs on a tab (exact path, the job's own file).
func TestJobsByPath(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	done(t, st, l.ID, "/m/a.mkv", "/m/a.mkv", 1000, 400)
	done(t, st, l.ID, "/m/a.mkv.bak", "/m/a.mkv.bak", 1000, 400)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=completed&path=/m/a.mkv"})
	var r struct{ Items []map[string]any }
	json.Unmarshal([]byte(body), &r)
	if len(r.Items) != 1 || r.Items[0]["name"] != "a.mkv" {
		t.Errorf("by path: %s", body)
	}
}

// #214 P2: a job whose ffmpeg never ran says so (ffmpegRan false), with exitCode null and no key lines.
func TestFailureNeverRanInTheAPI(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/never.mkv"})
	st.Claim("t")
	st.SetFailureSummary(id, jobs.FailureSummary{Step: 0, StepCount: 1, ExitCode: -1, FfmpegRan: false})
	st.Fail(id, "convert_failed", "Converting did not finish.")
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10)})
	var d struct{ Failure map[string]any }
	json.Unmarshal([]byte(body), &d)
	if d.Failure["ffmpegRan"] != false || d.Failure["exitCode"] != nil || len(d.Failure["keyLines"].([]any)) != 0 {
		t.Errorf("never ran: %v", d.Failure)
	}
}

// User review 11-13 item E: a running job's log is still coming back; a worker-lost job with no lines is lost.
func TestLogStates(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	run, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/run.mkv"})
	st.Claim("t")
	lost, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/lost.mkv"})
	st.Claim("t")
	st.Fail(lost, "worker_lost", "The worker stopped answering.")
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=running&limit=200"})
	var r struct{ Items []map[string]any }
	json.Unmarshal([]byte(body), &r)
	if len(r.Items) != 1 || r.Items[0]["logState"] != "coming_back" {
		t.Errorf("running: %s", body)
	}
	_ = run
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(lost, 10)})
	var d map[string]any
	json.Unmarshal([]byte(body), &d)
	if d["logState"] != "lost" {
		t.Errorf("worker lost: %s", body)
	}
}

// User review 11-13 item E: ?reason= lists the failures with one design code; an unknown code is refused.
func TestJobsByReason(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	lost, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/lost.mkv"})
	st.Claim("t")
	st.Fail(lost, "worker_lost", "The worker stopped answering.")
	other, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/other.mkv"})
	st.Claim("t")
	st.Fail(other, "convert_failed", "Converting did not finish.")
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs?tab=failed&reason=worker_lost&limit=200"})
	var r struct{ Items []map[string]any }
	json.Unmarshal([]byte(body), &r)
	if len(r.Items) != 1 || r.Items[0]["name"] != "lost.mkv" {
		t.Errorf("worker_lost: %s", body)
	}
	if code, _ := post(t, srv, "GET", "/api/v2/jobs?tab=failed&reason=nonsense", ""); code != 400 {
		t.Errorf("unknown reason: %d", code)
	}
}

// A job whose log was imported from an old .log file has lines but no steps: /log shows them as one "imported"
// step (the last 200 lines; outputLines counts all), not an empty log (design's preview check 11-13).
func TestJobLogImportedLinesShowAsOneStep(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/a.mkv"})
	st.Claim("t")
	for i := 0; i < 250; i++ {
		st.AppendLogLine(id, 0, "app", "line "+strconv.Itoa(i))
	}
	st.Complete(id, "/m/a.hevc.mkv", 100)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10) + "/log"})
	var r struct {
		State string
		Steps []struct {
			Name        string
			Outcome     *string
			TookMs      *float64
			Command     *string
			OutputLines int
			Output      []string
		}
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	if r.State != "complete" || len(r.Steps) != 1 {
		t.Fatalf("log %s", body)
	}
	s := r.Steps[0]
	if s.Name != "imported" || s.TookMs != nil || s.Command != nil || s.OutputLines != 250 || len(s.Output) != 200 ||
		s.Output[0] != "line 50" || s.Output[199] != "line 249" || s.Outcome == nil || *s.Outcome != "done" {
		t.Errorf("imported step %+v", s)
	}

	// a job with no lines and no steps has no log at all: state none, no steps
	id2, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/b.mkv"})
	st.Claim("t")
	st.Complete(id2, "/m/b.hevc.mkv", 100)
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id2, 10) + "/log"})
	r.Steps = nil
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	if r.State != "none" || len(r.Steps) != 0 {
		t.Errorf("no log: %s", body)
	}
}
