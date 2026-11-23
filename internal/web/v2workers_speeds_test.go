// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// F7: the workers list says how fast each worker's encoders ran, read from its finished conversions: the
// median realtime multiple and frames per second, and how many conversions that is.
func TestWorkersListShowsEncoderSpeeds(t *testing.T) {
	srv, st := workersServer(t)
	st.SetPaused(false) // this program's worker is on (S2: a new install starts stopped)
	l, err := st.AddLibrary("m", t.TempDir(), rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	conv := func(encoder string, speed, fps float64) {
		n++
		id, err := st.Enqueue(jobs.Job{Library: "m", LibraryID: l.ID, Path: filepath.Join(l.Path, fmt.Sprintf("%d.mkv", n))})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Claim(jobs.LocalWorker); err != nil {
			t.Fatal(err)
		}
		if err := st.RecordRunStep(id, jobs.RunStep{Index: 0, Name: "encode", Encoder: encoder, Speed: speed, FPS: fps}); err != nil {
			t.Fatal(err)
		}
		if err := st.Complete(id, filepath.Join(l.Path, "out.mkv"), 1); err != nil {
			t.Fatal(err)
		}
	}
	conv("hevc_qsv", 4.006, 118.44)
	conv("hevc_qsv", 2, 0) // its fps is unknown: the median is over the one that said
	conv("libx265", 0.5, 0)

	_, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workers"})
	got := decode[struct {
		EncoderSpeeds []struct {
			Worker, Encoder string
			Speed, FPS      float64
			Samples         int
		} `json:"encoderSpeeds"`
	}](t, b)
	type speed struct {
		Worker, Encoder string
		Speed, FPS      float64
		Samples         int
	}
	want := []speed{
		{Worker: jobs.LocalWorker, Encoder: "hevc_qsv", Speed: 3, FPS: 118.4, Samples: 2}, // 3.003x, and 118.44 fps, rounded
		{Worker: jobs.LocalWorker, Encoder: "libx265", Speed: 0.5, Samples: 1},
	}
	if len(got.EncoderSpeeds) != len(want) {
		t.Fatalf("encoderSpeeds %s, want %+v", b, want)
	}
	for i, w := range want {
		g := got.EncoderSpeeds[i]
		if g.Worker != w.Worker || g.Encoder != w.Encoder || g.Speed != w.Speed || g.FPS != w.FPS || g.Samples != w.Samples {
			t.Errorf("speed %d: %+v, want %+v", i, g, w)
		}
	}
	if n := strings.Count(b, `"fps"`); n != 1 { // only the pair that measured it has an fps
		t.Errorf(`%d "fps" fields, want 1 (the pair that measured it): %s`, n, b)
	}
}
