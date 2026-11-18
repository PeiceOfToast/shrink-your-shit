// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"os/exec"
	"syscall"
)

// dieWithParent makes cmd's process get SIGKILL when this program dies (review-697332b P2): an ffmpeg
// left running after the server was killed went on writing into the folder for new files. (The signal
// is tied to the OS thread that started it; Go ends a thread only for a goroutine that locked it and
// exited, which nothing here does.)
func dieWithParent(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}

// killGroup starts cmd in its own process group and, when its context ends, kills the whole group:
// a wrapper's children (QA's stall wrapper, a script around ffmpeg) go with it instead of holding the
// run's output open (QA finding B).
func killGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
