// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// sseMessage is one Server-Sent Events message.
type sseMessage struct{ id, event, data string }

// stream opens /api/v2/events (with Last-Event-ID lastID unless empty) and returns its messages as
// they come, until the test ends.
func stream(t *testing.T, url, lastID string) <-chan sseMessage {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", url+"/api/v2/events", nil)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("%d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	out := make(chan sseMessage, 100)
	go func() {
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

func nextMessage(t *testing.T, c <-chan sseMessage) sseMessage {
	t.Helper()
	select {
	case m := <-c:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no event within 5 s")
	}
	return sseMessage{}
}

// V3a: hello with the newest seq; a job's changes arrive as they happen, with the seq as id; a
// reconnect with Last-Event-ID replays what was missed; a pruned gap is resync.
func TestV2EventStream(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	st.SetPaused(false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	c := stream(t, srv.URL, "")
	hello := nextMessage(t, c)
	if hello.event != "hello" || !strings.Contains(hello.data, `"seq":`) {
		t.Fatalf("hello %+v", hello)
	}
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv")})
	created := nextMessage(t, c)
	if created.event != "job.state" || created.id == "" || !strings.Contains(created.data, `"from":null`) || !strings.Contains(created.data, `"to":"queued"`) {
		t.Fatalf("created %+v", created)
	}
	st.Claim("w")
	if m := nextMessage(t, c); !strings.Contains(m.data, `"to":"running"`) || !strings.Contains(m.data, `"workerId":"w"`) {
		t.Errorf("running %+v", m)
	}
	// a reconnect from the creation's id gets the running change again (replayed from the database)
	r := stream(t, srv.URL, created.id)
	if m := nextMessage(t, r); !strings.Contains(m.data, `"to":"running"`) {
		t.Errorf("replay %+v", m)
	}
	// a gap the pruning removed: resync
	st.Complete(id, filepath.Join(lib.Path, "a.mkv"), 1)
	st.PruneJobEvents(time.Now().Add(time.Hour))
	st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "b.mkv")})
	if m := nextMessage(t, stream(t, srv.URL, created.id)); m.event != "resync" {
		t.Errorf("pruned gap %+v", m)
	}
	// an id above the newest (the database was restored from a backup, or replaced): resync, not silence
	if m := nextMessage(t, stream(t, srv.URL, "999999")); m.event != "resync" {
		t.Errorf("id ahead of the newest %+v", m)
	}
}

// Hello and resync carry the newest seq as their id: a browser that drops right after hello and
// reconnects with that Last-Event-ID gets what happened meanwhile (ui-shop, cap-778a2f0).
func TestV2EventStreamHelloHasID(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	hello := nextMessage(t, stream(t, srv.URL, ""))
	if hello.event != "hello" || hello.id == "" || !strings.Contains(hello.data, `"seq":`+hello.id) {
		t.Fatalf("hello %+v", hello)
	}
	st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv")}) // while it was away
	if m := nextMessage(t, stream(t, srv.URL, hello.id)); m.event != "job.state" || !strings.Contains(m.data, `"to":"queued"`) {
		t.Errorf("reconnect after hello: %+v", m)
	}
	resync := nextMessage(t, stream(t, srv.URL, "999999"))
	if resync.event != "resync" || resync.id == "" || !strings.Contains(resync.data, `"seq":`+resync.id) {
		t.Errorf("resync %+v", resync)
	}
}

// The stream is behind the same sign-in as the rest of /api.
func TestV2EventStreamNeedsSignIn(t *testing.T) {
	srv, _, _, _ := libServer(t, true)
	if code, _, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/events"}); code != http.StatusUnauthorized {
		t.Errorf("signed out: %d", code)
	}
}

