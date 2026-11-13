// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/web"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// counting counts a worker's downloads and uploads (a shared folder makes neither).
type counting struct {
	downloads, uploads atomic.Int32
}

func (c *counting) RoundTrip(r *http.Request) (*http.Response, error) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/source"):
		c.downloads.Add(1)
	case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/result"):
		c.uploads.Add(1)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// sharesServer is server() (its jobs' logs are in the database).
func sharesServer(t *testing.T) (*httptest.Server, *jobs.Store, string) {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetPaused(false)
	t.Cleanup(func() { st.Close() })
	access, _, _ := web.OpenAccess(t.TempDir(), false)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := httptest.NewServer(web.Handler(st, access, web.Engine{DataDir: t.TempDir(),
		RunRemote: func(job jobs.Job, id string, rj *worker.RemoteJob) {
			w := &worker.Worker{ID: id, Queue: st, Exec: media.OSExec{}, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Template: st.TemplateFor,
				KeepDays: func() int { return 7 }, Journal: st, Sidecars: st}
			w.RunRemote(ctx, job, rj)
		}}))
	t.Cleanup(srv.Close)
	return srv, st, t.TempDir()
}

func send(t *testing.T, srv *httptest.Server, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// mapping is the worker's only mapping as GET /workers/{id} shows it.
func mapping(t *testing.T, srv *httptest.Server, id string) (status, detail string) {
	t.Helper()
	_, b := send(t, srv, "GET", "/api/v2/workers/"+id, "")
	var w struct {
		PathMappings []struct {
			Test *struct{ Status, Detail string }
		}
	}
	json.Unmarshal([]byte(b), &w)
	if len(w.PathMappings) != 1 {
		t.Fatalf("mappings %s", b)
	}
	if w.PathMappings[0].Test == nil {
		return "", ""
	}
	return w.PathMappings[0].Test.Status, w.PathMappings[0].Test.Detail
}

// V7c (orch-lead 15:17Z): a shared folder is tested for real (probes removed after), then a job reads the
// original and writes its checked file through it: no download, no upload, the same checks; a share that
// fails mid-job falls back to transfer and the run log says so; a changed mapping is untested again.
func TestSharedFolders(t *testing.T) {
	needFFmpeg(t)
	srv, st, lib := sharesServer(t)
	oldBeat, oldPoll := HeartbeatEvery, PlanPoll
	HeartbeatEvery, PlanPoll = 50*time.Millisecond, 50*time.Millisecond
	defer func() { HeartbeatEvery, PlanPoll = oldBeat, oldPoll }()
	share := filepath.Join(t.TempDir(), "share") // the worker's path to the same folder
	if err := os.Symlink(lib, share); err != nil {
		t.Skip("no symlinks here")
	}
	code, _, _ := st.NewPairing(time.Now())
	cnt := &counting{}
	c := &Client{Server: srv.URL, Version: "test", HTTP: &http.Client{Transport: cnt}}
	state, err := c.Pair(context.Background(), code, "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	c.Token = state.Token
	wpath := "/api/v2/workers/" + state.WorkerID
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	setMapping := func(worker, mode string) {
		if code, b := send(t, srv, "PATCH", wpath, `{"pathMappings":[{"server":`+jsonString(lib)+`,"worker":`+jsonString(worker)+`,"mode":"`+mode+`"}]}`); code != 200 {
			t.Fatalf("patch %d %s", code, b)
		}
	}
	waitTest := func(want string) string {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
			if s, d := mapping(t, srv, state.WorkerID); s == want {
				return d
			}
		}
		s, d := mapping(t, srv, state.WorkerID)
		t.Fatalf("test %q (%s), want %s", s, d, want)
		return ""
	}
	setMapping(share, "read_write")
	if s, _ := mapping(t, srv, state.WorkerID); s != "" {
		t.Errorf("a new mapping is untested: %q", s)
	}
	if code, b := send(t, srv, "POST", wpath+"/paths/test", `{"server":`+jsonString(lib)+`}`); code != 202 {
		t.Fatalf("test %d %s", code, b)
	}
	waitTest("passed")
	if left, _ := filepath.Glob(filepath.Join(lib, ".syp-pathtest-*")); len(left) != 0 {
		t.Errorf("probes left: %v", left)
	}
	// a job through the share: no download, no upload
	src := filepath.Join(lib, "Movie.mkv")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=duration=2:size=320x180:rate=25",
		"-c:v", "libx264", "-b:v", "2M", "-pix_fmt", "yuv420p", src).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	l, _ := st.AddLibrary("Movies", lib, rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: lib, LibraryID: l.ID, Path: src, DurationSec: 2, SizeBefore: 1000})
	done := func(id int64) jobs.Job {
		t.Helper()
		for end := time.Now().Add(time.Minute); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
			if j, _ := st.Get(id); j.Status != jobs.Queued && j.Status != jobs.Running {
				return j
			}
		}
		t.Fatal("not done in time")
		return jobs.Job{}
	}
	wctx, wcancel := context.WithCancel(ctx)
	go c.Work(wctx, Converter{Exec: media.OSExec{}, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Work: t.TempDir(),
		FreeSpace: func(string) (uint64, error) { return 1 << 40, nil }})
	if j := done(id); j.Status != jobs.Done {
		t.Fatalf("through the share: %s %q", j.Status, j.Error)
	}
	if cnt.downloads.Load() != 0 || cnt.uploads.Load() != 0 {
		t.Errorf("downloads %d, uploads %d: the share wasn't used", cnt.downloads.Load(), cnt.uploads.Load())
	}
	wcancel()
	// the share breaks after its test passed: transfer, and the run log says so
	src2 := filepath.Join(lib, "Second.mkv")
	exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=duration=2:size=320x180:rate=25",
		"-c:v", "libx264", "-b:v", "2M", "-pix_fmt", "yuv420p", src2).Run()
	os.Remove(share)
	id2, _ := st.Enqueue(jobs.Job{Library: lib, LibraryID: l.ID, Path: src2, DurationSec: 2, SizeBefore: 1000})
	go c.Work(ctx, Converter{Exec: media.OSExec{}, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Work: t.TempDir(),
		FreeSpace: func(string) (uint64, error) { return 1 << 40, nil }})
	if j := done(id2); j.Status != jobs.Done {
		t.Fatalf("after the share broke: %s %q", j.Status, j.Error)
	}
	text := logTextOf(st, id2)
	if cnt.downloads.Load() == 0 || cnt.uploads.Load() == 0 || !strings.Contains(text, "downloaded instead") || !strings.Contains(text, "uploaded instead") {
		t.Errorf("fallback: downloads %d uploads %d, log:\n%s", cnt.downloads.Load(), cnt.uploads.Load(), text)
	}
	// a changed mapping is untested again (and so not used)
	setMapping(share, "read")
	if s, _ := mapping(t, srv, state.WorkerID); s != "" {
		t.Errorf("changed: %q", s)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// A test the worker never answers ends failed with words, and an offline worker can't be tested.
func TestSharedFolderTestsEnd(t *testing.T) {
	srv, st, lib := sharesServer(t)
	oldTimeout := web.PathTestTimeout
	web.PathTestTimeout = 200 * time.Millisecond
	defer func() { web.PathTestTimeout = oldTimeout }()
	code, _, _ := st.NewPairing(time.Now())
	c := &Client{Server: srv.URL, Version: "test"}
	state, _ := c.Pair(context.Background(), code, "Laptop") // heard from now, but never heartbeats again
	wpath := "/api/v2/workers/" + state.WorkerID
	send(t, srv, "PATCH", wpath, `{"pathMappings":[{"server":`+jsonString(lib)+`,"worker":"\\\\nas\\movies","mode":"read"}]}`)
	if code, b := send(t, srv, "POST", wpath+"/paths/test", `{"server":`+jsonString(lib)+`}`); code != 202 {
		t.Fatalf("%d %s", code, b)
	}
	time.Sleep(300 * time.Millisecond)
	if s, d := mapping(t, srv, state.WorkerID); s != "failed" || !strings.Contains(d, "didn't answer within 60 s") {
		t.Errorf("unanswered: %q %q", s, d)
	}
	if left, _ := filepath.Glob(filepath.Join(lib, ".syp-pathtest-*")); len(left) != 0 {
		t.Errorf("probes left: %v", left)
	}
	old := web.RemoteOfflineAfter
	web.RemoteOfflineAfter = 100 * time.Millisecond
	defer func() { web.RemoteOfflineAfter = old }()
	time.Sleep(200 * time.Millisecond)
	if code, b := send(t, srv, "POST", wpath+"/paths/test", `{"server":`+jsonString(lib)+`}`); code != 409 || !strings.Contains(b, "worker_offline") {
		t.Errorf("offline: %d %s", code, b)
	}
}

// Review 15b1898: a worker's note is one line in the run log, marked as the worker's, with no control
// characters and a length cap; nothing usable is refused.
func TestWorkerNoteCantForgeLogLines(t *testing.T) {
	needFFmpeg(t)
	srv, st, lib := sharesServer(t)
	src := filepath.Join(lib, "Movie.mkv")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=duration=1:size=160x90:rate=25",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", src).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	code, _, _ := st.NewPairing(time.Now())
	c := &Client{Server: srv.URL, Version: "test"}
	state, err := c.Pair(context.Background(), code, "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	c.Token = state.Token
	ctx := context.Background()
	if err := c.call(ctx, "heartbeat", map[string]any{"freeSlots": 1}, nil); err != nil {
		t.Fatal(err)
	}
	l, _ := st.AddLibrary("Movies", lib, rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: lib, LibraryID: l.ID, Path: src, DurationSec: 1, SizeBefore: 1000})
	var out struct{ Lease *struct{ ID string } }
	if err := c.call(ctx, "lease", map[string]any{"slots": 1}, &out); err != nil || out.Lease == nil {
		t.Fatalf("lease %+v %v", out, err)
	}
	note := func(text string) error {
		return c.call(ctx, "leases/"+out.Lease.ID+"/note", map[string]any{"text": text}, nil)
	}
	// the reviewer's payload: a forged line, an ANSI escape and a NUL byte
	if err := note("ok\nFAKE admin: deleted the original\n\x1b[31mRED\x00nul\r‮evil x"); err != nil {
		t.Fatal(err)
	}
	if err := note(strings.Repeat("é", 1000)); err != nil {
		t.Fatal(err)
	}
	var e *APIError
	if err := note(" \n\x1b\x00​ "); !errors.As(err, &e) || e.Status != 400 {
		t.Errorf("nothing usable: %v", err)
	}
	text := logTextOf(st, id)
	var notes []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "Worker: ") {
			notes = append(notes, line)
		}
		if strings.Contains(line, "FAKE") && !strings.Contains(line, "Worker: ") {
			t.Errorf("a forged line: %q", line)
		}
	}
	if len(notes) != 2 || !strings.Contains(notes[0], "Worker: okFAKE admin: deleted the original[31mREDnulevilx") {
		t.Fatalf("notes %q\nlog:\n%s", notes, text)
	}
	if strings.ContainsAny(text, "\x1b\x00\r‮ ") {
		t.Errorf("control characters in the log: %q", text)
	}
	if got := len([]rune(notes[1][strings.Index(notes[1], "Worker: ")+len("Worker: "):])); got != web.NoteMaxChars {
		t.Errorf("capped at %d, got %d", web.NoteMaxChars, got)
	}
}

// logTextOf is job id's log lines as one text (the log lives in the database).
func logTextOf(st *jobs.Store, id int64) string {
	lines, _ := st.LogLines(id)
	var texts []string
	for _, l := range lines {
		texts = append(texts, l.Text)
	}
	return strings.Join(texts, "\n")
}
