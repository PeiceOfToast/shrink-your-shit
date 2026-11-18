// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !linux

package sysload

// Not measured here yet: the Workers page says so.
func cpuTimes() (idle, total uint64, ok bool) { return 0, 0, false }
func memory() (used, total uint64, ok bool)   { return 0, 0, false }
