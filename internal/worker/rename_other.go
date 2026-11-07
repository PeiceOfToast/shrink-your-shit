// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !linux && !windows

package worker

func renameNoReplaceOS(src, dst string) error { return linkRename(src, dst) }
