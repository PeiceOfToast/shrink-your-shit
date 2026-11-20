// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !linux

package web

import "errors"

// openPerfCounter has no PMU counters off Linux, so the Intel reading is never measured here.
func openPerfCounter(uint32, uint64, int) (pmuCounter, error) {
	return nil, errors.New("PMU counters are read on Linux only")
}
