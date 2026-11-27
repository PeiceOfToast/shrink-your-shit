// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package fake

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

func TestProbeAnswersFromFixtures(t *testing.T) {
	m := &Media{
		Probes:      map[string]media.Info{"/a.mp4": {Path: "/a.mp4", Format: media.Format{Size: 10}}},
		ProbeErrors: map[string]error{"/bad.mp4": media.ErrNotMedia},
	}

	info, err := m.Probe(context.Background(), "/a.mp4")
	if err != nil || info.Format.Size != 10 {
		t.Fatalf("known path = %+v, %v; want the fixture", info, err)
	}
	if _, err := m.Probe(context.Background(), "/bad.mp4"); !errors.Is(err, media.ErrNotMedia) {
		t.Fatalf("error path = %v, want ErrNotMedia", err)
	}
	if _, err := m.Probe(context.Background(), "/unknown.mp4"); !errors.Is(err, media.ErrNotMedia) {
		t.Fatalf("unknown path = %v, want ErrNotMedia", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Probe(ctx, "/a.mp4"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled probe = %v, want context.Canceled", err)
	}
}

// TestProbeFromJSONFixture is the fixture path the plan names: a caller sets a probe answer from
// an ffprobe-style JSON document, keyed by the path it will ask for.
func TestProbeFromJSONFixture(t *testing.T) {
	const fixture = `{"format":{"format_name":"mov,mp4","duration":"2.000000","size":10,"bit_rate":"40"},
	  "streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":320,"height":240,
	  "avg_frame_rate":"30/1","pix_fmt":"yuv420p"}]}`
	var m Media
	if err := m.ProbeJSON("/library/clip.mp4", []byte(fixture)); err != nil {
		t.Fatalf("ProbeJSON: %v", err)
	}
	info, err := m.Probe(context.Background(), "/library/clip.mp4")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if info.Path != "/library/clip.mp4" || info.Video[0].Codec != "h264" || info.Format.Duration != 2*time.Second {
		t.Errorf("info = %+v, want the fixture's h264 clip", info)
	}
	if _, err := m.Probe(context.Background(), "/library/other.mp4"); !errors.Is(err, media.ErrNotMedia) {
		t.Errorf("unscripted path = %v, want ErrNotMedia", err)
	}
	if err := m.ProbeJSON("/x", []byte("not json")); err == nil {
		t.Error("a broken fixture returned no error")
	}
}

func TestRunScriptsProgressAndResult(t *testing.T) {
	m := &Media{Runs: []RunScript{{
		Progress: []media.Progress{
			{OutTime: time.Second, TotalSize: 100},
			{OutTime: 2 * time.Second, TotalSize: 200},
		},
		Result: media.Result{ExitCode: 0, KeyLines: []string{"done"}},
	}}}

	var seen []media.Progress
	result, err := m.Run(context.Background(), []string{"ffmpeg", "-i", "in.mp4", "out.mp4"}, func(p media.Progress) {
		seen = append(seen, p)
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 0 || len(result.KeyLines) != 1 {
		t.Errorf("result = %+v, want exit 0 and one key line", result)
	}
	if len(seen) != 2 || seen[1].TotalSize != 200 {
		t.Errorf("progress = %+v, want the two scripted blocks", seen)
	}
	argvs := m.Argvs()
	if len(argvs) != 1 || argvs[0][0] != "ffmpeg" || argvs[0][3] != "out.mp4" {
		t.Errorf("argv = %v, want the call recorded", argvs)
	}
}

func TestRunHangsUntilCancelled(t *testing.T) {
	m := &Media{Runs: []RunScript{{Hang: true}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := m.Run(ctx, []string{"ffmpeg"}, nil)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("Run returned before the cancel: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a hung Run did not stop when its context ended")
	}
}

// TestRunRepeatsTheLastScript covers the retry case: a caller runs the same fake twice and gets
// the script again.
func TestRunRepeatsTheLastScript(t *testing.T) {
	m := &Media{Runs: []RunScript{
		{Err: errors.New("first try fails")},
		{Result: media.Result{ExitCode: 0}},
	}}
	if _, err := m.Run(context.Background(), []string{"ffmpeg"}, nil); err == nil {
		t.Fatal("first run returned no error")
	}
	for i := 0; i < 2; i++ {
		if _, err := m.Run(context.Background(), []string{"ffmpeg"}, nil); err != nil {
			t.Fatalf("run %d: %v, want the last script repeated", i+2, err)
		}
	}
	if got := len(m.Argvs()); got != 3 {
		t.Errorf("recorded runs = %d, want 3", got)
	}
}

// TestTheZeroValueIsSafe: a test that only needs one side of the pair can leave the media zero.
func TestTheZeroValueIsSafe(t *testing.T) {
	var m Media
	if _, err := m.Probe(context.Background(), "/x.mp4"); !errors.Is(err, media.ErrNotMedia) {
		t.Fatalf("zero probe = %v, want ErrNotMedia", err)
	}
	if _, err := m.Run(context.Background(), []string{"ffmpeg"}, nil); err != nil {
		t.Fatalf("zero run = %v, want success with an empty result", err)
	}
}
