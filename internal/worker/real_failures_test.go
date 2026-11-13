// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// User review 11-13 item E, the three forced failures: a damaged file, an output folder that can't be written to,
// and a missing encoder. Real ffmpeg prints each one; the summary's code must name it (not the generic one).
func TestRealFailuresGetTheirCode(t *testing.T) {
	needTools(t)
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only folder: the output case needs a normal user")
	}
	dir := t.TempDir()
	damaged := filepath.Join(dir, "damaged.mkv")
	if err := os.WriteFile(damaged, make([]byte, 200000), 0o644); err != nil {
		t.Fatal(err)
	}
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "clip.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=5:duration=1", "-c:v", "libx264", src)
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"damaged source", []string{"-nostdin", "-v", "error", "-i", damaged, "-c:v", "libx265", "-f", "matroska", filepath.Join(dir, "out.mkv")}, "source_damaged"},
		{"output not writable", []string{"-nostdin", "-v", "error", "-i", src, "-c:v", "libx265", "-f", "matroska", filepath.Join(ro, "out.mkv")}, "output_not_writable"},
		{"missing encoder", []string{"-nostdin", "-v", "error", "-i", src, "-c:v", "no_such_encoder", "-f", "matroska", filepath.Join(dir, "enc.mkv")}, "encoder_unavailable"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, err := exec.Command("ffmpeg", c.args...).CombinedOutput()
			if err == nil {
				t.Fatalf("ffmpeg did not fail: %s", out)
			}
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if got := jobs.FailureCode(jobs.ReasonConvertFailed, lines); got != c.want {
				t.Errorf("code %s, want %s; ffmpeg said:\n%s", got, c.want, out)
			}
		})
	}
}
