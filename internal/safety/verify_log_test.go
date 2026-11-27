// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media/fake"
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

// TestSourceCheckLogsItsVerdict: the original's check writes one decision line too, so the job log
// shows the damaged source the run kept.
func TestSourceCheckLogsItsVerdict(t *testing.T) {
	bus, err := events.New(events.Options{Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bus.Close(context.Background()) })
	ch, unsub := bus.Subscribe(events.Filter{Kind: events.KindDecision, JobID: 7})
	defer unsub()

	stderr := "[h264 @ 0x2] [error] error while decoding MB 27 15\n" +
		"[verbose]   Input stream #0:0 (video): 500 packets read (1000 bytes); 470 frames decoded; 0 decode errors;\n"
	m := &fake.Media{Captures: []fake.CaptureScript{{Stderr: []byte(stderr)}}}
	v, err := SourceCheck(context.Background(), m, Recorder{Bus: bus, JobID: 7}, "/original.mkv", sourceInfo(20*time.Second), time.Minute)
	if err != nil || v.OK || v.Code != CodeOriginalDamaged {
		t.Fatalf("verdict = %+v, %v; want the damaged original kept", v, err)
	}
	select {
	case e := <-ch:
		if e.Source != "safety" || e.Code != CodeOriginalDamaged || e.Level != events.LevelWarn || e.NextAction != NextKeepOriginal {
			t.Errorf("line = %+v; want the source check's no at warn with keep_original", e)
		}
		if want := "original: " + v.Message; e.Message != want {
			t.Errorf("message = %q, want %q", e.Message, want)
		}
		if e.Data["detail"] == nil {
			t.Errorf("line = %+v; want the detail for the drawer", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no decision line for the source check")
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
