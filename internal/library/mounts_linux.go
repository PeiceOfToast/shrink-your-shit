// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package library

import (
	"io"
	"os"
)

// mountInfoSource opens the mount table. Tests replace it with a fixture.
var mountInfoSource = func() (io.ReadCloser, error) { return os.Open("/proc/self/mountinfo") }
