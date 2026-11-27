// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// TestRealTryOnAFile is S2-5 end to end: the starter runs on a real clip and answers the three
// step lines and a size line; a long clip is tried on its one-minute sample, a short one whole.
func TestRealTryOnAFile(t *testing.T) {
	requireRealTools(t)
	for _, encoder := range []string{"libx264", "libx265"} {
		if !hasEncoder(t, encoder) {
			if os.Getenv("SYP_REQUIRE_FFMPEG") != "" {
				t.Fatalf("this ffmpeg build has no %s and SYP_REQUIRE_FFMPEG is set", encoder)
			}
			t.Skipf("this ffmpeg build has no %s", encoder)
		}
	}

	cases := []struct {
		name     string
		duration int
		want     func(string) bool
	}{
		{
			name: "a long clip is tried on a sample", duration: 200,
			want: func(line string) bool {
				return strings.HasPrefix(line, "Size: about ") && strings.HasSuffix(line, "estimated from a one-minute sample.")
			},
		},
		{
			name: "a short clip is tried whole", duration: 40,
			want: func(line string) bool {
				return strings.HasPrefix(line, "Size: ") && !strings.Contains(line, "about") && !strings.Contains(line, "sample")
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			src := filepath.Join(root, "holiday.mkv")
			// A deliberately fat source, so the starter's HEVC encode is the smaller file and the
			// size line is the ordinary one whichever way the estimate lands.
			make := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
				"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration="+strconv.Itoa(c.duration),
				"-f", "lavfi", "-i", "sine=frequency=440:duration="+strconv.Itoa(c.duration),
				"-c:v", "libx264", "-preset", "ultrafast", "-b:v", "1M", "-pix_fmt", "yuv420p",
				"-c:a", "aac", "-shortest", src)
			if out, err := make.CombinedOutput(); err != nil {
				t.Fatalf("make the clip: %v\n%s", err, out)
			}
			before, err := os.ReadFile(src)
			if err != nil {
				t.Fatalf("read the source: %v", err)
			}

			env := flow.Env{
				Media:      media.MediaFuncs{ProbeFunc: media.Probe, RunFunc: media.Run, CaptureFunc: media.Capture},
				Safety:     flow.RealSafety{},
				Pick:       flow.ProcessorPick,
				Now:        time.Now,
				Sleep:      time.Sleep,
				AppVersion: "test",
			}
			res, err := flow.Try(context.Background(), starterFlow(t), src, env, t.TempDir())
			if err != nil {
				t.Fatalf("try: %v", err)
			}

			if len(res.Lines) != 4 {
				t.Fatalf("lines = %q, want three step lines and the size line", texts(res.Lines))
			}
			for i, id := range []string{"video", "audio", "save", "save"} {
				if res.Lines[i].StepID != id || res.Lines[i].Text == "" {
					t.Errorf("line %d = %+v, want a sentence on %s", i, res.Lines[i], id)
				}
			}
			if last := res.Lines[3].Text; !c.want(last) {
				t.Errorf("size line = %q, want %s's form", last, c.name)
			}

			// Nothing was kept: the original is byte for byte, and the folder holds only it.
			after, err := os.ReadFile(src)
			if err != nil {
				t.Fatalf("the original is gone: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Errorf("the original changed: %d -> %d bytes", len(before), len(after))
			}
			got := entries(t, root)
			if len(got) != 1 || got[0] != "holiday.mkv" {
				t.Errorf("the folder holds %v, want only the original", got)
			}
		})
	}
}
