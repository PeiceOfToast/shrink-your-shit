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

// W7 with real ffmpeg: a graphics encoder that refuses an option it does not take (no device set here) fails for real.
// The Convert step's choices decide what happens next, and the job log names the rung that made the file.
func TestRealHardwareChoicesOnConvert(t *testing.T) {
	needTools(t)
	badDevice := func(string, bool, *rules.HDR) (string, string) { return "hevc_vaapi", "" }

	// Each case converts its own source: a converted file is replaced in place, so a shared one would be skipped.
	run := func(t *testing.T, hardware, failMode string) (*jobs.Store, int64, []string) {
		t.Helper()
		src := filepath.Join(t.TempDir(), "clip.mkv")
		runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=320x240:rate=25", "-c:v", "libx264", "-pix_fmt", "yuv420p", src)
		st, id := realJob(t, src, 3)
		w := realWorker(st, media.OSExec{})
		w.Encoder = badDevice
		sel := rules.DefaultSelectors(3)
		sel.Hardware, sel.HardwareFail = hardware, failMode
		tmpl := sel.Template()
		w.Template = func(jobs.Job) (rules.Template, int64, error) { return tmpl, 0, nil }
		w.run(t) // a failed job returns its error; the status and log are the check
		lines, err := st.LogLines(id)
		if err != nil {
			t.Fatal(err)
		}
		var texts []string
		for _, l := range lines {
			texts = append(texts, l.Text)
		}
		return st, id, texts
	}
	has := func(texts []string, sub string) bool {
		for _, s := range texts {
			if strings.Contains(s, sub) {
				return true
			}
		}
		return false
	}

	t.Run("auto steps down to the CPU rung and the same checks run", func(t *testing.T) {
		st, id, log := run(t, "auto", "")
		if j, _ := st.Get(id); j.Status != jobs.Done {
			t.Fatalf("status %s, error %q", j.Status, j.Error)
		}
		if !has(log, "hevc_vaapi failed (") || !has(log, "converted on the processor") {
			t.Errorf("the job log does not name the step down:\n%s", strings.Join(log, "\n"))
		}
		if !has(log, "Comparing picture quality with the original") {
			t.Error("the checks did not run on the CPU file")
		}
	})

	t.Run("CPU only never tries the graphics encoder", func(t *testing.T) {
		st, id, log := run(t, "cpu", "")
		if j, _ := st.Get(id); j.Status != jobs.Done {
			t.Fatalf("status %s, error %q", j.Status, j.Error)
		}
		if !has(log, "CPU only, as the Convert step says") {
			t.Errorf("no CPU-only line:\n%s", strings.Join(log, "\n"))
		}
		if has(log, "hevc_vaapi") {
			t.Errorf("the graphics encoder was named:\n%s", strings.Join(log, "\n"))
		}
	})

	t.Run("hardware only with a failing card fails the job in plain words", func(t *testing.T) {
		st, id, log := run(t, "only", "")
		j, _ := st.Get(id)
		if j.Status != jobs.Failed || !strings.Contains(j.Error, "Hardware only: the graphics encoder failed") {
			t.Fatalf("status %s, error %q", j.Status, j.Error)
		}
		if has(log, "converted on the processor") {
			t.Errorf("the processor converted it:\n%s", strings.Join(log, "\n"))
		}
	})

	t.Run("fail with a failing card fails the job in plain words", func(t *testing.T) {
		st, id, log := run(t, "auto", "fail")
		j, _ := st.Get(id)
		if j.Status != jobs.Failed || !strings.Contains(j.Error, "set to fail rather than convert on the processor") {
			t.Fatalf("status %s, error %q", j.Status, j.Error)
		}
		if has(log, "converted on the processor") {
			t.Errorf("the processor converted it:\n%s", strings.Join(log, "\n"))
		}
	})
}
