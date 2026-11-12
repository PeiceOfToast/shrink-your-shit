// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"bufio"
	"context"
	"net/http"
	"path/filepath"
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
