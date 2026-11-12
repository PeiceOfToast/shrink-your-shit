// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Convert again with real ffmpeg (design 11:45Z): the first run made a 480p file and kept the 720p
// original; converted again with the workflow now keeping the size, the new file is 720p, so it was made
// from the kept original (our 480p output could only give 480p), and the original stays kept.
func TestRealConvertAgainFromTheKeptOriginal(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "Movie.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=4:size=1280x720:rate=25", "-c:v", "libx264", "-b:v", "5000k", "-pix_fmt", "yuv420p", src)
	original, _ := os.ReadFile(src)
	sel := rules.DefaultSelectors(3)
	sel.Resolution = "480"
	tmpl := sel.Template()
	st, id := realJob(t, src, 4)
	w := realWorker(st, media.OSExec{})
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	w.KeepDays, w.Journal = func() int { return 7 }, st
	if err := w.run(t); err != nil {
		t.Fatal(err)
	}
	if j, _ := st.Get(id); j.Status != jobs.Done || probe(t, src).Video().Height != 480 {
		t.Fatalf("first: %s %q", j.Status, j.Error)
	}
	o, err := st.OriginalOfJob(id)
	if err != nil {
		t.Fatal(err)
	}
	sel.Resolution = "keep" // the workflow as it is now
	tmpl = sel.Template()
	if err := st.ConvertAgain(id); err != nil {
		t.Fatal(err)
	}
	if err := w.run(t); err != nil {
		t.Fatal(err)
	}
	if j, _ := st.Get(id); j.Status != jobs.Done {
		t.Fatalf("again: %s %q", j.Status, j.Error)
	}
	if h := probe(t, src).Video().Height; h != 720 {
		t.Errorf("the new file is %dp: not made from the 720p original", h)
	}
	if kept, _ := os.ReadFile(o.Trash); string(kept) != string(original) {
		t.Error("the kept original changed")
	}
	if again, _ := st.OriginalByID(o.ID); again.State != jobs.OriginalKept || !isConvertedFile(src, again) {
		t.Errorf("record %+v", again)
	}
}
