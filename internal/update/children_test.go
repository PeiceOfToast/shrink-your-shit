// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

// Only the children the app started are picked: created no earlier than the app, not the keep process, and with a
// creation time that could be read. An older process that names the app as parent (a reused PID) is not picked.
func TestOwnChildrenPicksOnlyTheAppsOwn(t *testing.T) {
	app := time.Date(2026, 11, 21, 3, 0, 0, 0, time.UTC)
	cands := []childCandidate{
		{pid: 10, created: app.Add(time.Minute)},    // the app's child
		{pid: 11, created: app.Add(-time.Hour)},     // older than the app: a reused PID, spared
		{pid: 12, created: app},                     // created with the app: the app's
		{pid: 13, err: errors.New("access denied")}, // creation time unknown: spared
		{pid: 14, created: app.Add(time.Minute)},    // the watcher, kept
	}
	got := ownChildren(cands, app, 14)
	if want := []int{10, 12}; !reflect.DeepEqual(got, want) {
		t.Errorf("ownChildren = %v, want %v", got, want)
	}
}

// A walk that fails partway still ends the children it already found, then reports the walk error.
func TestEndChildrenStopsTheFoundOnesWhenTheWalkFails(t *testing.T) {
	walkErr := errors.New("walk broke")
	var stopped []int
	err := endChildren([]int{10, 11}, walkErr, func(pid int) error {
		stopped = append(stopped, pid)
		return nil
	})
	if want := []int{10, 11}; !reflect.DeepEqual(stopped, want) {
		t.Errorf("stopped %v, want %v", stopped, want)
	}
	if !errors.Is(err, walkErr) {
		t.Errorf("err = %v, want the walk error", err)
	}
}

// With a complete walk, a child that fails to stop is reported and the others are still stopped.
func TestEndChildrenReportsAStopFailureAndStopsTheRest(t *testing.T) {
	stopErr := errors.New("access denied")
	var stopped []int
	err := endChildren([]int{10, 11, 12}, nil, func(pid int) error {
		stopped = append(stopped, pid)
		if pid == 11 {
			return stopErr
		}
		return nil
	})
	if want := []int{10, 11, 12}; !reflect.DeepEqual(stopped, want) {
		t.Errorf("stopped %v, want %v", stopped, want)
	}
	if !errors.Is(err, stopErr) {
		t.Errorf("err = %v, want the stop error", err)
	}
}
