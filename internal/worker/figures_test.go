// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// F7: the local worker measures its own encode: the media's length over the encode's wall time, its frames
// over the same, and the encoder the command names for the plan's main video.
func TestEncodeFiguresMeasureTheRungThatRan(t *testing.T) {
	start := time.Unix(1_000_000, 0)
	w := &Worker{Now: func() time.Time { return start.Add(20 * time.Second) }}
	plan := rules.Plan{Video: rules.VideoEncode{Index: 1}}
	args := []string{"-i", "in.mkv", "-c:v:0", "libx265", "-c:v:1", "hevc_qsv", "out.mkv"}
	// 300 seconds of media in 20: 15x; 6000 frames in 20: 300 fps
	if f := w.figuresOf(jobs.Job{DurationSec: 300}, args, plan, start, 6000, nil); f.encoder != "hevc_qsv" || f.speed != 15 || f.fps != 300 {
		t.Errorf("figures %+v, want hevc_qsv at 15x and 300 fps", f)
	}
	// nothing measured it: a failed encode, a copied video, a length the job does not know
	for _, c := range []struct {
		name  string
		job   jobs.Job
		plan  rules.Plan
		terr  error
		frame int64
	}{
		{"failed", jobs.Job{DurationSec: 300}, plan, errors.New("exit status 1"), 6000},
		{"copy", jobs.Job{DurationSec: 300}, rules.Plan{Video: rules.VideoEncode{Copy: true}}, nil, 6000},
		{"no length", jobs.Job{}, plan, nil, 6000},
	} {
		if f := w.figuresOf(c.job, args, c.plan, start, c.frame, c.terr); f != (encodeFigures{}) {
			t.Errorf("%s: figures %+v, want none", c.name, f)
		}
	}
	// the frames are unknown (an ffmpeg that reported no count): the speed still is
	if f := w.figuresOf(jobs.Job{DurationSec: 300}, args, plan, start, -1, nil); f.speed != 15 || f.fps != 0 {
		t.Errorf("unknown frames: figures %+v, want the speed only", f)
	}
}

// F7: a finished local conversion keeps its speed on the encode step, measured from its own run (so the
// job's log and the remembered speeds both have it).
func TestASuccessfulEncodeRecordsItsSpeed(t *testing.T) {
	s := newScenario(t)
	if err := s.run(Replace); err != nil {
		t.Fatal(err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("job %s: %q", j.Status, j.Error)
	}
	steps, err := s.store.RunSteps(s.id)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range steps {
		if st.Name != "encode" {
			continue
		}
		if st.Encoder == "" || st.Speed <= 0 {
			t.Errorf("encode step %+v, want the encoder and a realtime multiple", st)
		}
		return
	}
	t.Fatalf("no encode step: %+v", steps)
}

// F7: the speed a remote worker measures comes back with its answer, for the server to keep on the step
// (its own clock around the conversion also counts the transfers, so it cannot measure this).
func TestRemoteAnswersCarryTheirOwnSpeed(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	job, err := s.store.Claim("remote-worker")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.store.LeaseOf(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	rj := NewRemoteJob(lease)
	ran := []string{"ffmpeg", "-i", s.src, "-c:v:0", "hevc_qsv", s.src + ".new.mkv"}
	go func() {
		<-rj.Planned()
		rj.Deliver(RemoteResult{Command: ran, Encoder: "hevc_qsv", Speed: 3.5, FPS: 118})
	}()
	if _, _, err := rj.convert(context.Background(), w, job, rules.Snapshot{}, "/in.mkv", filepath.Join(t.TempDir(), "out.mkv")); err != nil {
		t.Fatal(err)
	}
	if f := rj.figures(); f.encoder != "hevc_qsv" || f.speed != 3.5 || f.fps != 118 {
		t.Errorf("figures %+v, want the remote worker's own 3.5x and 118 fps", f)
	}
}

// F7: the encode step keeps the figures it is given, so the remembered speeds can be read from the steps.
func TestRecordEncodeKeepsTheSpeed(t *testing.T) {
	st, id := realJob(t, filepath.Join(t.TempDir(), "x.mkv"), 10)
	w := &Worker{Queue: st}
	w.recordEncode(id, []string{"-c:v:0", "hevc_qsv"}, time.Now(), "", nil, "", encodeFigures{encoder: "hevc_qsv", speed: 3.5, fps: 118})
	steps, err := st.RunSteps(id)
	if err != nil || len(steps) != 1 {
		t.Fatalf("steps %+v, %v", steps, err)
	}
	if steps[0].Encoder != "hevc_qsv" || steps[0].Speed != 3.5 || steps[0].FPS != 118 {
		t.Errorf("encode step %+v, want hevc_qsv at 3.5x and 118 fps", steps[0])
	}
}
