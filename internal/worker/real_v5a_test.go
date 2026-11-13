// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/scanner"
)

// V5a, output-first on real files: "Next to each library" puts the new file in "<library> (shrunk)",
// in the same sub-folder, and leaves the original; the next scan queues nothing (the folder is outside
// the library). When the original changes it is converted again and the new file replaces our own
// earlier one; a file there we didn't make is never replaced (design 01:04Z).
func TestRealNextToLibrary(t *testing.T) {
	needTools(t)
	parent := t.TempDir()
	libDir := filepath.Join(parent, "movies")
	src := filepath.Join(libDir, "Action", "Movie.mp4")
	os.MkdirAll(filepath.Dir(src), 0o755)
	writeSource := func(seconds string) {
		runFFmpeg(t, "-y", "-f", "lavfi", "-i", "testsrc2=duration="+seconds+":size=640x360:rate=25", "-f", "lavfi", "-i", "sine=duration="+seconds+":frequency=440",
			"-map", "0", "-map", "1", "-c:v", "libx264", "-b:v", "4000k", "-c:a", "aac", "-b:a", "96k", "-metadata:s:a:0", "language=eng", src)
		past := time.Now().Add(-time.Hour)
		os.Chtimes(src, past, past)
	}
	writeSource("2")
	before, _ := os.ReadFile(src)
	sel := rules.DefaultSelectors(3)
	sel.MaxBitrate, sel.RateControl, sel.FileType, sel.AfterConverting, sel.FolderMode = "medium", "level", "mkv", "folder", rules.NextToLibrary
	tmpl := sel.Template()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetPaused(false)
	lib, err := st.AddLibrary("Movies", libDir, tmpl, true)
	if err != nil {
		t.Fatal(err)
	}
	scan := func() int {
		sc := &scanner.Scanner{Store: st, Exec: media.OSExec{}, FFprobe: "ffprobe",
			Targets: func() ([]scanner.Target, error) {
				return []scanner.Target{{LibraryID: lib.ID, Path: libDir, Template: tmpl}}, nil
			}}
		return sc.ScanOnce(context.Background()) + sc.ScanOnce(context.Background())
	}
	w := realWorker(st, media.OSExec{})
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	w.KeepDays, w.Journal = func() int { return 7 }, st
	out := filepath.Join(parent, "movies (shrunk)", "Action", "Movie.mkv")
	last := func() jobs.Job {
		js, _ := st.List(10)
		return js[0]
	}

	if n := scan(); n != 1 {
		t.Fatalf("first scan: %d queued", n)
	}
	w.run(t)
	if j := last(); j.Status != jobs.Done || j.OutputPath != out {
		t.Fatalf("converted into the folder beside the library: %s %q %s", j.Status, j.OutputPath, j.Error)
	}
	if b, _ := os.ReadFile(src); string(b) != string(before) {
		t.Error("the original changed")
	}
	first, _ := os.Stat(out)
	if n := scan(); n != 0 {
		t.Errorf("after the conversion: %d queued", n)
	}

	// the original changes: converted again, the new file replaces our earlier one
	writeSource("3")
	if n := scan(); n != 1 {
		t.Fatalf("changed original: %d queued", n)
	}
	w.run(t)
	second, _ := os.Stat(out)
	if j := last(); j.Status != jobs.Done || second == nil || second.Size() == first.Size() {
		t.Fatalf("our earlier output should be replaced: %s %s (sizes %d, %v)", j.Status, j.Error, first.Size(), second)
	}

	// a file there we didn't make: never replaced, the job fails in design's words
	os.WriteFile(out, []byte("someone else's file"), 0o644)
	writeSource("2")
	if n := scan(); n != 1 {
		t.Fatalf("changed again: %d queued", n)
	}
	w.run(t)
	j := last()
	want := "A file named Movie.mkv is already in " + filepath.Dir(out) + " and wasn't made by Shrink Your Shit. Nothing was changed. Move or rename it, then press Retry."
	if j.Status != jobs.Failed || !strings.Contains(j.Error, want) {
		t.Errorf("a file we didn't make: %s %q", j.Status, j.Error)
	}
	if b, _ := os.ReadFile(out); string(b) != "someone else's file" {
		t.Error("the file we didn't make was replaced")
	}
}
