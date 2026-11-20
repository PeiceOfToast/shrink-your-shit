// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// R8, real: a tiny, heavily compressed source converted at the highest quality comes out bigger. With "keep" the
// original stays byte-identical in its folder, nothing else is left beside it, and the job is kept with a reason
// that says why. (If the size check is removed, the original is replaced and this test fails.)
func TestRealBiggerNewFileKeepsTheOriginal(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "tiny.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=320x240:rate=25",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "51", src)
	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	sel := rules.DefaultSelectors(5) // the highest quality: the lowest CRF, so the new file is bigger
	sel.KeepIfBigger = true
	tmpl := sel.Template()
	st, id := realJob(t, src, 3)
	w := realWorker(st, media.OSExec{})
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	w.KeepDays, w.Journal = func() int { return 7 }, st
	w.run(t)

	j, err := st.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != jobs.Kept || !strings.Contains(j.Error, "bigger") {
		t.Fatalf("status %s, reason %q: want kept because the new file is bigger", j.Status, j.Error)
	}
	after, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("the original changed: %d bytes before, %d after", len(before), len(after))
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "tiny.mkv" {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("left in the folder: %v (want only the original)", names)
	}
}
