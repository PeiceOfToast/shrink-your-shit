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

// D4c: a convert that fails while its share does not answer goes back in the queue (share_dropped), never failed.
func TestShareDroppedPutsTheFileBackNotFailed(t *testing.T) {
	oneTry(t)
	s := newScenario(t)
	s.encodeErr = errors.New("ffmpeg exited 1")
	w := s.worker(Replace)
	w.ShareDown = func(string, int64) (ShareDown, bool) {
		return ShareDown{ShareID: 7, Name: "Movies NAS", MountPath: s.dir}, true
	}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("a share drop is not an error for the worker: %v", err)
	}
	j := s.job()
	if j.Status != jobs.Queued || j.Attempts != 0 {
		t.Errorf("want queued with share_dropped and no try counted, got %+v", j)
	}
	if !strings.Contains(j.Error, "Movies NAS stopped answering") {
		t.Errorf("the reason does not name the share: %q", j.Error)
	}
	s.assertOriginalSafe()
	if tempLeft(s.src) {
		t.Error("temp left behind")
	}
}

// The same failure while the share answers is still a failure, as before.
func TestShareAnsweringStillFails(t *testing.T) {
	oneTry(t)
	s := newScenario(t)
	s.encodeErr = errors.New("ffmpeg exited 1")
	w := s.worker(Replace)
	w.ShareDown = func(string, int64) (ShareDown, bool) { return ShareDown{}, false }
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Failed {
		t.Errorf("want failed as before, got %+v", j)
	}
}

// D4c fix: the share answers again, but had an outage during the convert: the file is still put back (not
// failed), and nothing is paused, since the share is up.
func TestShareDroppedWhileAnsweringPausesNothing(t *testing.T) {
	oneTry(t)
	s := newScenario(t)
	s.encodeErr = errors.New("ffmpeg exited 1")
	lib, _ := s.store.AddLibrary("Films", s.dir, rules.SliderTemplate(3), true)
	w := s.worker(Replace)
	w.ShareDown = func(string, int64) (ShareDown, bool) {
		return ShareDown{ShareID: 7, Name: "Movies NAS", MountPath: s.dir, Answering: true}, true
	}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("a share drop is not an error for the worker: %v", err)
	}
	j := s.job()
	if j.Status != jobs.Queued || j.Attempts != 0 {
		t.Errorf("want queued with no try counted, got %+v", j)
	}
	if l, _ := s.store.GetLibrary(lib.ID); l.SharePausedBy != 0 {
		t.Errorf("the share answers, yet its library was paused: %+v", l)
	}
}
