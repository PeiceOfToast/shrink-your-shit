// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"fmt"
	"time"
)

// stopWait is how long a stopped process has to exit before the update counts it as still running.
const stopWait = 30 * time.Second

// The result of WaitForSingleObject (Windows): WAIT_OBJECT_0 when the process exited, WAIT_TIMEOUT when it did
// not in time. Only WAIT_FAILED comes back as an error, so a timeout needs the event checked too.
const (
	waitObject0 = 0x00000000
	waitTimeout = 0x00000102
)

// exitWaitError says whether a process has exited, from the event and error WaitForSingleObject returned.
func exitWaitError(pid int, event uint32, err error) error {
	switch {
	case err != nil:
		return fmt.Errorf("process %d did not exit: %w", pid, err)
	case event == waitObject0:
		return nil
	case event == waitTimeout:
		return fmt.Errorf("process %d did not exit within %s", pid, stopWait)
	default:
		return fmt.Errorf("process %d did not exit (wait result %#x)", pid, event)
	}
}
