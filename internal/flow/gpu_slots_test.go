// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/hw"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media/fake"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// The graphics card's places (L-HW-8) in a real run: one count for the process, 0 means the processor and a
// reason, and every path gives the place back. The count's own rules (waiting, releasing twice, a stop while
// waiting) are internal/hw's tests.

// slotWatcher records how many places were in use each time an encode started: the card's place must be held
// while the card encodes and given back by the time the processor does.
type slotWatcher struct {
	*fake.Media
	during []int
}

func (s *slotWatcher) Run(ctx context.Context, argv []string, onProgress func(media.Progress)) (media.Result, error) {
	s.during = append(s.during, hw.Running())
	return s.Media.Run(ctx, argv, onProgress)
}

// slottedRun is ladderRun with the worker's graphics slots and a change to the safety fake.
func slottedRun(t *testing.T, pick func(flow.Want) (flow.Attempt, error), runs []fake.RunScript,
	gpuSlots func() int, tweak func(*fakeSafety)) (flow.Result, *fake.Media, *slotWatcher, *recorder) {
	t.Helper()
	root, src, info := libraryFile(t, "clip.mkv", "h264")
	want := filepath.Join(root, "clip-shrunk.mkv")
	m := &fake.Media{Probes: map[string]media.Info{src: info, want: info}, Runs: runs}
	sw := &slotWatcher{Media: m}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	if tweak != nil {
		tweak(sf)
	}
	bus := &recorder{}
	env := newEnv(sw, sf, bus, starterFlow(t), &bytes.Buffer{})
	env.Pick = pick
	env.GPUSlots = gpuSlots
	return flow.Run(context.Background(), runInput(t, root, src), env), m, sw, bus
}

// Graphics slots at 0: the processor converts the file, and the log says why once (L-HW-8).
func TestGraphicsSlotsAtZeroUseTheProcessor(t *testing.T) {
	before := hw.Running()
	res, m, sw, bus := slottedRun(t, pickRungs(hybridRung()), nil, func() int { return 0 }, nil)
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("outcome = %q (%s: %s), want smaller", res.Outcome, res.Reason, res.Message)
	}
	argvs := m.Argvs()
	if len(argvs) != 1 || !strings.Contains(strings.Join(argvs[0], " "), "libx265") ||
		strings.Contains(strings.Join(argvs[0], " "), "hevc_qsv") {
		t.Fatalf("encodes, want the processor alone:\n%v", argvs)
	}
	if len(sw.during) != 1 || sw.during[0] != before {
		t.Errorf("places in use while the processor encoded = %v, want %d (no card place taken)", sw.during, before)
	}
	count := 0
	for _, line := range said(bus, events.KindWarning) {
		if strings.Contains(line, "the graphics slots are set to 0") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("graphics-slots-0 line said %d times, want once", count)
	}
	if n := hw.Running(); n != before {
		t.Errorf("places after the job: %d, want %d", n, before)
	}
}

// A job that converts on the card takes a place while the card encodes, and the place is back when the job
// ends (L-HW-8).
func TestAPassingGraphicsJobGivesTheCardBack(t *testing.T) {
	before := hw.Running()
	res, _, sw, _ := slottedRun(t, pickRungs(fullRung()), nil, func() int { return 2 }, nil)
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("outcome = %q (%s: %s), want smaller", res.Outcome, res.Reason, res.Message)
	}
	if len(sw.during) != 1 || sw.during[0] != before+1 {
		t.Errorf("places in use while the card encoded = %v, want %d", sw.during, before+1)
	}
	if n := hw.Running(); n != before {
		t.Errorf("places after a passing job: %d, want %d", n, before)
	}
}

// A later check that stops the job still gives the card's place back (L-HW-8): the encode passes, the result
// check does not, and the job ends on the original.
func TestAJobStoppedByALaterCheckGivesTheCardBack(t *testing.T) {
	before := hw.Running()
	res, _, _, _ := slottedRun(t, pickRungs(fullRung()), nil, func() int { return 2 }, func(sf *fakeSafety) {
		sf.verify = safety.Verdict{OK: false}
	})
	if res.Outcome != flow.OutcomeBiggerKept {
		t.Fatalf("outcome = %q (%s: %s), want the original kept", res.Outcome, res.Reason, res.Message)
	}
	if n := hw.Running(); n != before {
		t.Errorf("places after a job the check stopped: %d, want %d", n, before)
	}
}

// A staging failure below the checks never leaves the card held (L-HW-8).
func TestAJobWhoseStagingFailsAfterTheChecksGivesTheCardBack(t *testing.T) {
	before := hw.Running()
	res, m, sw, _ := slottedRun(t, pickRungs(fullRung()), nil, func() int { return 2 }, func(sf *fakeSafety) {
		sf.stageE = errors.New("cannot stage beside the original")
	})
	if res.Reason != "save.stage_failed" {
		t.Fatalf("reason = %q (%s), want save.stage_failed", res.Reason, res.Message)
	}
	if len(m.Argvs()) != 0 || len(sw.during) != 0 {
		t.Errorf("encodes ran after a staging failure: %v", m.Argvs())
	}
	if n := hw.Running(); n != before {
		t.Errorf("places after a staging failure: %d, want %d", n, before)
	}
}

// The place goes back when the ladder reaches the processor: the card's failure hands the file on, and the
// processor's encode starts with no place held (L-HW-8).
func TestTheCardIsGivenBackBeforeTheProcessorTakesOver(t *testing.T) {
	before := hw.Running()
	res, _, sw, _ := slottedRun(t, pickRungs(hybridRung()),
		[]fake.RunScript{failRun("[hevc_qsv @ 0x55] Error creating a MFX session: -9."), {}},
		func() int { return 2 }, nil)
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("outcome = %q (%s: %s), want smaller", res.Outcome, res.Reason, res.Message)
	}
	if len(sw.during) != 2 || sw.during[0] != before+1 || sw.during[1] != before {
		t.Errorf("places at the two encodes = %v, want [%d %d] (card, then processor with none)", sw.during, before+1, before)
	}
	if n := hw.Running(); n != before {
		t.Errorf("places after the job: %d, want %d", n, before)
	}
}
