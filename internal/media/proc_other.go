// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !linux

package media

import "os/exec"

// dieWithParent does nothing here: only Linux can tie a child's life to its parent's. A converter left
// running after the server died is cleaned up at the next start (the staging sweep).
func dieWithParent(cmd *exec.Cmd) {}
