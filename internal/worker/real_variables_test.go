// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// W4b on a real file: a templated output name and a templated .srt name, filled in from the original's
// probe (W4b's values), and the result passes every check.
func TestRealTemplatedNames(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.srt"), []byte("1\n00:00:00,000 --> 00:00:02,000\nhello\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.srt"), []byte("1\n00:00:00,000 --> 00:00:02,000\nbonjour\n"), 0o644)
	src := filepath.Join(dir, "Movie.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=5:size=1280x720:rate=25", "-f", "lavfi", "-i", "sine=duration=5:frequency=440",
		"-i", filepath.Join(dir, "a.srt"), "-i", filepath.Join(dir, "b.srt"),
		"-map", "0", "-map", "1", "-map", "2", "-map", "3", "-c:v", "libx264", "-b:v", "5000k", "-c:a", "aac", "-b:a", "96k", "-c:s", "srt",
		"-metadata:s:a:0", "language=eng", "-metadata:s:s:0", "language=eng", "-metadata:s:s:1", "language=fre", "-disposition:s:1", "forced", src)
	before, _ := os.ReadFile(src)
	s := rules.DefaultSelectors(3)
	s.AfterConverting, s.Subtitles, s.Audio = "beside", "srt", "aac"
	s.OutputName, s.SrtName = "{{title}}-{{resolution}}", "{{title}}-{{sub_lang}}{{sub_kind}}"
	tmpl := s.Template()
	tmpl.Languages = "eng,fra" // both tracks are in the user's languages, so both become .srt files
	st, id := realJob(t, src, 5)
	w := realWorker(st, media.OSExec{})
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tmpl, 0, nil }
	if err := w.run(t); err != nil {
		t.Fatal(err)
	}
	if j, _ := st.Get(id); j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	out := filepath.Join(dir, "Movie-720p.mkv") // the resolution came from the probe
	info := probe(t, out)
	if v := info.Video(); v == nil || v.Codec != "hevc" || v.Width != 1280 || v.Height != 720 {
		t.Errorf("%s: %+v", out, v)
	}
	if len(info.Subtitles()) != 0 || len(info.Audio()) != 1 {
		t.Errorf("tracks: %+v", info)
	}
	// the .srt names the template made, with sub_lang and sub_kind
	for name, want := range map[string]string{"Movie-eng.srt": "hello", "Movie-fre.forced.srt": "bonjour"} {
		if b, _ := os.ReadFile(filepath.Join(dir, name)); !strings.Contains(string(b), want) {
			t.Errorf("%s: %q", name, b)
		}
	}
	if b, _ := os.ReadFile(src); string(b) != string(before) {
		t.Error("the original must stay as it was")
	}
	if tempLeft(src) {
		t.Error("temp left")
	}
}

// The move-to folder and the track title are templates too (W4b): on a real file the new file lands
// in the folder the template made, and ffprobe reads the resolved title back off its audio track.
func TestRealTemplatedFolderAndTrackTitle(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "Movie.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=5:size=1280x720:rate=25", "-f", "lavfi", "-i", "sine=duration=5",
		"-c:v", "libx264", "-b:v", "5000k", "-c:a", "aac", "-b:a", "96k", "-metadata:s:a:0", "language=eng", src)
	before, _ := os.ReadFile(src)
	s := rules.DefaultSelectors(3)
	s.AfterConverting, s.FolderMode, s.Audio = "folder", "", "aac"
	s.OutputDir = filepath.Join(t.TempDir(), "{{title}}") // one folder for all, filled in for this file
	s.OutputName, s.TrackTitle = "{{title}}-{{resolution}}", "{{title}} ({{resolution}})"
	tmpl := s.Template()
	st, id := realJob(t, src, 5)
	w := realWorker(st, media.OSExec{})
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tmpl, 0, nil }
	if err := w.run(t); err != nil {
		t.Fatal(err)
	}
	j, _ := st.Get(id)
	if j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	out := filepath.Join(filepath.Dir(s.OutputDir), "Movie", "Movie-720p.mkv") // the folder came from the probe
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("the new file is not where the templates put it: %v", err)
	}
	if a := probe(t, out).Audio(); len(a) != 1 || a[0].Title != "Movie (720p)" {
		t.Errorf("the track title: %+v", a)
	}
	if b, _ := os.ReadFile(src); string(b) != string(before) {
		t.Error("the original must stay as it was")
	}
	if tempLeft(src) {
		t.Error("temp left")
	}
}
