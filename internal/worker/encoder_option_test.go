// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// realLibopusRefusal is what ffmpeg 7.0.2 prints when a -global_quality reaches libopus through a video
// output (checked by hand, A10).
const realLibopusRefusal = "[libopus @ 0x1] Quality-based encoding not supported, please specify a bitrate and VBR setting.\n" +
	"[aost#0:1/libopus @ 0x2] Error while opening encoder - maybe incorrect parameters such as bit_rate, rate, width or height.\n" +
	"Conversion failed!\n"

// A10 user bug: a graphics encode ffmpeg refuses because of an encoder option fails there. The card is not
// blamed, the processor is not tried — on the full pipeline or on the rung below it — and the job reports its
// settings as the fault.
func TestAnEncoderOptionRefusalIsNotBlamedOnTheGraphicsCard(t *testing.T) {
	for _, rung := range []struct {
		name    string
		decodes bool // the family decodes the source: the first attempt is the full GPU pipeline
	}{
		{"full GPU pipeline", true},
		{"processor decoding for the graphics encoder", false},
	} {
		t.Run(rung.name, func(t *testing.T) {
			s := newScenario(t)
			s.failEncoder = "hevc_qsv"
			s.fullGPUStderr = realLibopusRefusal
			w := s.worker(Replace)
			var asked string
			w.GraphicsDecodes = decodeHook(rung.decodes, &asked)
			w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_qsv", "" }
			w.RunOne(context.Background()) // the job fails: that is the point
			if j := s.job(); j.Status != jobs.Failed {
				t.Fatalf("status %s, error %q; an option error is not retried", j.Status, j.Error)
			}
			if enc := encodeCalls(s); len(enc) != 1 {
				t.Fatalf("encodes, want one (no step down, no processor retry):\n%s", strings.Join(enc, "\n"))
			}
			for _, words := range []string{"full GPU pipeline failed", "converted on the processor"} {
				if n := logCount(t, s, words); n != 0 {
					t.Errorf("%q in the job log %d times; an option error is the settings' fault", words, n)
				}
			}
			f, err := s.store.FailureOf(s.id)
			if err != nil || f == nil {
				t.Fatalf("failure summary: %v (%v)", f, err)
			}
			if code := jobs.FailureCode(jobs.ReasonConvertFailed, f.KeyLines); code != "settings_rejected" {
				t.Errorf("failure code %s from %q; want settings_rejected", code, strings.Join(f.KeyLines, " / "))
			}
		})
	}
}

// A10: once a graphics encode falls back, the job's page shows the command that is running, not the one that
// just failed: while the retry ran the step still named the full GPU pipeline's command.
func TestTheJobLogNamesTheCommandThatRanAfterAFallback(t *testing.T) {
	s := newScenario(t)
	s.failFullGPU = true
	w := s.worker(Replace)
	var asked string
	w.GraphicsDecodes = decodeHook(true, &asked)
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_qsv", "" }
	var during string
	s.onEncode = func() {
		if len(encodeCalls(s)) != 2 { // the second encode: the retry, while it runs
			return
		}
		steps, err := s.store.RunSteps(s.id)
		if err != nil {
			t.Errorf("RunSteps: %v", err)
			return
		}
		for _, st := range steps {
			if st.Name == "encode" {
				during = st.Command
			}
		}
	}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if during == "" {
		t.Fatal("the retry recorded no command while it ran")
	}
	if strings.Contains(during, "-hwaccel") || !strings.Contains(during, "hevc_qsv") {
		t.Errorf("while the fall-back ran, the job log showed %q; want the command that ran", during)
	}
	steps, err := s.store.RunSteps(s.id)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range steps {
		if st.Name == "encode" && (strings.Contains(st.Command, "-hwaccel") || !strings.Contains(st.Command, "hevc_qsv")) {
			t.Errorf("after the retry, the job log shows %q; want the command that ran", st.Command)
		}
	}
}
