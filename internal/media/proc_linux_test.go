// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// Review 697332b P2: a converter started by the server dies when the server is killed, instead of
// going on writing. The test runs itself as a "server" that starts a child the way ffmpeg is started
// (OSExec), prints the child's pid, and is then killed with SIGKILL.
func TestChildDiesWithTheServer(t *testing.T) {
	if os.Getenv("SYP_TEST_PARENT") == "1" { // the "server"
		OSExec{}.Stream(context.Background(), func(line string) { fmt.Println(line) }, "sh", "-c", "echo $$; exec sleep 60")
		return
	}
	parent := exec.Command(os.Args[0], "-test.run=^TestChildDiesWithTheServer$")
	parent.Env = append(os.Environ(), "SYP_TEST_PARENT=1")
	out, _ := parent.StdoutPipe()
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(out)
	if !sc.Scan() {
		t.Fatal("the child's pid never came")
	}
	pid, err := strconv.Atoi(sc.Text())
	if err != nil {
		t.Fatalf("pid %q", sc.Text())
	}
	defer syscall.Kill(pid, syscall.SIGKILL) // whatever happens, no sleep left behind
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the child isn't running: %v", err)
	}
	parent.Process.Kill() // kill -9 the "server"
	parent.Wait()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return // gone with its parent
		}
		if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil && zombie(b) { // ended, not yet reaped
			return
		}
	}
	t.Error("the child was still running 3 s after the server was killed")
}

// zombie reports whether /proc/<pid>/stat says the process has ended (state Z or X).
func zombie(stat []byte) bool {
	for i := len(stat) - 1; i > 0; i-- {
		if stat[i] == ')' && i+2 < len(stat) {
			return stat[i+2] == 'Z' || stat[i+2] == 'X'
		}
	}
	return false
}
