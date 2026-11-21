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

// The names of the audio tracks on real files (F6): a track the plan makes is named for what it is; a copied
// track keeps the name it had in the source.
func TestRealTrackTitles(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	// video; a 5.1 FLAC in English with an old title; a small stereo AC-3 in Japanese with its own title
	src := filepath.Join(dir, "movie.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=5:size=1280x720:rate=25",
		"-f", "lavfi", "-i", "sine=duration=5:frequency=440", "-f", "lavfi", "-i", "sine=duration=5:frequency=660",
		"-map", "0", "-map", "1", "-map", "2",
		"-c:v", "libx264", "-b:v", "5000k", "-c:a:0", "flac", "-ac:a:0", "6", "-c:a:1", "ac3", "-b:a:1", "96k", "-ac:a:1", "2",
		"-metadata:s:a:0", "language=eng", "-metadata:s:a:0", "title=Old DTS title",
		"-metadata:s:a:1", "language=jpn", "-metadata:s:a:1", "title=Japanese commentary", src)
	run := func(t *testing.T, audio string) media.Info {
		t.Helper()
		p := filepath.Join(t.TempDir(), "m.mkv")
		b, _ := os.ReadFile(src)
		os.WriteFile(p, b, 0o644)
		s := rules.DefaultSelectors(3)
		s.Audio, s.OnlyMyLanguages, s.Subtitles = audio, false, "keep_all"
		tmpl := s.Template()
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
	titles := func(info media.Info) string {
		var out []string
		for _, a := range info.Audio() {
			out = append(out, a.Title)
		}
		return strings.Join(out, " | ")
	}
	// the surround converted to E-AC-3 (5.1 kept): it has its own title, which ffmpeg keeps; the copied stereo keeps its own name
	if info := run(t, "eac3_surround"); titles(info) != "Old DTS title | Japanese commentary" {
		t.Errorf("eac3_surround: titles %q", titles(info))
	}
	// the surround copied, a stereo AAC added from it: an added track is a new track, so it gets its automatic name (not the source's),
	// and it sits straight after the surround it came from (slice 17)
	if info := run(t, "stereo_aac_surround"); titles(info) != "Old DTS title | Stereo (AAC) | Japanese commentary" {
		t.Errorf("stereo_aac_surround: titles %q", titles(info))
	}
}
