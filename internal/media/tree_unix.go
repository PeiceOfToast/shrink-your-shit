// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build unix

package media

import (
	"os/exec"
	"syscall"
)

// tree is a started process and the process group it leads.
type tree struct {
	pid int
}

// startTree starts cmd as the leader of a new process group, so killing the group also kills any
// child ffmpeg starts (L-Q-7).
func startTree(cmd *exec.Cmd) (*tree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if LowPriority() {
		lowerPriority(cmd.Process.Pid)
	}
	return &tree{pid: cmd.Process.Pid}, nil
}

// kill kills the whole group. The group id is the leader's pid because Setpgid is set above.
func (t *tree) kill() {
	if t.pid > 0 {
		_ = syscall.Kill(-t.pid, syscall.SIGKILL)
	}
}

// release has nothing to free on Unix; Wait already reaped the leader.
func (t *tree) release() {}
