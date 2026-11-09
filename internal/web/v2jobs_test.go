// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"path/filepath"
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
	st.Fail(failed, "Converting did not finish")
	retry := add("retry.mkv")
	st.Claim("w")
	st.RetryLater(retry, time.Now().Add(time.Hour), "busy")
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
	code, body = post(t, srv, "POST", "/api/v2/jobs/actions", `{"action":"cancel","ids":`+ids(retry, 999)+`}`)
	if code != 200 || strings.Count(body, `"ok":true`) != 1 || !strings.Contains(body, `"ok":false`) {
		t.Errorf("cancel %d %s", code, body)
	}
	if code, _ := post(t, srv, "POST", "/api/v2/jobs/actions", `{"action":"delete","ids":["1"]}`); code != 400 {
		t.Errorf("unknown action %d", code)
	}
}
