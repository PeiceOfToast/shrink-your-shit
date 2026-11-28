// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package helper

import (
	"context"
	"os/exec"
)

// execRunner runs a fixed program with no shell and an empty environment. The helper's caller
// supplies every argument; nothing here reads a string as a command.
func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{}
	return cmd.CombinedOutput()
}
