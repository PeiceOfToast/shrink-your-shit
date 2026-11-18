// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

type jobMeta struct {
	Metadata struct {
		Before *struct{ VideoCodec string } `json:"before"`
		After  *struct{ VideoCodec string } `json:"after"`
	}
}

// dev-review 04:27Z: a replaced file's job says what the original was (before) and what it became
// (after): before is the original as probed when it was planned, not the new file now at its name.
func TestJobBeforeIsTheOriginal(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		if os.Getenv("SYP_REQUIRE_FFMPEG") == "1" {
			t.Fatal("ffmpeg is needed here")
		}
		t.Skip("no ffmpeg")
	}
	srv, st, _, _ := libServer(t, false)
	dir := t.TempDir()
	lib, _ := st.AddLibrary("Movies", dir, rules.SliderTemplate(3), true)
	src := filepath.Join(lib.Path, "clip.mp4")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=duration=2:size=640x360:rate=25",
		"-c:v", "libx264", "-b:v", "3000k", src).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: src, DurationSec: 2})
	r := rules.Default()
	r.Preset, r.CRF = "ultrafast", 28
	sel := rules.DefaultSelectors(3)
	sel.FileType = "keep" // replace at the same name: the reported case
	tmpl := sel.Template()
	w := &worker.Worker{ID: "t", Queue: st, Exec: media.OSExec{}, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Rule: r, Mode: worker.Replace,
		Template: func(jobs.Job) (rules.Template, error) { return tmpl, nil }, KeepDays: func() int { return 7 }, Journal: st}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j, _ := st.Get(id); j.Status != jobs.Done || j.OutputPath != src {
		t.Fatalf("%s %q -> %s", j.Status, j.Error, j.OutputPath)
	}
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10)})
	var d jobMeta
	json.Unmarshal([]byte(body), &d)
	if d.Metadata.Before == nil || d.Metadata.Before.VideoCodec != "h264" || d.Metadata.After == nil || d.Metadata.After.VideoCodec != "hevc" {
		t.Errorf("before/after %s", body)
	}

	// a job planned before the original was recorded, replaced at the same name: before is unknown
	// (null), never the new file now at its name
	old, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "old.mkv")})
	st.Claim("w")
	st.PutProbe(filepath.Join(lib.Path, "old.mkv"), 10, 1, `{"videoCodec":"hevc"}`)
	st.Complete(old, filepath.Join(lib.Path, "old.mkv"), 10)
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(old, 10)})
	d = jobMeta{}
	json.Unmarshal([]byte(body), &d)
	if d.Metadata.Before != nil {
		t.Errorf("an older replaced job's before should be null: %s", body)
	}
}
