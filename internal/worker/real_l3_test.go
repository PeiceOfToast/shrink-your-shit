// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// R14 with real ffmpeg: after a real replace, the replace step's moves[] has the permissions change, the move
// into place and the cleanup, in that order; the cleanup line says the temp folder is gone and held no bytes.
func TestRealReplaceMovesAndCleanup(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "m.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=320x180:rate=25",
		"-c:v", "libx264", "-b:v", "4M", "-pix_fmt", "yuv420p", src)
	st, id := realJob(t, src, 3)
	w := realWorker(st, media.OSExec{})
	w.Rule.CRF = 22 // high enough that the picture check passes on the short sample
	if err := w.run(t); err != nil {
		t.Fatal(err)
	}
	j, _ := st.Get(id)
	if j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	moves, err := st.JobMoves(id)
	if err != nil {
		t.Fatal(err)
	}
	var chmod, moved, cleaned = -1, -1, -1
	for i, m := range moves {
		switch m.Action {
		case "chmod":
			chmod = i
		case "move":
			moved = i
		case "remove":
			cleaned = i
		}
	}
	if chmod < 0 || moved < 0 || cleaned < 0 || !(chmod < moved && moved < cleaned) {
		t.Fatalf("moves %+v: want chmod, then the move into place, then the cleanup", moves)
	}
	if m := moves[moved]; !m.OK || m.To != src || !strings.Contains(m.From, ".syp-tmp") {
		t.Errorf("the move into place: %+v", m)
	}
	if m := moves[cleaned]; !m.OK || !strings.Contains(m.From, ".syp-tmp") {
		t.Errorf("the cleanup: %+v", m)
	} else if _, err := os.Stat(m.From); !os.IsNotExist(err) {
		t.Errorf("the temp folder %s is still there: %v", m.From, err)
	}
	clean := ""
	for _, l := range sectionLines(t, st, id, "replace") {
		if strings.Contains(l.Text, "Removed the temp folder") {
			clean = l.Text
		}
	}
	if clean == "" || !strings.Contains(clean, "(0 bytes left in it)") {
		t.Errorf("the cleanup line: %q", clean)
	}
}