// V3b: a running job's progress comes as job.progress (no id: never replayed), with the step's own
// percent and the whole job's, and only for jobs a worker is converting.
func TestV2EventStreamProgress(t *testing.T) {
	defer func(d time.Duration) { eventProgress = d }(eventProgress)
	eventProgress = 50 * time.Millisecond
	srv, st, _, _ := libServer(t, false)
	st.SetPaused(false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv")})
	st.Claim("w")
	worker.SetLiveForTest(id, worker.LiveJob{Worker: "w", Started: time.Now(), Progress: media.Progress{Percent: 42, ETASeconds: 60, Speed: 1.5}})
	defer worker.DropLiveForTest(id)
	c := stream(t, srv.URL, "")
	nextMessage(t, c) // hello
	for {
		m := nextMessage(t, c)
		if m.event != "job.progress" {
			continue
		}
		if m.id != "" || !strings.Contains(m.data, `"state":"running"`) || !strings.Contains(m.data, `"percent":42`) ||
			!strings.Contains(m.data, `"workflowPercent":38`) || !strings.Contains(m.data, `"etaS":60`) {
			t.Errorf("progress %+v", m)
		}
		break
	}
}

// V3c: the summary in one call; the two pauses, each an event in the live stream.
func TestV2SummaryAndPauses(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	st.SetPaused(false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	done, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv"), SizeBefore: 1000})
	st.Claim("w")
	st.Complete(done, filepath.Join(lib.Path, "a.mkv"), 400)
	bad, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "b.mkv"), SizeBefore: 1000})
	st.Claim("w")
	st.Fail(bad, "convert_failed", "Converting did not finish")
	st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "c.mkv"), SizeBefore: 1000})
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/summary"})
	for _, want := range []string{`"completed":1`, `"failed":1`, `"successRate":0.5`, `"waiting":1`, `"failed24h":1`,
		`"savedBytes":600`, `"processingPaused":false`, `"intakePaused":false`, `"recentFailures":[{`} {
		if !strings.Contains(body, want) {
			t.Errorf("summary lacks %s: %s", want, body)
		}
	}
	c := stream(t, srv.URL, "")
	nextMessage(t, c) // hello
	if code, b := post(t, srv, "POST", "/api/v2/intake/pause", `{}`); code != 200 || !st.IntakePaused() {
		t.Fatalf("intake pause %d %s", code, b)
	}
	if code, b := post(t, srv, "POST", "/api/v2/processing/pause", `{}`); code != 200 || !st.Paused() {
		t.Fatalf("processing pause %d %s", code, b)
	}
	for _, want := range []string{"intake.paused", "processing.paused"} {
		m := nextMessage(t, c)
		if m.event != want || m.id == "" || !strings.Contains(m.data, want) {
			t.Errorf("want %s: %+v", want, m)
		}
	}
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/summary"}); !strings.Contains(body, `"processingPaused":true`) || !strings.Contains(body, `"intakePaused":true`) {
		t.Errorf("paused summary %s", body)
	}
}

// A failure's reason is {code, message}: the code a fixed one, the sentence beside it (cap-778a2f0).
func TestV2ReasonIsCodeAndMessage(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	st.SetPaused(false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv")})
	st.Claim("w")
	c := stream(t, srv.URL, "")
	nextMessage(t, c) // hello
	st.RetryLater(id, time.Now().Add(time.Hour), jobs.ReasonLowDiskSpace, "there is not enough free disk space next to this file")
	if m := nextMessage(t, c); !strings.Contains(m.data, `"reason":{"code":"low_disk_space","message":"there is not enough free disk space next to this file"}`) {
		t.Errorf("stream %+v", m)
	}
	code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10)})
	var d struct {
		Reason any
		Events []struct{ Reason any }
	}
	json.Unmarshal([]byte(body), &d)
	want := map[string]any{"code": "low_disk_space", "message": "there is not enough free disk space next to this file"}
	if code != 200 || !reflect.DeepEqual(d.Reason, want) || len(d.Events) == 0 || !reflect.DeepEqual(d.Events[len(d.Events)-1].Reason, want) {
		t.Errorf("job: reason and its history entry should both be %v: %d %s", want, code, body)
	}
	for _, e := range d.Events[:len(d.Events)-1] { // a change with no reason (created, running): null, never ""
		if e.Reason != nil {
			t.Errorf("history entry without a reason: %v", e.Reason)
		}
	}
}

// ui-shop 06:24Z: a library's change is an event with an id (a paused library's rows show "Library
// paused" at once, not on the next refresh); a reconnect replays it; a removed library says so.
func TestV2LibraryChangedEvent(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	libID := strconv.FormatInt(lib.ID, 10)
	c := stream(t, srv.URL, "")
	hello := nextMessage(t, c)
	if code, b, _ := do(t, nil, srv, call{method: "PATCH", path: "/api/v2/libraries/" + libID, body: `{"enabled":false}`, ctype: "application/json"}); code != 200 {
		t.Fatalf("pause %d %s", code, b)
	}
	m := nextMessage(t, c)
	if m.event != "library.changed" || m.id == "" || m.data != `{"libraryId":"`+libID+`","type":"library.changed"}` {
		t.Fatalf("paused %+v", m)
	}
	if r := nextMessage(t, stream(t, srv.URL, hello.id)); r != m { // replayed from the database
		t.Errorf("replay %+v, want %+v", r, m)
	}
	st.SetLibraryPriority(lib.ID, 5) // every change, by any route: here the store's own
	if m := nextMessage(t, c); m.event != "library.changed" {
		t.Errorf("priority %+v", m)
	}
	if code, b, _ := do(t, nil, srv, call{method: "DELETE", path: "/api/v2/libraries/" + libID, ctype: "application/json"}); code >= 300 {
		t.Fatalf("delete %d %s", code, b)
	}
	if m := nextMessage(t, c); m.event != "library.changed" || !strings.Contains(m.data, `"removed":true`) {
		t.Errorf("removed %+v", m)
	}
}
