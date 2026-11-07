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

// The audio and subtitle choices on real files (slice 25b): the result has exactly the tracks the Plan
// says, and passes every check.
func TestRealAudioAndSubtitles(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	srt := filepath.Join(dir, "s.srt")
	os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:02,000\nhi\n"), 0o644)
	// video; a 5.1 FLAC (lossless) in English; a small stereo AC-3 in Japanese; English text subtitles
	src := filepath.Join(dir, "movie.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=5:size=1280x720:rate=25",
		"-f", "lavfi", "-i", "sine=duration=5:frequency=440", "-f", "lavfi", "-i", "sine=duration=5:frequency=660", "-i", srt,
		"-map", "0", "-map", "1", "-map", "2", "-map", "3",
		"-c:v", "libx264", "-b:v", "5000k", "-c:a:0", "flac", "-ac:a:0", "6", "-c:a:1", "ac3", "-b:a:1", "96k", "-ac:a:1", "2",
		"-c:s", "srt", "-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=jpn", "-metadata:s:s:0", "language=eng", src)
	run := func(t *testing.T, audio, subs, langs string) media.Info {
		t.Helper()
		p := filepath.Join(t.TempDir(), "m.mkv")
		b, _ := os.ReadFile(src)
		os.WriteFile(p, b, 0o644)
		s := rules.DefaultSelectors(3)
		s.Audio, s.Subtitles, s.SkipSmall = audio, subs, false
		tmpl := s.Template()
		tmpl.Languages = langs
		st, id := realJob(t, p, 5)
		w := realWorker(st, media.OSExec{})
		w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
		if err := w.run(t); err != nil {
			t.Fatal(err)
		}
		if j, _ := st.Get(id); j.Status != jobs.Done {
			t.Fatalf("%s %q", j.Status, j.Error)
		}
		return probe(t, p)
	}
	for _, c := range []struct{ to, codec string }{{"aac", "aac"}, {"eac3", "eac3"}, {"opus", "opus"}} {
		t.Run(c.to, func(t *testing.T) {
			info := run(t, c.to, "keep_all", "")
			a := info.Audio()
			if len(a) != 2 || a[0].Codec != c.codec || a[0].Channels != 6 || a[1].Codec != "ac3" {
				t.Errorf("audio %+v", a)
			}
			if len(info.Subtitles()) != 1 {
				t.Errorf("subtitles %+v", info.Subtitles())
			}
		})
	}
	t.Run("my languages", func(t *testing.T) {
		info := run(t, "my_languages", "keep_all", "eng")
		if a := info.Audio(); len(a) != 1 || a[0].Language != "eng" || a[0].Codec != "flac" {
			t.Errorf("audio %+v", a)
		}
	})
	t.Run("text only keeps text", func(t *testing.T) {
		if info := run(t, "keep_all", "text_only", ""); len(info.Subtitles()) != 1 {
			t.Errorf("subtitles %+v", info.Subtitles())
		}
	})
}

// Subtitles saved as .srt files beside the new file (slice 25b): the text ones leave the file, named
// after the new file and their language; an existing .srt with that name stops the job before anything
// is converted, unchanged.
func TestRealSubtitlesAsSrtFiles(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.srt"), []byte("1\n00:00:00,000 --> 00:00:02,000\nhello\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.srt"), []byte("1\n00:00:00,000 --> 00:00:02,000\nbonjour\n"), 0o644)
	src := filepath.Join(dir, "Movie.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=5:size=1280x720:rate=25", "-i", filepath.Join(dir, "a.srt"), "-i", filepath.Join(dir, "b.srt"),
		"-map", "0", "-map", "1", "-map", "2", "-c:v", "libx264", "-b:v", "5000k", "-c:s", "srt",
		"-metadata:s:s:0", "language=eng", "-metadata:s:s:1", "language=fre", "-disposition:s:1", "forced", src)
	s := rules.DefaultSelectors(3)
	s.Subtitles, s.SkipSmall = "srt", false
	tmpl := s.Template()
	run := func() jobs.Job {
		st, id := realJob(t, src, 5)
		w := realWorker(st, media.OSExec{})
		w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
		w.run(t)
		j, _ := st.Get(id)
		return j
	}
	// a file already there with one of the names: nothing happens
	taken := filepath.Join(dir, "Movie.fre.forced.srt")
	os.WriteFile(taken, []byte("mine"), 0o644)
	before, _ := os.ReadFile(src)
	if j := run(); j.Status != jobs.Failed || !strings.Contains(j.Error, "A subtitle file named Movie.fre.forced.srt is already there. Nothing was changed.") {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	if b, _ := os.ReadFile(taken); string(b) != "mine" {
		t.Error("the existing .srt was changed")
	}
	if b, _ := os.ReadFile(src); string(b) != string(before) {
		t.Error("the original was changed")
	}
	if _, err := os.Stat(filepath.Join(dir, "Movie.eng.srt")); err == nil {
		t.Error("a half set of subtitle files was left")
	}
	os.Remove(taken)
	// now it goes through
	if j := run(); j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	if n := len(probe(t, src).Subtitles()); n != 0 {
		t.Errorf("%d subtitles still inside", n)
	}
	for name, want := range map[string]string{"Movie.eng.srt": "hello", "Movie.fre.forced.srt": "bonjour"} {
		if b, _ := os.ReadFile(filepath.Join(dir, name)); !strings.Contains(string(b), want) {
			t.Errorf("%s: %q", name, b)
		}
	}
	if tempLeft(src) {
		t.Error("temp left")
	}
}
