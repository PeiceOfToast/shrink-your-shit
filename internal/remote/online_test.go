// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/web"
)

// slowEncode is ffmpeg, but its encode takes long (then fails): a long conversion on the worker.
type slowEncode struct {
	media.OSExec
	took time.Duration
}

func (x slowEncode) Stream(ctx context.Context, onLine func(string), name string, args ...string) (string, error) {
	for _, a := range args {
		if a == "libx265" {
			select {
			case <-time.After(x.took):
			case <-ctx.Done():
			}
			return "", context.Canceled
		}
	}
	return x.OSExec.Stream(ctx, onLine, name, args...)
}

// V7d (orch-lead 15:02Z): a worker stays online through a long encode, since its heartbeats go on beside
// its work (here an encode 4x the offline wait); once it stops it shows offline, once (worker.updated
// fires on the change, not on every check), and the summary counts it; its load comes with its heartbeats.
func TestWorkerStaysOnlineThroughALongEncode(t *testing.T) {
	needFFmpeg(t)
	srv, st, lib := server(t)
	oldOff, oldCheck, oldBeat, oldPoll := web.RemoteOfflineAfter, web.StatusCheckEvery, HeartbeatEvery, PlanPoll
	web.RemoteOfflineAfter, web.StatusCheckEvery, HeartbeatEvery, PlanPoll = 400*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond
	defer func() {
		web.RemoteOfflineAfter, web.StatusCheckEvery, HeartbeatEvery, PlanPoll = oldOff, oldCheck, oldBeat, oldPoll
	}()
	src := filepath.Join(lib, "Long.mkv")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=duration=2:size=320x180:rate=25",
		"-c:v", "libx264", "-b:v", "2M", "-pix_fmt", "yuv420p", src).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	l, _ := st.AddLibrary("Movies", lib, rules.SliderTemplate(3), true)
	st.Enqueue(jobs.Job{Library: lib, LibraryID: l.ID, Path: src, DurationSec: 2, SizeBefore: 1000})
	code, _, _ := st.NewPairing(time.Now())
	c := &Client{Server: srv.URL, Version: "test"}
	state, err := c.Pair(context.Background(), code, "Desktop")
	if err != nil {
		t.Fatal(err)
	}
	c.Token = state.Token
	events := make(chan string, 100)
	sctx, scancel := context.WithCancel(context.Background())
	defer scancel()
	go func() { // the live stream: its polls run the status checks
		req, _ := http.NewRequestWithContext(sctx, "GET", srv.URL+"/api/v2/events", nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		defer res.Body.Close()
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			if line := sc.Text(); strings.Contains(line, "worker.updated") {
				events <- line
			}
		}
	}()
	worker := func() (status string, cpu, mem any) {
		res, err := http.Get(srv.URL + "/api/v2/workers/" + state.WorkerID)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var w struct {
			Status string
			Live   struct {
				CPUPercent    any
				MemTotalBytes any
			}
		}
		json.NewDecoder(res.Body).Decode(&w)
		return w.Status, w.Live.CPUPercent, w.Live.MemTotalBytes
	}
	ctx, cancel := context.WithCancel(context.Background())
	go c.Run(ctx)
	go c.Work(ctx, Converter{Exec: slowEncode{took: 1600 * time.Millisecond}, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Work: t.TempDir(),
		FreeSpace: func(string) (uint64, error) { return 1 << 40, nil }})
	for end := time.Now().Add(1500 * time.Millisecond); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		if s, _, _ := worker(); s == "offline" {
			t.Fatalf("offline while it converts")
		}
	}
	if s, _, mem := worker(); s != "busy" || mem == nil {
		t.Errorf("converting: %s, memory %v", s, mem)
	}
	cancel() // it stops answering
	time.Sleep(700 * time.Millisecond)
	if s, _, _ := worker(); s != "offline" {
		t.Errorf("after it stopped: %s", s)
	}
	res, _ := http.Get(srv.URL + "/api/v2/summary")
	var sum struct{ Badges struct{ WorkersOffline int } }
	json.NewDecoder(res.Body).Decode(&sum)
	res.Body.Close()
	if sum.Badges.WorkersOffline != 1 {
		t.Errorf("workersOffline %d", sum.Badges.WorkersOffline)
	}
	time.Sleep(300 * time.Millisecond) // more checks: no more events without a change
	offline := 0
	for len(events) > 0 {
		if line := <-events; strings.Contains(line, `"status":"offline"`) && strings.Contains(line, state.WorkerID) {
			offline++
		}
	}
	if offline != 1 {
		t.Errorf("worker.updated offline events: %d", offline)
	}
}
