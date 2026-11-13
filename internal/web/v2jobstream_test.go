// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

func streamServer(t *testing.T) (*httptest.Server, *jobs.Store, jobs.Library) {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetPaused(false)
	t.Cleanup(func() { st.Close() })
	a, _, _ := OpenAccess(t.TempDir(), false)
	srv := httptest.NewServer(Handler(st, a, Engine{}))
	t.Cleanup(srv.Close)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	return srv, st, lib
}

// streamAt opens the stream at path (with Last-Event-ID lastID unless empty); its messages come on the
// channel, which closes when the server ends the stream.
func streamAt(t *testing.T, url, path, lastID string) <-chan sseMessage {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", url+path, nil)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("%s: %d %s", path, res.StatusCode, res.Header.Get("Content-Type"))
	}
	out := make(chan sseMessage, 100)
	go func() {
		defer close(out)
		defer res.Body.Close()
		sc := bufio.NewScanner(res.Body)
		var m sseMessage
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "id: "):
				m.id = line[4:]
			case strings.HasPrefix(line, "event: "):
				m.event = line[7:]
			case strings.HasPrefix(line, "data: "):
				m.data = line[6:]
			case line == "" && m.event != "":
				out <- m
				m = sseMessage{}
			}
		}
	}()
	return out
}

// until reads messages until one of type event comes (and returns it), failing after 5 s.
func until(t *testing.T, c <-chan sseMessage, event string) sseMessage {
	t.Helper()
	for {
		select {
		case m, ok := <-c:
			if !ok {
				t.Fatalf("the stream ended before %s", event)
			}
			if m.event == event {
				return m
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no %s within 5 s", event)
		}
	}
}

// V3d: the feed lists what happened, newest first, in words; the live stream sends each new entry as
// activity, before the change it comes from.
func TestV2ActivityFeed(t *testing.T) {
	srv, st, lib := streamServer(t)
	add := func(name string) int64 {
		id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, name), SizeBefore: 2 << 30})
		return id
	}
	done, failed, skipped := add("Done.mkv"), add("Failed.mkv"), add("Skipped.mkv")
	st.Claim("w")
	st.Complete(done, filepath.Join(lib.Path, "Done.mkv"), 1<<30)
	st.Claim("w")
	st.Fail(failed, jobs.ReasonConvertFailed, "Converting did not finish.")
	st.Skip(skipped, "it is already HEVC in MKV", rules.SkipAlreadyInFormat)
	c := streamAt(t, srv.URL, "/api/v2/events", "")
	until(t, c, "hello")
	kept := add("Kept.mkv")
	st.Claim("w")
	st.Keep(kept, jobs.ReasonKeptOriginal, "the new file does not look like the original")
	a := until(t, c, "activity")
	if !strings.Contains(a.data, `"type":"job.failed"`) || !strings.Contains(a.data, `Kept the original of Kept.mkv: a check said no. The new file does not look like the original.`) || a.id != "" {
		t.Errorf("live entry %+v", a)
	}
	if m := <-c; m.event != "job.state" || !strings.Contains(m.data, `"to":"failed"`) || !strings.Contains(m.data, `"code":"kept_original"`) {
		t.Errorf("after the entry, its change: %+v", m)
	}

	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/activity?limit=3"})
	var feed struct {
		Items []jobs.Activity
		Total int
	}
	json.Unmarshal([]byte(body), &feed)
	var got []string
	for _, it := range feed.Items {
		got = append(got, it.Type+": "+it.Text)
	}
	want := []string{
		"job.failed: Kept the original of Kept.mkv: a check said no. The new file does not look like the original.",
		"job.skipped: Left Skipped.mkv as it is. It is already HEVC in MKV.",
		"job.failed: Failed.mkv could not be converted. Converting did not finish.",
	}
	if feed.Total != 4 || strings.Join(got, "|") != strings.Join(want, "|") || feed.Items[0].Refs["jobId"] != strconv.FormatInt(kept, 10) {
		t.Fatalf("feed %d %q", feed.Total, got)
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/activity?before=" + feed.Items[2].ID})
	if !strings.Contains(body, `"text":"Converted Done.mkv: 2.0 GB to 1.0 GB."`) || strings.Contains(body, "Failed.mkv") {
		t.Errorf("the next page %s", body)
	}
}

