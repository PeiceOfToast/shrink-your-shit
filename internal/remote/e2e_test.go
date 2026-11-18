// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/web"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

func needFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		if os.Getenv("SYP_REQUIRE_FFMPEG") != "" {
			t.Fatal("ffmpeg is required")
		}
		t.Skip("no ffmpeg")
	}
}

// server is a real server (its store, its web handler, its runs of remote jobs) on loopback.
func server(t *testing.T) (*httptest.Server, *jobs.Store, string) {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetPaused(false)
	t.Cleanup(func() { st.Close() })
	access, _, err := web.OpenAccess(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := httptest.NewServer(web.Handler(st, access, web.Engine{DataDir: t.TempDir(),
		RunRemote: func(job jobs.Job, id string, rj *worker.RemoteJob) {
			w := &worker.Worker{ID: id, Queue: st, Exec: media.OSExec{}, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Template: st.TemplateFor,
				KeepDays: func() int { return 7 }, Journal: st, Sidecars: st}
			w.RunRemote(ctx, job, rj)
		}}))
	t.Cleanup(srv.Close)
	lib := t.TempDir()
	return srv, st, lib
}

// V7b2 end to end: a worker (here in this process, over HTTP) pairs, leases the job, downloads the
// original, converts and checks it, uploads the result, and the server re-checks it and puts it in
// place, keeping the original as a local conversion does.
func TestRemoteWorkerConvertsAFile(t *testing.T) {
	needFFmpeg(t)
	srv, st, lib := server(t)
	src := filepath.Join(lib, "Movie.mkv")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=duration=4:size=640x360:rate=25",
		"-c:v", "libx264", "-b:v", "3M", "-pix_fmt", "yuv420p", src).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	original, _ := os.ReadFile(src)
	l, err := st.AddLibrary("Movies", lib, rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := st.Enqueue(jobs.Job{Library: lib, LibraryID: l.ID, Path: src, DurationSec: 4, SizeBefore: int64(len(original))})
	code, _, _ := st.NewPairing(time.Now())
	c := &Client{Server: srv.URL, Version: "test"}
	state, err := c.Pair(context.Background(), code, "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	c.Token = state.Token
	old := PlanPoll
	PlanPoll = 100 * time.Millisecond
	defer func() { PlanPoll = old }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Work(ctx, Converter{Exec: media.OSExec{}, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Work: t.TempDir(),
		FreeSpace: func(string) (uint64, error) { return 1 << 40, nil }})
	var j jobs.Job
	for end := time.Now().Add(2 * time.Minute); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		if j, _ = st.Get(id); j.Status != jobs.Queued && j.Status != jobs.Running {
			break
		}
	}
	if j.Status != jobs.Done || j.Worker != state.WorkerID {
		t.Fatalf("job %s by %q: %q", j.Status, j.Worker, j.Error)
	}
	o, err := st.OriginalOfJob(id)
	if err != nil {
		t.Fatal(err)
	}
	if kept, _ := os.ReadFile(o.Trash); string(kept) != string(original) {
		t.Error("the original wasn't kept as it was")
	}
	info, err := media.Probe(context.Background(), media.OSExec{}, "ffprobe", src)
	if err != nil || info.Video() == nil || info.Video().Codec != "hevc" {
		t.Errorf("the new file: %+v %v", info.Video(), err)
	}
}

// A worker that vanishes mid-upload: a report whose checksum doesn't match what arrived is refused;
// then it stops answering, its lease runs out, the job goes back to the queue with the original
// untouched, the server's run ends and its staged file next to the original is gone; the worker's late
// calls are refused (409).
func TestRemoteWorkerVanishesMidUpload(t *testing.T) {
	needFFmpeg(t)
	srv, st, lib := server(t)
	src := filepath.Join(lib, "Movie.mkv")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=duration=2:size=320x180:rate=25",
		"-c:v", "libx264", "-b:v", "2M", "-pix_fmt", "yuv420p", src).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	original, _ := os.ReadFile(src)
	l, _ := st.AddLibrary("Movies", lib, rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: lib, LibraryID: l.ID, Path: src, DurationSec: 2, SizeBefore: int64(len(original))})
	code, _, _ := st.NewPairing(time.Now())
	c := &Client{Server: srv.URL, Version: "test"}
	state, _ := c.Pair(context.Background(), code, "Laptop")
	c.Token = state.Token
	oldLease, oldCheck, oldPoll := jobs.LeaseFor, worker.RemoteCheckEvery, PlanPoll
	jobs.LeaseFor, worker.RemoteCheckEvery, PlanPoll = time.Second, 50*time.Millisecond, 50*time.Millisecond
	defer func() { jobs.LeaseFor, worker.RemoteCheckEvery, PlanPoll = oldLease, oldCheck, oldPoll }()
	ctx := context.Background()
	lease, err := c.Lease(ctx)
	if err != nil || lease == nil {
		t.Fatalf("lease %v %v", lease, err)
	}
	if _, err := c.plan(ctx, lease); err != nil {
		t.Fatal(err)
	}
	half := filepath.Join(t.TempDir(), "half")
	os.WriteFile(half, []byte("the first half of a new file"), 0o600)
	if _, _, err := c.upload(ctx, lease, half, "video", 0); err != nil {
		t.Fatal(err)
	}
	err = c.report(ctx, lease, map[string]any{"ok": true, "sha256": "00", "bytes": 999})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != "upload_damaged" {
		t.Errorf("a report that doesn't match the upload: %v", err)
	}
	time.Sleep(1500 * time.Millisecond) // it stopped answering: its lease runs out
	if n, err := st.RequeueExpiredLeases(0); err != nil || n != 1 {
		t.Fatalf("requeued %d %v", n, err)
	}
	if j, _ := st.Get(id); j.Status != jobs.Queued {
		t.Errorf("the job: %s", j.Status)
	}
	if b, _ := os.ReadFile(src); string(b) != string(original) {
		t.Error("the original changed")
	}
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if left, _ := filepath.Glob(filepath.Join(lib, ".*syp-tmp*")); len(left) == 0 {
			break
		}
	}
	if left, _ := filepath.Glob(filepath.Join(lib, ".*syp-tmp*")); len(left) != 0 {
		t.Errorf("staging left: %v", left)
	}
	if err := c.report(ctx, lease, map[string]any{"ok": false, "message": "late"}); !errors.As(err, &ae) || ae.Code != "lease_gone" {
		t.Errorf("a late report: %v", err)
	}
}
