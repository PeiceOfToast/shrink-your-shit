// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A job converted on another computer records the command that computer ran: its graphics encoder, not the
// processor's arguments the server planned (the job log showed libx265 for a hevc_qsv job).
func TestRemoteJobRecordsTheCommandItRan(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, error) { return rules.SliderTemplate(3), nil }
	job, err := s.store.Claim("remote-worker")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.store.LeaseOf(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	rj := NewRemoteJob(lease)
	ran := []string{"ffmpeg", "-i", s.src, "-c:v", "hevc_qsv", "-global_quality", "22", s.src + ".new.mkv"}
	go func() {
		<-rj.Planned()
		rj.Deliver(RemoteResult{Stderr: "boom", Command: ran, Err: errors.New("the other computer couldn't convert it")})
	}()
	w.RunRemote(context.Background(), job, rj) // the remote's failure is the job's outcome; the step is what's checked
	steps, _ := s.store.RunSteps(s.id)
	var cmd string
	for _, st := range steps {
		if st.Name == "encode" {
			cmd = st.Command
		}
	}
	if !strings.Contains(cmd, "hevc_qsv") || strings.Contains(cmd, "libx265") {
		t.Errorf("encode step command %q: want the remote's hevc_qsv command, not the server's libx265 plan", cmd)
	}
}
