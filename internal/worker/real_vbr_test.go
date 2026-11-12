// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// User review 11-12 item 1, done when: a Standard flow at 720 / Medium / VBR with a 3 Mbps limit makes a
// 720p file whose average is close to the 720p Medium figure (2.5 Mbps), on a real, busy 1080p file
// (H.264 at 10 Mbps, so neither the level nor the limit is above it). The peak is the encoder's
// (-maxrate 3000k with -bufsize 6000k: the file plays from a 3 Mbps stream with a 2 s buffer).
func TestRealVBR720MediumWith3MbpsLimit(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "Busy.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=25:duration=12", "-vf", "noise=alls=10:allf=t",
		"-c:v", "libx264", "-preset", "ultrafast", "-b:v", "10M", "-pix_fmt", "yuv420p", src)
	sel := rules.DefaultSelectors(3)
	sel.Resolution, sel.MaxBitrate, sel.RateControl, sel.VBRMaxKbps, sel.Audio, sel.OnlyMyLanguages = "720", "medium", "vbr", 3000, "as_is", false
	if err := sel.Validate(); err != nil {
		t.Fatal(err)
	}
	tmpl := sel.Template()
	st, id := realJob(t, src, 12)
	w := realWorker(st, media.OSExec{})
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	if err := w.run(t); err != nil {
		t.Fatal(err)
	}
	if j, _ := st.Get(id); j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	if h := probe(t, src).Video().Height; h != 720 {
		t.Errorf("height %d", h)
	}
	out, err := exec.CommandContext(context.Background(), "ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "packet=size",
		"-of", "csv=p=0", src).Output()
	if err != nil {
		t.Fatal(err)
	}
	var bytes int64
	for _, l := range strings.Fields(string(out)) {
		n, _ := strconv.ParseInt(strings.TrimSuffix(l, ","), 10, 64)
		bytes += n
	}
	avg := float64(bytes*8) / 12 / 1000
	t.Logf("average %.0f kbps (720p Medium: 2500; limit 3000)", avg)
	if avg < 2500*0.85 || avg > 2500*1.15 {
		t.Errorf("average %.0f kbps, not close to 2500", avg)
	}
}
