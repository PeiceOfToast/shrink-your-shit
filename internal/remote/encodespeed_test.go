// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// F7: the worker measures its own encode (the server's clock also counts the transfers) and sends the
// figures with its answer. A copied video measured nothing.
func TestConvertMeasuresItsOwnRun(t *testing.T) {
	old := ProgressEvery
	ProgressEvery = 0 // every progress line is seen
	defer func() { ProgressEvery = old }()
	convert := func(copyVideo bool) (encodeSpeed, []string, error) {
		srv, _ := recordingServer(t)
		cl := &Client{Server: srv.URL, Version: "test"}
		l := &Lease{ID: "L1"}
		l.Job.DurationSec = 100
		exec := progressExec{lines: []string{"frame=6000", "out_time_us=100000000"}}
		cv := Converter{Exec: exec, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Work: t.TempDir()}
		plan := rules.Plan{Video: rules.VideoEncode{Codec: "libx264", CRF: 20, Preset: "medium", Copy: copyVideo}}
		out := filepath.Join(t.TempDir(), "out.mkv")
		if err := os.WriteFile(out, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		_, args, speed, err := cl.convert(context.Background(), cv, l, rules.Snapshot{Plan: plan}, "/in.mkv", out)
		return speed, args, err
	}
	speed, args, err := convert(false)
	if err != nil {
		t.Fatal(err)
	}
	if speed.encoder == "" || speed.encoder != rules.VideoEncoderIn(args, 0) {
		t.Fatalf("encoder %q from %v, want the one the command ran", speed.encoder, args)
	}
	if speed.speed <= 0 || speed.fps <= 0 {
		t.Errorf("speed %+v, want this run's realtime multiple and frames per second", speed)
	}
	if got, _, err := convert(true); err != nil || got != (encodeSpeed{}) {
		t.Errorf("a copy measured %+v, %v; want nothing", got, err)
	}
	// the answer carries them; a copy's carries none
	body := map[string]any{"ok": true}
	addSpeed(body, encodeSpeed{encoder: "hevc_qsv", speed: 3.5, fps: 118})
	if body["encoder"] != "hevc_qsv" || body["speed"] != 3.5 || body["fps"] != 118.0 {
		t.Errorf("report %v, want the encoder, speed and fps", body)
	}
	copied := map[string]any{"ok": true}
	addSpeed(copied, encodeSpeed{})
	if _, ok := copied["encoder"]; ok {
		t.Errorf("a copy's report carries %v, want none", copied)
	}
}
