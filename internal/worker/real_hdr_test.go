// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// S3 with real ffmpeg: an HDR10 file converts with its HDR intact (ffprobe on the new file shows PQ,
// BT.2020, the mastering display and the light levels), a repackaged HDR10 HEVC file keeps them too,
// and an SDR file is converted as before.
func TestRealHDR10(t *testing.T) {
	needTools(t)
	const md = "G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50)"
	run := func(t *testing.T, src string) (*jobs.Store, int64) {
		tmpl := rules.BasicTemplate(3, "")
		tmpl.Languages = "eng"
		st, id := realJob(t, src, 3)
		w := realWorker(st, media.OSExec{})
		w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
		w.KeepDays, w.Journal = func() int { return 7 }, st
		w.run(t)
		if j, _ := st.Get(id); j.Status != jobs.Done {
			t.Fatalf("%s %q", j.Status, j.Error)
		}
		return st, id
	}
	hdrOK := func(t *testing.T, v *media.Stream) {
		t.Helper()
		if v == nil || v.Codec != "hevc" || v.BitDepth != 10 || v.ColorTransfer != "smpte2084" || v.ColorPrimaries != "bt2020" ||
			v.MasteringDisplay != md || v.CLL != "1000,400" {
			t.Errorf("new file's video %+v", v)
		}
	}
	t.Run("HDR10 H.264 is converted to 10-bit HEVC with its HDR", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "Movie.mkv")
		// x264 writes the VUI and the HDR10 SEI itself (in the bitstream only: the first-frame probe finds them)
		runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=1280x720:rate=25", "-f", "lavfi", "-i", "sine=duration=3",
			"-c:v", "libx264", "-b:v", "5000k", "-pix_fmt", "yuv420p10le",
			"-x264-params", "colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:mastering-display="+md+":cll=1000,400",
			"-c:a", "aac", "-ac", "2", "-metadata:s:a:0", "language=eng", src)
		if v := probe(t, src).Video(); v.ColorTransfer != "smpte2084" || v.MasteringDisplay != md || v.CLL != "1000,400" {
			t.Fatalf("the test file is not HDR10 as meant: %+v", v)
		}
		st, id := run(t, src)
		hdrOK(t, probe(t, src).Video())
		if snap := plan(t, st, id); snap.Video.HDR == nil || snap.Video.HDR.MasterDisplay != md {
			t.Errorf("plan %+v", snap.Video.HDR)
		}
	})
	t.Run("HDR10 HEVC MP4 repackaged keeps its HDR", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "Movie.mp4")
		runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=1280x720:rate=25", "-f", "lavfi", "-i", "sine=duration=3",
			"-c:v", "libx265", "-preset", "ultrafast", "-tag:v", "hvc1", "-pix_fmt", "yuv420p10le",
			"-x265-params", "log-level=error:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:hdr10=1:master-display="+md+":max-cll=1000,400",
			"-c:a", "aac", "-ac", "2", "-metadata:s:a:0", "language=eng", src)
		st, id := run(t, src)
		hdrOK(t, probe(t, filepath.Join(dir, "Movie.mkv")).Video())
		if snap := plan(t, st, id); !snap.Video.Copy {
			t.Error("the video was encoded, not copied")
		}
	})
	t.Run("SDR is converted as before", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "Movie.mkv")
		runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=1280x720:rate=25", "-f", "lavfi", "-i", "sine=duration=3",
			"-c:v", "libx264", "-b:v", "5000k", "-c:a", "aac", "-ac", "2", "-metadata:s:a:0", "language=eng", src)
		st, id := run(t, src)
		if v := probe(t, src).Video(); v.Codec != "hevc" || v.BitDepth != 8 || v.ColorTransfer == "smpte2084" || v.MasteringDisplay != "" {
			t.Errorf("%+v", v)
		}
		if snap := plan(t, st, id); snap.Video.HDR != nil {
			t.Errorf("SDR plan has HDR %+v", snap.Video.HDR)
		}
	})
}

// S5: a real job writes its step summary and its run log.
func TestRealRunLogAndSteps(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "Movie.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=640x360:rate=25", "-c:v", "libx264", "-b:v", "3000k", src)
	st, id := realJob(t, src, 3)
	w := realWorker(st, media.OSExec{})
	w.Template = func(jobs.Job) (rules.Template, error) { return rules.BasicTemplate(3, "keep_all"), nil }
	w.KeepDays, w.Journal = func() int { return 7 }, st
	w.run(t)
	if j, _ := st.Get(id); j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	steps, _ := st.Steps(id)
	var names []string
	for _, s := range steps {
		if !s.OK || s.Words == "" {
			t.Errorf("step %+v", s)
		}
		names = append(names, s.Step)
	}
	if strings.Join(names, ",") != "converted,verified,replaced" {
		t.Errorf("steps %v", names)
	}
	text := logTextOf(st, id)
	if !strings.Contains(text, "started") || !strings.Contains(text, "done") {
		t.Errorf("run log %q", text)
	}
}
