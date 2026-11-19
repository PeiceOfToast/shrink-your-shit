// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"errors"
	"reflect"
	"testing"
)

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
