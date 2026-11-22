// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// deviceFake answers the device probes: a sample made in software (the codec's encoder, with its pixel format)
// and the on-device decode or scale of that sample. failDecode10 fails the HEVC 10-bit decode; failScale fails
// any run with a scale filter on the device.
type deviceFake struct {
	failDecode10, failScale bool
	last10                  bool
	decodes, scales         int
}

func (f *deviceFake) run(name string, args []string) (string, string, error) {
	all := strings.Join(args, " ")
	switch {
	case strings.Contains(all, "-c:v libx265") || strings.Contains(all, "-c:v libx264"):
		f.last10 = strings.Contains(all, "yuv420p10le")
		return "", "", nil
	case strings.Contains(all, "-hwaccel"):
		if strings.Contains(all, "-vf scale_") {
			f.scales++
			if f.failScale {
				return "", "scale failed", errors.New("exit status 1")
			}
			return "", "", nil
		}
		f.decodes++
		if f.failDecode10 && f.last10 {
			return "", "unsupported profile", errors.New("exit status 1")
		}
		return "", "", nil
	}
	return "", "", nil
}

func deviceChecker(f *deviceFake) *Checker {
	return &Checker{Exec: &media.FakeExec{Handler: f.run}, FFmpeg: "/usr/bin/ffmpeg", FFprobe: "/usr/bin/ffprobe"}
}

// A device with 10-bit encode but no 10-bit decode: the record says the HEVC 10-bit decode is "can't" while the
// 8-bit one and the scale stand.
func TestDeviceRecordSeparatesTenBitDecodeFromEncode(t *testing.T) {
	f := &deviceFake{failDecode10: true}
	recs := deviceChecker(f).deviceTest(context.Background(), []string{"hevc_qsv"})
	if len(recs) != 1 || recs[0].Family != "qsv" {
		t.Fatalf("records %+v, want one qsv record", recs)
	}
	r := recs[0]
	if got := r.Decode["hevc"]; !got.Bit8 || got.Bit10 {
		t.Errorf("hevc decode %+v, want 8-bit yes and 10-bit no", got)
	}
	if got := r.Decode["h264"]; !got.Bit8 {
		t.Errorf("h264 decode %+v, want 8-bit yes", got)
	}
	if !r.Scale {
		t.Error("scale is no, but only the 10-bit decode failed")
	}
}

// A scale test that fails is "can't" for scale only: the decodes still stand.
func TestDeviceRecordScaleFailureIsCantNotCrash(t *testing.T) {
	f := &deviceFake{failScale: true}
	recs := deviceChecker(f).deviceTest(context.Background(), []string{"hevc_vaapi"})
	if len(recs) != 1 {
		t.Fatalf("records %+v, want one vaapi record", recs)
	}
	if recs[0].Scale {
		t.Error("scale is yes, but its test failed")
	}
	if got := recs[0].Decode["hevc"]; !got.Bit8 || !got.Bit10 {
		t.Errorf("hevc decode %+v, want both depths: the decodes passed", got)
	}
	if f.scales == 0 {
		t.Error("the scale test never ran")
	}
}

// A family with no full pipeline (or no encoder) has no device record: nothing is measured, nothing is claimed.
func TestDeviceRecordOnlyForFullPipelineFamilies(t *testing.T) {
	f := &deviceFake{}
	c := deviceChecker(f)
	if recs := c.deviceTest(context.Background(), []string{"hevc_nvenc", "libx265"}); len(recs) != 0 {
		t.Errorf("records %+v for families with no full pipeline", recs)
	}
	if f.decodes != 0 || f.scales != 0 {
		t.Errorf("ran %d decodes and %d scales for no device", f.decodes, f.scales)
	}
}
