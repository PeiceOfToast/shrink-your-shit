// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !linux

package library

import (
	"errors"
	"io"
)

// errNoMountTable: there is no /proc/self/mountinfo here, so the share check is skipped and the identity check alone
// decides. No change for other OSes (S2-10a).
var errNoMountTable = errors.New("no mount table on this system")

// mountInfoSource reports no mount table, so readMounts returns nil and the share check is skipped.
var mountInfoSource = func() (io.ReadCloser, error) { return nil, errNoMountTable }
