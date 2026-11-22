// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// encodeStep is the job's encode step, which every one of these scenarios has.
func (s *scenario) encodeStep() jobs.RunStep {
	s.t.Helper()
	steps, err := s.store.RunSteps(s.id)
	if err != nil {
		s.t.Fatal(err)
	}
	for _, st := range steps {
		if st.Name == "encode" {
			return st
		}
	}
	s.t.Fatalf("no encode step: %+v", steps)
	return jobs.RunStep{}
}

// F7 review2 #638 P3-1 (M3): a graphics rung that fails and steps down to the processor must measure the
// rung that ran. The clock moves 5 s for the failed rung and 10 s for the one that made the file, so the
// speed is the media's 100 s over 10 s; taking the start time once, before the ladder, would say 100/15.
func TestAFallbackMeasuresTheRungThatRan(t *testing.T) {
	s := newScenario(t)
	s.failEncoder = "hevc_qsv"
	w := s.worker(Replace)
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_qsv", "" }
	clock := time.Unix(1_700_000_000, 0)
	encodes := 0
	s.onEncode = func() {
		encodes++
		if encodes == 1 {
			clock = clock.Add(5 * time.Second) // the card's rung took 5 s and failed
			return
		}
		clock = clock.Add(10 * time.Second) // the processor's rung took 10 s and made the file
	}
	w.Now = func() time.Time { return clock }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("job %s: %q", j.Status, j.Error)
	}
	if encodes != 2 {
		t.Fatalf("encodes %d, want the card's rung then the processor's", encodes)
	}
	st := s.encodeStep()
	if st.Encoder != "libx265" || st.Speed != 10 {
		t.Errorf("encode step %+v, want the processor's 10x (100 s of media in its 10 s), not both rungs' 15 s", st)
	}
}

// F7 review2 #638 P3-2 (M4): the frames ffmpeg reports while encoding are the local fps. The fake prints
// frame=500 and the rung takes 10 s, so the step has 50 fps; dropping the frame count on the way out of the
// run would record none.
func TestALocalEncodeRecordsItsFramesPerSecond(t *testing.T) {
	s := newScenario(t)
	s.progressOut = "frame=500\nout_time_us=50000000\n"
	w := s.worker(Replace)
	clock := time.Unix(1_700_000_000, 0)
	s.onEncode = func() { clock = clock.Add(10 * time.Second) }
	w.Now = func() time.Time { return clock }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("job %s: %q", j.Status, j.Error)
	}
	st := s.encodeStep()
	if st.Encoder == "" || st.Speed != 10 || st.FPS != 50 {
		t.Errorf("encode step %+v, want a 10x encode at 50 fps (500 frames in its 10 s)", st)
	}
}

// F7 review2 #638 §2 (M5): a successful remote conversion records the speed its worker measured on the job's
// encode step, at the place the server keeps it. The two halves were tested apart (figures(), recordEncode);
// this is the join between them.
func TestARemoteConversionRecordedSpeedIsOnTheStep(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return rules.SliderTemplate(3), 0, nil }
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
		pl, ready := rj.Plan()
		if !ready {
			return
		}
		os.WriteFile(pl.Stage, []byte(s.content), 0o644) // the worker uploaded its checked file here
		rj.Deliver(RemoteResult{Command: ran, Encoder: "hevc_qsv", Speed: 3.5, FPS: 118})
	}()
	w.RunRemote(context.Background(), job, rj)
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("job %s: %q", j.Status, j.Error)
	}
	st := s.encodeStep()
	if st.Encoder != "hevc_qsv" || st.Speed != 3.5 || st.FPS != 118 {
		t.Errorf("encode step %+v, want the remote worker's own hevc_qsv at 3.5x and 118 fps", st)
	}
}
