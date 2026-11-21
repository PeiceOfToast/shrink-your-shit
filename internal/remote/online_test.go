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

// slowEncode is ffmpeg, but its encode takes long (then fails): a long conversion on the worker. It says when
// the encode starts (started nil: don't say).
type slowEncode struct {
	media.OSExec
	took    time.Duration
	started chan struct{}
}

func (x slowEncode) Stream(ctx context.Context, onLine func(string), name string, args ...string) (string, error) {
	for _, a := range args {
		if a == "libx265" {
			if x.started != nil {
				select {
				case x.started <- struct{}{}:
				default:
				}
			}
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
// its work (here an encode 3x the offline wait); once it stops it shows offline, once (worker.updated
// fires on the change, not on every check), and the summary counts it; its load comes with its heartbeats.
func TestWorkerStaysOnlineThroughALongEncode(t *testing.T) {
	needFFmpeg(t)
	srv, st, lib := server(t)
	oldOff, oldCheck, oldBeat, oldPoll := web.RemoteOfflineAfter, web.StatusCheckEvery, HeartbeatEvery, PlanPoll
	web.RemoteOfflineAfter, web.StatusCheckEvery, HeartbeatEvery, PlanPoll = time.Second, 50*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond // margins for a busy machine
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
	lastSeen := func() string {
		rws, err := st.RemoteWorkers()
		if err != nil {
			t.Fatal(err)
		}
		for _, rw := range rws {
			if rw.ID == state.WorkerID {
				return rw.LastSeen
			}
		}
		return ""
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone, workDone := make(chan struct{}), make(chan struct{})
	x := slowEncode{took: 3 * time.Second, started: make(chan struct{}, 1)}
	go func() { c.Run(ctx); close(runDone) }()
	go func() {
		c.Work(ctx, Converter{Exec: x, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Work: t.TempDir(),
			FreeSpace: func(string) (uint64, error) { return 1 << 40, nil }})
		close(workDone)
	}()
	select {
	case <-x.started:
	case <-time.After(20 * time.Second):
		t.Fatal("the encode never started")
	}
	// The heartbeats go on beside the encode: once it has run half its length, its last_seen still moves on,
	// and a fresh one is not offline. Waiting for that beat is the sync here — sampling the 1 s offline window
	// on a busy host read a long scheduling pause as offline (the 2026-11-21 flake, which this does not
	// reproduce: fixed from what the test now observes, not by a longer sleep).
	seen := lastSeen()
	time.Sleep(x.took / 2) // let the encode run half its length
	for end := time.Now().Add(x.took + 2*time.Second); ; time.Sleep(10 * time.Millisecond) {
		if s := lastSeen(); s != "" && s != seen {
			break
		}
		if time.Now().After(end) {
			t.Fatalf("the worker's last_seen did not move on while it converted: %q", seen)
		}
	}
	if s, _, mem := worker(); s != "busy" || mem == nil {
		t.Errorf("converting: %s, memory %v", s, mem)
	}
	// A busy host's pause during the encode is not this test's claim: count the offline events from the stop on.
	for len(events) > 0 {
		<-events
	}
	cancel() // it stops answering
	for _, done := range []struct {
		name string
		c    chan struct{}
	}{{"run", runDone}, {"work", workDone}} {
		select {
		case <-done.c:
		case <-time.After(30 * time.Second):
			t.Fatalf("the worker's %s did not stop", done.name)
		}
	}
	time.Sleep(100 * time.Millisecond) // any call in flight has landed: no heartbeat may set last_seen after the clear
	if err := st.ClearSeen(state.WorkerID); err != nil {
		t.Fatal(err)
	}
	// A worker whose last_seen is cleared reads offline whatever the window (the explicit sync, as in
	// TestWorkerHoldingBack): wait for that read, and then for the check that records the change.
	offline := 0
	isOffline := func(line string) bool {
		return strings.Contains(line, `"status":"offline"`) && strings.Contains(line, state.WorkerID)
	}
	collect := func(d time.Duration) { // count this worker's offline events for d
		for end := time.Now().Add(d); time.Now().Before(end); {
			select {
			case line := <-events:
				if isOffline(line) {
					offline++
				}
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if s, _, _ := worker(); s == "offline" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the worker did not read offline after it stopped")
		}
	}
	for deadline := time.Now().Add(5 * time.Second); offline == 0 && time.Now().Before(deadline); {
		collect(50 * time.Millisecond)
	}
	res, _ := http.Get(srv.URL + "/api/v2/summary")
	var sum struct{ Badges struct{ WorkersOffline int } }
	json.NewDecoder(res.Body).Decode(&sum)
	res.Body.Close()
	if sum.Badges.WorkersOffline != 1 {
		t.Errorf("workersOffline %d", sum.Badges.WorkersOffline)
	}
	collect(300 * time.Millisecond) // more checks: no more events without a change
	if offline != 1 {
		t.Errorf("worker.updated offline events: %d", offline)
	}
}
