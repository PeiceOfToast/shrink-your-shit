// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Remove black bars, for real: a 1920x1080 file whose picture is 1920x800 with black bars above and below
// (the bars are 140 px at the top and 140 px at the bottom, so the picture sits at y=140).
// The whole job, with the setting on, must come out 800 high.
func TestRealCropRemovesLetterboxBars(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "Letterboxed.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=1920x800:rate=25:duration=12",
		"-vf", "pad=1920:1080:0:140:black", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", src)

	info := probe(t, src)
	w := &Worker{Exec: media.OSExec{}, FFmpeg: "ffmpeg"}
	r, ok, words := w.detectCrop(context.Background(), info, src)
	if !ok || r != (rules.CropRect{W: 1920, H: 800, X: 0, Y: 140}) {
		t.Fatalf("detectCrop: got %+v ok %v (%s), want 1920x800 at 0,140", r, ok, words)
	}

	out := realCropEncode(t, src, true)
	if h := probe(t, out).Video().Height; h != 800 {
		t.Errorf("output height %d, want 800 with the bars removed", h)
	}
}

// A file whose shape changes partway (full 1080 picture, then a letterboxed one) is never cropped: the samples
// disagree, so the job keeps the picture as it is.
func TestRealCropSkipsAMidFileChangeOfShape(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "Changes.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=25:duration=6",
		"-f", "lavfi", "-i", "testsrc2=size=1920x800:rate=25:duration=6",
		"-filter_complex", "[1:v]pad=1920:1080:0:140:black[b];[0:v][b]concat=n=2:v=1:a=0,format=yuv420p[v]",
		"-map", "[v]", "-c:v", "libx264", "-preset", "ultrafast", src)

	w := &Worker{Exec: media.OSExec{}, FFmpeg: "ffmpeg"}
	if _, ok, words := w.detectCrop(context.Background(), probe(t, src), src); ok {
		t.Fatalf("detectCrop cropped a file whose shape changes: %s", words)
	}

	out := realCropEncode(t, src, true)
	if h := probe(t, out).Video().Height; h != 1080 {
		t.Errorf("output height %d, want 1080 (no crop)", h)
	}
}

// realCropEncode runs the worker on src with the Remove black bars setting on or off and returns the output.
func realCropEncode(t *testing.T, src string, removeBars bool) string {
	t.Helper()
	in := filepath.Join(t.TempDir(), "in.mkv")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in, b, 0o644); err != nil {
		t.Fatal(err)
	}
	sel := rules.DefaultSelectors(3)
	sel.RemoveBlackBars, sel.Audio, sel.OnlyMyLanguages = removeBars, "as_is", false
	if err := sel.Validate(); err != nil {
		t.Fatal(err)
	}
	st, id := realJob(t, in, 12)
	w := realWorker(st, media.OSExec{})
	tmpl := sel.Template()
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tmpl, 0, nil }
	if err := w.run(t); err != nil {
		t.Fatal(err)
	}
	j, _ := st.Get(id)
	if j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	return j.OutputPath
}
