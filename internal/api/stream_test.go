// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
)

type sseFrame struct{ id, event, data string }

// openStream connects to the stream at path with the fixture's session. lastID, when set, is sent as Last-Event-ID.
// The frames arrive on the returned channel until the body ends; closing the returned body ends the stream.
func openStream(t *testing.T, ts *httptest.Server, cookie string, path, lastID string) (<-chan sseFrame, io.Closer) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Accept", "text/event-stream")
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("connect %s: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("connect %s: %d", path, resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type = %q, want text/event-stream", ct)
	}
	out := make(chan sseFrame, 256)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(resp.Body)
		var f sseFrame
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if f.event != "" {
					out <- f
				}
				f = sseFrame{}
			case strings.HasPrefix(line, "id: "):
				f.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				f.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				f.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return out, resp.Body
}

// nextFrame waits for one frame, failing the test after a few seconds.
func nextFrame(t *testing.T, frames <-chan sseFrame, what string) sseFrame {
	t.Helper()
	select {
	case f, ok := <-frames:
		if !ok {
			t.Fatalf("%s: the stream ended", what)
		}
		return f
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: no frame in 5 s", what)
	}
	return sseFrame{}
}

// streamServer serves the fixture over real HTTP. The stream's own stop runs before the server closes, since an
// open stream never ends on its own.
func streamServer(t *testing.T, f *jobsFixture) *httptest.Server {
	ts := httptest.NewServer(f.e.srv)
	t.Cleanup(ts.Close)
	t.Cleanup(f.e.srv.CloseStreams)
	return ts
}

func TestStreamReplaysAfterLastEventIDWithRowIDs(t *testing.T) {
	f := newJobsFixture(t)
	ts := streamServer(t, f)
	cookie := f.cookie.String()

	// Wait for the fixture's events to commit, then read the whole log from the start.
	frames, body := openStream(t, ts, cookie, "/api/v2/events", "0")
	defer body.Close()
	var all []sseFrame
	for len(all) < 3 {
		all = append(all, nextFrame(t, frames, "replay from 0"))
	}
	var prev int64
	for i, fr := range all {
		id, err := strconv.ParseInt(fr.id, 10, 64)
		if err != nil || id <= prev {
			t.Fatalf("frame %d id %q: want a row id after %d", i, fr.id, prev)
		}
		prev = id
		if fr.event == "" || !strings.Contains(fr.data, `"seq"`) {
			t.Errorf("frame %d = %+v; want an event name and a log line", i, fr)
		}
	}

	// Resume after the first frame: the next frames are the second and third, with no gap and no repeat.
	resumed, body2 := openStream(t, ts, cookie, "/api/v2/events", all[0].id)
	defer body2.Close()
	got := nextFrame(t, resumed, "resume")
	if got.id != all[1].id {
		t.Errorf("resume after %s starts at %s; want %s (no gap, no repeat)", all[0].id, got.id, all[1].id)
	}
	if got2 := nextFrame(t, resumed, "resume 2"); got2.id != all[2].id {
		t.Errorf("second resumed frame = %s; want %s", got2.id, all[2].id)
	}
}

func TestStreamSendsLiveEventsForOneJob(t *testing.T) {
	f := newJobsFixture(t)
	ts := streamServer(t, f)
	// A new stream starts after the newest committed row, so wait for the fixture's queue lines to commit first.
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if rec := f.get(fmt.Sprintf("/api/v2/jobs/%d/events", f.waiting)); strings.Contains(rec.Body.String(), `"queued"`) {
			break
		}
	}
	frames, body := openStream(t, ts, f.cookie.String(), fmt.Sprintf("/api/v2/events?job=%d", f.waiting), "")
	defer body.Close()

	// No replay without Last-Event-ID: the first frame is the one emitted now.
	if err := f.e.bus.Emit(events.Event{JobID: f.waiting, Kind: events.KindNote, Level: events.LevelInfo, Source: "test", Message: "live line"}); err != nil {
		t.Fatal(err)
	}
	fr := nextFrame(t, frames, "live event")
	if !strings.Contains(fr.data, "live line") || fr.event != "note" {
		t.Errorf("live frame = %+v; want the note with its text", fr)
	}
	if fr.id == "" {
		t.Error("a job frame has no id line")
	}
}

func TestStreamPingCarriesNoID(t *testing.T) {
	f := newJobsFixture(t)
	f.e.srv.pingEvery = 20 * time.Millisecond
	ts := streamServer(t, f)
	frames, body := openStream(t, ts, f.cookie.String(), fmt.Sprintf("/api/v2/events?job=%d", f.waiting), "")
	defer body.Close()
	for {
		fr := nextFrame(t, frames, "ping")
		if fr.event != "ping" {
			continue
		}
		if fr.id != "" || fr.data != "{}" {
			t.Errorf("ping = %+v; want no id and data {}", fr)
		}
		return
	}
}

func TestStreamEndsWhenTheClientGoes(t *testing.T) {
	f := newJobsFixture(t)
	f.e.srv.pingEvery = 20 * time.Millisecond
	ts := streamServer(t, f)
	base := runtime.NumGoroutine()
	frames, body := openStream(t, ts, f.cookie.String(), fmt.Sprintf("/api/v2/events?job=%d", f.waiting), "")
	nextFrame(t, frames, "first ping")
	body.Close()
	// The stream's goroutines wind down once the client is gone. Allow a small margin for the test server's own.
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > base+2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base+2 {
		t.Errorf("goroutines = %d after the client left; started at %d", n, base)
	}
}

func TestStreamRefusesWithoutSignInAndUnknownJobs(t *testing.T) {
	f := newJobsFixture(t)
	if rec := f.e.do(http.MethodGet, "/api/v2/events", "", nil, false); rec.Code != http.StatusUnauthorized {
		t.Errorf("stream without a session: %d, want 401", rec.Code)
	}
	rec := f.get("/api/v2/events?job=999999")
	if code, _, _ := errorBody(t, rec); rec.Code != http.StatusNotFound || code != "job_not_found" {
		t.Errorf("stream for an unknown job: %d %s; want 404 job_not_found", rec.Code, code)
	}
}
