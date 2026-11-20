// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// streamTag reads one stream tag of the file's first video stream with ffprobe ("" when absent).
func streamTag(t *testing.T, path, key string) string {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream_tags="+key,
		"-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	return strings.TrimSpace(string(out))
}

// F9: a source's per-track statistics (here BPS=99999999 and friends) do not come out of a re-encode: the
// new stream's bitrate is its own, not the old one's.
func TestRealStaleStatsDroppedOnReencode(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "stats.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=320x240:rate=25",
		"-f", "lavfi", "-i", "sine=duration=3:frequency=440",
		"-map", "0", "-map", "1", "-c:v", "libx264", "-c:a", "aac",
		"-metadata:s:v:0", "BPS=99999999", "-metadata:s:v:0", "NUMBER_OF_BYTES=12345",
		"-metadata:s:v:0", "NUMBER_OF_FRAMES=75", "-metadata:s:v:0", "BPS-eng=99999999", src)
	if got := streamTag(t, src, "BPS"); got != "99999999" {
		t.Fatalf("the sample does not carry the stale statistic (BPS=%q): the test proves nothing", got)
	}

	p := filepath.Join(t.TempDir(), "m.mkv")
	b, _ := os.ReadFile(src)
	os.WriteFile(p, b, 0o644)
	st, id := realJob(t, p, 3)
	w := realWorker(st, media.OSExec{})
	if err := w.run(t); err != nil {
		t.Fatal(err)
	}
	if j, _ := st.Get(id); j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	for _, key := range []string{"BPS", "BPS-eng", "NUMBER_OF_BYTES", "NUMBER_OF_FRAMES"} {
		if got := streamTag(t, p, key); got == "99999999" || got == "12345" || got == "75" {
			t.Errorf("the re-encoded video still carries the old %s=%s", key, got)
		}
	}
}