// Publishing a workflow is an event of its own (workflow.published) and a feed entry.
func TestV2PublishIsAnEvent(t *testing.T) {
	srv, st, lib := streamServer(t)
	st.EnsureRevisions() // the library's flow gets its revision 1, as at start-up
	c := streamAt(t, srv.URL, "/api/v2/events", "")
	until(t, c, "hello")
	d, _, err := st.Draft(lib.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := st.SaveDraft(lib.FlowID, d.Doc, d.Layout, d.Etag)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := st.GetFlow(lib.FlowID)
	if _, err := st.Publish(lib.FlowID, saved.Etag, f.Template); err != nil {
		t.Fatal(err)
	}
	if a := until(t, c, "activity"); !strings.Contains(a.data, `"type":"workflow.published"`) || !strings.Contains(a.data, `, version 2."`) {
		t.Errorf("entry %+v", a)
	}
	if m := <-c; m.event != "workflow.published" || m.id == "" || !strings.Contains(m.data, `"workflowId":"`+strconv.FormatInt(lib.FlowID, 10)+`"`) || !strings.Contains(m.data, `"version":2`) {
		t.Errorf("event %+v", m)
	}
}

// A job's log streams as it is written, its id the byte offset to go on from; once the job has ended and
// its log is all sent, end.
func TestV2JobLogStream(t *testing.T) {
	srv, st, lib := streamServer(t)
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv")})
	st.Claim("w")
	st.AppendLogLine(id, 0, "app", "probing") // the log lives in the database; its cursor is the line's seq
	path := "/api/v2/jobs/" + strconv.FormatInt(id, 10) + "/log/stream"
	c := streamAt(t, srv.URL, path, "")
	m := until(t, c, "job.log")
	if !strings.Contains(m.data, `probing\n"`) || m.id != "1" || !strings.Contains(m.data, `"next":1`) {
		t.Fatalf("first %+v", m)
	}
	st.AppendLogLine(id, 0, "app", "converting")
	if m := until(t, c, "job.log"); !strings.Contains(m.data, `converting\n"`) || m.id != "2" {
		t.Errorf("as it is written %+v", m)
	}
	// a reconnect from an offset gets only what came after it
	if m := until(t, streamAt(t, srv.URL, path, "1"), "job.log"); !strings.Contains(m.data, `converting\n"`) {
		t.Errorf("reconnect %+v", m)
	}
	st.Complete(id, filepath.Join(lib.Path, "a.mkv"), 1)
	until(t, c, "end")
	if _, ok := <-c; ok {
		t.Error("the stream did not close after end")
	}
	if code, _, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/999/log/stream"}); code != 404 {
		t.Errorf("no such job: %d", code)
	}
}

// One job's events: its changes (id: the seq, replayed after Last-Event-ID), new steps, its log, then end.
func TestV2JobEventStream(t *testing.T) {
	srv, st, lib := streamServer(t)
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv")})
	other, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "b.mkv")})
	path := "/api/v2/jobs/" + strconv.FormatInt(id, 10) + "/events"
	c := streamAt(t, srv.URL, path, "")
	hello := until(t, c, "hello")
	st.Claim("w") // takes id (the first waiting)
	running := until(t, c, "job.state")
	if !strings.Contains(running.data, `"to":"running"`) || !strings.Contains(running.data, `"jobId":"`+strconv.FormatInt(id, 10)+`"`) || running.id == "" {
		t.Fatalf("running %+v", running)
	}
	st.AddStep(id, "checked", true, "To be converted.")
	if m := until(t, c, "job.step"); !strings.Contains(m.data, `"words":"To be converted."`) {
		t.Errorf("step %+v", m)
	}
	st.AppendLogLine(id, 0, "app", "converting")
	if m := until(t, c, "job.log"); !strings.Contains(m.data, `converting\n"`) || m.id != "" {
		t.Errorf("log %+v", m)
	}
	st.Claim("w2") // the other job starts: not in this stream (the next job.state must be this job's)
	st.Fail(id, jobs.ReasonConvertFailed, "Converting did not finish.")
	failed := until(t, c, "job.state")
	if !strings.Contains(failed.data, `"reason":{"code":"convert_failed","message":"Converting did not finish."}`) {
		t.Errorf("failed %+v", failed)
	}
	until(t, c, "end")
	// the other job's changes are not in this stream; a reconnect after hello replays this job's
	r := streamAt(t, srv.URL, path, hello.id)
	until(t, r, "hello")
	if m := until(t, r, "job.state"); m.id != running.id {
		t.Errorf("replay %+v, want the running change %s", m, running.id)
	}
	if j, _ := st.Get(other); j.Status != jobs.Running {
		t.Errorf("the other job should be running (the check above relies on it): %s", j.Status)
	}
}

// Review 6f2b3a6 (a race seen under load): a job that ends just after a poll has read its changes still
// gets its last change on the stream before end.
func TestJobStreamSendsTheLastChangeBeforeEnd(t *testing.T) {
	srv, st, lib := streamServer(t)
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv")})
	st.Claim("w")
	var once sync.Once
	jobEventsRead = func() { // the job fails right after this poll read its changes
		once.Do(func() { st.Fail(id, jobs.ReasonConvertFailed, "Converting did not finish.") })
	}
	defer func() { jobEventsRead = nil }()
	c := streamAt(t, srv.URL, "/api/v2/jobs/"+strconv.FormatInt(id, 10)+"/events", "")
	until(t, c, "hello")
	if m := until(t, c, "job.state"); !strings.Contains(m.data, `"to":"failed"`) {
		t.Errorf("last change %+v", m)
	}
	until(t, c, "end")
}
