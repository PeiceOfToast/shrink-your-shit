// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package doctor

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// A real doctor run on this host's ffmpeg. On a host with no graphics card the record list is empty and the run
// still finishes; the time it took is logged for the handback (L2). Not asserted beyond that.
func TestRealDoctorDeviceRecordTime(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	c := &Checker{Exec: media.OSExec{}, FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	start := time.Now()
	tools := c.Tools(context.Background())
	took := time.Since(start)
	t.Logf("doctor Tools() took %s; %d hardware encoders; %d device records", took, len(tools.Hardware), len(tools.Devices))
	for _, d := range tools.Devices {
		t.Logf("device %s: decode %+v scale %v", d.Family, d.Decode, d.Scale)
	}
	if took > deviceBudget+30*time.Second {
		t.Errorf("doctor run took %s, past the device budget", took)
	}
}
