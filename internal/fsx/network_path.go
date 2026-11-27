// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package fsx

import "strings"

// networkPathKind classifies an absolute path by its shape alone, without asking the OS: a UNC path
// is a network share, and the \\?\ long-path prefix is local (except \\?\UNC\, which is UNC in long
// form). kind is empty and askDrive true when only GetDriveType on the volume can answer.
func networkPathKind(abs string) (kind string, askDrive bool) {
	switch {
	case strings.HasPrefix(abs, `\\?\UNC\`) || strings.HasPrefix(abs, `//?/UNC/`):
		return "network share", false
	case strings.HasPrefix(abs, `\\?\`) || strings.HasPrefix(abs, `//?/`):
		return "", false // \\?\ on a local path: the long-path prefix, never a share
	case strings.HasPrefix(abs, `\\`) || strings.HasPrefix(abs, `//`):
		return "network share", false
	}
	return "", true
}
