// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"errors"
	"strings"
	"testing"
)

// A process that exited is stopped. A timeout is not, even though WaitForSingleObject returns no error for it.
func TestExitWaitErrorReadsTheEvent(t *testing.T) {
	if err := exitWaitError(7, waitObject0, nil); err != nil {
		t.Fatalf("exited process: %v", err)
	}

	err := exitWaitError(7, waitTimeout, nil)
	if err == nil {
		t.Fatal("a wait that timed out was reported as an exit")
	}
	if !strings.Contains(err.Error(), "process 7 did not exit within 30s") {
		t.Errorf("timeout error %q, want the pid and the 30s wait", err)
	}

	// Any other event (abandoned, or a code this does not know) is not an exit either.
	if err := exitWaitError(7, 0x80, nil); err == nil {
		t.Error("an abandoned wait was reported as an exit")
	}
}

// A failed wait keeps its own error for the caller.
func TestExitWaitErrorKeepsTheWaitError(t *testing.T) {
	cause := errors.New("wait failed")
	err := exitWaitError(7, 0xFFFFFFFF, cause)
	if !errors.Is(err, cause) {
		t.Fatalf("error %v does not wrap the wait's error", err)
	}
}
