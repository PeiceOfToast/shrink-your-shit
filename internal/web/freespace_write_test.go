// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"sync/atomic"
	"testing"
	"time"
)

// A write probe that never answers (a hung network mount) must not hold the list: the answer is "not known yet",
// which the list shows as nothing, not as read-only.
func TestWriteCheckDoesNotBlockTheList(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	oldCheck, oldWait := writeCheck, writeTimeout
	writeCheck = func(string) bool { <-block; return true }
	writeTimeout = 20 * time.Millisecond
	t.Cleanup(func() { writeCheck, writeTimeout = oldCheck, oldWait })

	start := time.Now()
	_, known := canWriteTimed(t.TempDir())
	if known {
		t.Error("a probe that has not answered is reported as known")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("a hung probe held the call for %v, want about %v", d, writeTimeout)
	}
}

// One probe per folder within the TTL: the list asks again and again, but the folder is only probed once a minute.
func TestWriteCheckIsCachedPerFolder(t *testing.T) {
	var calls atomic.Int32
	oldCheck := writeCheck
	writeCheck = func(string) bool { calls.Add(1); return true }
	t.Cleanup(func() { writeCheck = oldCheck })

	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if w, known := canWriteTimed(dir); !w || !known {
			t.Fatalf("call %d: writable %v, known %v; want true, true", i, w, known)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("the folder was probed %d times in a row, want 1", n)
	}
}
