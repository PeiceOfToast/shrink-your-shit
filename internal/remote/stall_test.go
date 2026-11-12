// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/runlog"
	"github.com/shrink-your-shit/shrink-your-shit/internal/web"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// stallServer is a server whose remote runs are stopped after stall without the encode moving on.
func stallServer(t *testing.T, stall time.Duration) (*httptest.Server, *jobs.Store, string) {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetPaused(false)
	t.Cleanup(func() { st.Close() })
	access, _, _ := web.OpenAccess(t.TempDir(), false)
	logs := &runlog.Logs{Dir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := httptest.NewServer(web.Handler(st, access, web.Engine{DataDir: t.TempDir(), RunLogs: logs,
		RunRemote: func(job jobs.Job, id string, rj *worker.RemoteJob) {
			w := &worker.Worker{ID: id, Queue: st, Exec: media.OSExec{}, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Template: st.TemplateFor,
				KeepDays: func() int { return 7 }, Journal: st, Sidecars: st, Logs: logs, StallAfter: stall}
			w.RunRemote(ctx, job, rj)
		}}))
	t.Cleanup(srv.Close)
	return srv, st, t.TempDir()
}

// stuckEncode is a worker's ffmpeg that reports the same position until it is stopped.
type stuckEncode struct{ media.OSExec }

func (stuckEncode) Stream(ctx context.Context, onLine func(string), name string, args ...string) (string, error) {
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(10 * time.Millisecond):
			onLine("out_time_us=5000000")
			onLine("progress=continue")
		}
	}
}

// remoteJob makes a 2-second file in a new library, queues it and returns the job's id.
func remoteJob(t *testing.T, st *jobs.Store, lib string) int64 {
	t.Helper()
	os.MkdirAll(lib, 0o755)
	src := filepath.Join(lib, "Movie.mkv")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=duration=2:size=320x180:rate=25",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", src).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	l, _ := st.AddLibrary("Movies", lib, rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: lib, LibraryID: l.ID, Path: src, DurationSec: 2, SizeBefore: 1000})
	return id
}

func pairClient(t *testing.T, srv *httptest.Server, st *jobs.Store) *Client {
	t.Helper()
	code, _, _ := st.NewPairing(time.Now())
	c := &Client{Server: srv.URL, Version: "test"}
	state, err := c.Pair(context.Background(), code, "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	c.Token = state.Token
	return c
}

// Row 17:40Z, orch-lead 19:13Z: a remote worker whose encode repeats one position is stopped by the
// server after StallAfter, as a local one is (ReasonStalled, the same words), and the job ends failed.
func TestRemoteEncodeRepeatingOnePositionIsStopped(t *testing.T) {
	needFFmpeg(t)
	srv, st, lib := stallServer(t, 300*time.Millisecond)
	oldPoll, oldEvery := PlanPoll, ProgressEvery
	PlanPoll, ProgressEvery = 50*time.Millisecond, 20*time.Millisecond
	defer func() { PlanPoll, ProgressEvery = oldPoll, oldEvery }()
	id := remoteJob(t, st, lib)
	c := pairClient(t, srv, st)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Work(ctx, Converter{Exec: stuckEncode{}, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Work: t.TempDir(),
		FreeSpace: func(string) (uint64, error) { return 1 << 40, nil }})
	var j jobs.Job
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if j, _ = st.Get(id); j.Status != jobs.Queued && j.Status != jobs.Running {
			break
		}
	}
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "Stopped: no progress for 300ms") || !strings.Contains(j.Error, "original is untouched") {
		t.Errorf("%s %q", j.Status, j.Error)
	}
}

// ...and its checks after the encode ("checking": nothing is reported while they run, and a full
// picture check can take long) are never a stall; nor is a worker that doesn't send its position.
func TestRemoteChecksAndUnjudgedWorkersAreNotStopped(t *testing.T) {
	needFFmpeg(t)
	srv, st, lib := stallServer(t, 300*time.Millisecond)
	oldPoll := PlanPoll
	PlanPoll = 50 * time.Millisecond
	defer func() { PlanPoll = oldPoll }()
	ctx := context.Background()
	for _, c := range []struct {
		name    string
		reports []map[string]any
	}{
		{"checking", []map[string]any{{"stage": "converting", "outTimeUs": 0}, {"stage": "converting", "outTimeUs": 900000}, {"stage": "checking"}}},
		{"no position", []map[string]any{{"stage": "converting", "percent": 10}}},
	} {
		remoteJob(t, st, filepath.Join(lib, c.name))
		cl := pairClient(t, srv, st)
		l, err := cl.Lease(ctx)
		if err != nil || l == nil {
			t.Fatalf("%s: lease %v %v", c.name, l, err)
		}
		if _, err := cl.plan(ctx, l); err != nil {
			t.Fatal(err)
		}
		for _, r := range c.reports {
			if err := cl.call(ctx, "leases/"+l.ID+"/progress", r, nil); err != nil {
				t.Fatal(c.name, err)
			}
		}
		time.Sleep(1200 * time.Millisecond)         // 4x StallAfter, renewing nothing in between (the lease lasts longer)
		id, _ := strconv.ParseInt(l.Job.ID, 10, 64) // the one leased (the case before gave its own back)
		if j, _ := st.Get(id); j.Status != jobs.Running {
			t.Errorf("%s: %s %q", c.name, j.Status, j.Error)
		}
		cl.call(ctx, "leases/"+l.ID+"/release", map[string]any{}, nil)
	}
}
