// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !unix && !windows

package main

import "os"

func lockFile(*os.File) error { return nil }
func unlockFile(*os.File)     {}
