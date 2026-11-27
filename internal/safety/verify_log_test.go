// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
)

// Each check that runs writes one job-log decision line, Source "safety", so a job's history shows why it kept the
// original: a passing check, then the check that said no.
func TestVerifyLogsEachCheckAsASafetyDecision(t *testing.T) {
	bus, err := events.New(events.Options{Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bus.Close(context.Background()) })
	ch, unsub := bus.Subscribe(events.Filter{Kind: events.KindDecision, JobID: 7})
	defer unsub()

	st, _ := stagedWith(t, "x")
	v, err := Verify(context.Background(), &stubProber{info: goodInfo(90 * time.Second)},
		Recorder{Bus: bus, JobID: 7}, st, goodInfo(100*time.Second), wantAll, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if v.Code != CodeTooShort {
		t.Fatalf("verdict = %+v; want too_short", v)
	}

	var got []events.Event
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case e := <-ch:
			got = append(got, e)
		case <-timeout:
			t.Fatalf("got %d decision lines, want 2: %+v", len(got), got)
		}
	}
	if got[0].Code != "check.pass" || got[0].Message != "complete: pass" || got[0].Level != events.LevelInfo {
		t.Errorf("first line = %+v; want the complete check passing at info", got[0])
	}
	if got[1].Code != CodeTooShort || got[1].Level != events.LevelWarn || got[1].NextAction != NextKeepOriginal {
		t.Errorf("second line = %+v; want too_short at warn with keep_original", got[1])
	}
	for _, e := range got {
		if e.Source != "safety" || e.Kind != events.KindDecision || e.JobID != 7 {
			t.Errorf("line %+v; want Source safety, kind decision, job 7", e)
		}
	}
}

// A zero Recorder logs nothing and changes no verdict.
func TestZeroRecorderWritesNothing(t *testing.T) {
	st, _ := stagedWith(t, "x")
	v, err := Verify(context.Background(), &stubProber{info: goodInfo(100 * time.Second)},
		Recorder{}, st, goodInfo(100*time.Second), wantAll, time.Minute)
	if err != nil || !v.OK {
		t.Fatalf("verdict = %+v, err = %v; want a pass", v, err)
	}
}
