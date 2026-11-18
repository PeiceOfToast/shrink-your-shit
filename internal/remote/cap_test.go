// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// Review d1fe673: an upload larger than the room kept for it on the destination disk (the source plus
// a tenth plus 64 MB, as a local run's preflight) is refused (413 too_large), its staged part emptied,
// and the job ends as a local run out of room does: back in the queue to be tried later.
func TestUploadCappedAtTheRoomKeptForIt(t *testing.T) {
	needFFmpeg(t)
	srv, st, lib := server(t)
	src := filepath.Join(lib, "Movie.mkv")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=duration=1:size=320x180:rate=25",
		"-c:v", "libx264", "-b:v", "2M", "-pix_fmt", "yuv420p", src).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	fi, _ := os.Stat(src)
	l, _ := st.AddLibrary("Movies", lib, rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: lib, LibraryID: l.ID, Path: src, DurationSec: 1, SizeBefore: fi.Size()})
	code, _, _ := st.NewPairing(time.Now())
	c := &Client{Server: srv.URL, Version: "test"}
	state, _ := c.Pair(context.Background(), code, "Desktop")
	c.Token = state.Token
	old := PlanPoll
	PlanPoll = 50 * time.Millisecond
	defer func() { PlanPoll = old }()
	ctx := context.Background()
	lease, err := c.Lease(ctx)
	if err != nil || lease == nil {
		t.Fatalf("lease %v %v", lease, err)
	}
	if _, err := c.plan(ctx, lease); err != nil {
		t.Fatal(err)
	}
	room := int64(worker.SpaceNeeded(fi.Size()))
	body := io.LimitReader(zeros{}, room+1024)
	res, err := c.request(ctx, http.MethodPut, "leases/"+lease.ID+"/result?part=video&index=0", body, map[string]string{"Upload-Offset": "0", "Content-Type": "application/octet-stream"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(string(b), "too_large") || !strings.Contains(string(b), "larger than the room kept for it on the destination disk") {
		t.Fatalf("%d %s (room %s)", res.StatusCode, b, strconv.FormatInt(room, 10))
	}
	var j jobs.Job
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if j, _ = st.Get(id); j.Status == jobs.Queued {
			break
		}
	}
	if j.Status != jobs.Queued || j.RetryAt == "" || !strings.Contains(strings.ToLower(j.Error), "space") {
		t.Errorf("the job: %s retry %q %q", j.Status, j.RetryAt, j.Error)
	}
	if left, _ := filepath.Glob(filepath.Join(lib, ".*syp-tmp*")); len(left) != 0 {
		t.Errorf("staging left: %v", left)
	}
}
