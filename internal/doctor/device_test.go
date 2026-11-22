// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package doctor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// deviceFake answers the device probes: a sample made in software (the codec's encoder, with its pixel format),
// the on-device decode or scale of that sample, and the HDR round trip (a Matroska file ffprobe reads back).
// failDecode10 fails the HEVC 10-bit decode; failScale fails any run with a scale filter on the device;
// failHDREncode fails the device's HDR encode; hdrLost leaves the light levels out of what ffprobe reads back.
type deviceFake struct {
	failDecode10, failScale, failHDREncode, hdrLost, frameOnly bool
	last10                                                     bool
	decodes, scales, hdrEncodes                                int
}

const (
	hdrBoth          = `{"streams":[{"codec_type":"video","side_data_list":[{"side_data_type":"Mastering display metadata"},{"side_data_type":"Content light level metadata"}]}]}`
	hdrOnlyMastering = `{"streams":[{"codec_type":"video","side_data_list":[{"side_data_type":"Mastering display metadata"}]}]}`
	// hdrFrameOnly is the ffprobe 6.1 shape: the stream lists no side data, the first frame lists both.
	hdrFrameOnly = `{"streams":[{"codec_type":"video"}],"frames":[{"side_data_list":[{"side_data_type":"Mastering display metadata"},{"side_data_type":"Content light level metadata"}]}]}`
)

func (f *deviceFake) run(name string, args []string) (string, string, error) {
	all := strings.Join(args, " ")
	switch {
	case strings.HasSuffix(name, "ffprobe"):
		if f.hdrLost {
			return hdrOnlyMastering, "", nil
		}
		if f.frameOnly {
			return hdrFrameOnly, "", nil
		}
		return hdrBoth, "", nil
	case strings.Contains(all, "-f matroska"):
		f.hdrEncodes++
		if f.failHDREncode {
			return "", "encode failed", errors.New("exit status 1")
		}
		return "", "", nil
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
	recs := deviceChecker(f).deviceTest(context.Background(), []string{"hevc_qsv"}, []string{"hevc_qsv"})
	if len(recs) != 1 || recs[0].Family != "qsv" {
		t.Fatalf("records %+v, want one qsv record", recs)
	}
	r := recs[0]
	if got := r.Decode["hevc"]; !got.Bit8 || got.TenBit() {
		t.Errorf("hevc decode %+v, want 8-bit yes and 10-bit no", got)
	}
	if got := r.Decode["h264"]; !got.Bit8 || got.Bit10 != nil {
		t.Errorf("h264 decode %+v, want 8-bit yes and no 10-bit key (never probed)", got)
	}
	if !r.Scale {
		t.Error("scale is no, but only the 10-bit decode failed")
	}
}

// A scale test that fails is "can't" for scale only: the decodes still stand.
func TestDeviceRecordScaleFailureIsCantNotCrash(t *testing.T) {
	f := &deviceFake{failScale: true}
	recs := deviceChecker(f).deviceTest(context.Background(), []string{"hevc_vaapi"}, []string{"hevc_vaapi"})
	if len(recs) != 1 {
		t.Fatalf("records %+v, want one vaapi record", recs)
	}
	if recs[0].Scale {
		t.Fatal("scale is yes, but its test failed")
	}
	if got := recs[0].Decode["hevc"]; !got.Bit8 || !got.TenBit() {
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
	if recs := c.deviceTest(context.Background(), []string{"hevc_nvenc", "libx265"}, nil); len(recs) != 0 {
		t.Errorf("records %+v for families with no full pipeline", recs)
	}
	if f.decodes != 0 || f.scales != 0 {
		t.Errorf("ran %d decodes and %d scales for no device", f.decodes, f.scales)
	}
}

// The HDR probe: a device whose 10-bit HEVC encode keeps both the mastering display and the light levels is yes.
func TestDeviceRecordHDRMetaSurvivesTheDeviceEncode(t *testing.T) {
	f := &deviceFake{}
	recs := deviceChecker(f).deviceTest(context.Background(), []string{"hevc_qsv"}, []string{"hevc_qsv"})
	if len(recs) != 1 || !recs[0].HDRMeta {
		t.Fatalf("records %+v, want HDR metadata yes", recs)
	}
	if f.hdrEncodes != 1 {
		t.Errorf("%d HDR encodes, want 1", f.hdrEncodes)
	}
}

// Light levels lost on the way through the device is "can't", even though the mastering display survived.
func TestDeviceRecordHDRMetaLostIsCant(t *testing.T) {
	f := &deviceFake{hdrLost: true}
	recs := deviceChecker(f).deviceTest(context.Background(), []string{"hevc_qsv"}, []string{"hevc_qsv"})
	if len(recs) != 1 || recs[0].HDRMeta {
		t.Fatalf("records %+v, want HDR metadata no", recs)
	}
}

// A failed HDR encode is "can't" and leaves the rest of the record standing.
func TestDeviceRecordHDRMetaEncodeFailureIsCant(t *testing.T) {
	f := &deviceFake{failHDREncode: true}
	recs := deviceChecker(f).deviceTest(context.Background(), []string{"hevc_qsv"}, []string{"hevc_qsv"})
	if len(recs) != 1 || recs[0].HDRMeta {
		t.Fatalf("records %+v, want HDR metadata no", recs)
	}
	if !recs[0].Scale || !recs[0].Decode["hevc"].TenBit() {
		t.Errorf("the other fields changed: %+v", recs[0])
	}
}

// No 10-bit encode for the family: no HDR probe runs at all.
func TestDeviceRecordHDRMetaOnlyAfterATenBitEncode(t *testing.T) {
	f := &deviceFake{}
	recs := deviceChecker(f).deviceTest(context.Background(), []string{"hevc_qsv"}, nil)
	if len(recs) != 1 || recs[0].HDRMeta {
		t.Fatalf("records %+v, want HDR metadata no", recs)
	}
	if f.hdrEncodes != 0 {
		t.Errorf("%d HDR encodes with no 10-bit encode", f.hdrEncodes)
	}
}

// The ffprobe 6.1 shape: the side data is on the first frame only, and the card still counts as HDR.
func TestDeviceRecordHDRMetaReadsTheFirstFrame(t *testing.T) {
	f := &deviceFake{frameOnly: true}
	recs := deviceChecker(f).deviceTest(context.Background(), []string{"hevc_qsv"}, []string{"hevc_qsv"})
	if len(recs) != 1 || !recs[0].HDRMeta {
		t.Fatalf("records %+v, want HDR metadata yes from the frame-level side data", recs)
	}
}

// The encoder answer is stored before the device probes run, so ToolsNow has the card while a device probe is
// still stuck (up to 90 s on a fresh boot). The device record follows when the probe is done.
func TestEncoderAnswerIsReadyWhileTheDeviceProbeRuns(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	x := &media.FakeExec{Handler: func(name string, args []string) (string, string, error) {
		a := strings.Join(args, " ")
		switch {
		case strings.Contains(a, "-version"):
			return name + " version 7.1.5", "", nil
		case strings.Contains(a, "-encoders"):
			return " V....D libx265              H.265 / HEVC\n V....D hevc_qsv           hardware HEVC\n", "", nil
		case strings.Contains(a, "-hwaccel"): // the first device probe: hold it
			once.Do(func() { close(entered) })
			<-release
			return "", "", nil
		case strings.Contains(a, "-c:v"):
			return "", "", nil
		}
		return "", "", errors.New("unexpected " + a)
	}}
	c := &Checker{Exec: x, FFmpeg: "/usr/bin/ffmpeg", FFprobe: "/usr/bin/ffprobe"}
	done := make(chan Tools, 1)
	go func() { done <- c.Tools(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("the device probe never started")
	}
	tl, ok := c.ToolsNow()
	close(release)
	<-done
	if !ok || len(tl.Hardware) != 1 || tl.Hardware[0] != "hevc_qsv" {
		t.Errorf("while the device probe runs: ok=%v hardware=%v, want the encoder answer hevc_qsv", ok, tl.Hardware)
	}
}

// "Test again" re-probes decode: a decode answer cached as failed must not survive a Retest, or the card that
// now decodes reads "can't" until the process restarts.
func TestRetestReprobesDecode(t *testing.T) {
	var mu sync.Mutex
	decodeFails := true
	x := &media.FakeExec{Handler: func(name string, args []string) (string, string, error) {
		a := strings.Join(args, " ")
		switch {
		case strings.Contains(a, "-version"):
			return name + " version 7.1.5", "", nil
		case strings.Contains(a, "-encoders"):
			return " V....D libx265              H.265 / HEVC\n V....D hevc_qsv           hardware HEVC\n", "", nil
		case strings.Contains(a, "-hwaccel"):
			mu.Lock()
			fail := decodeFails
			mu.Unlock()
			if fail {
				return "", "decode failed", errors.New("exit status 1")
			}
			return "", "", nil
		case strings.Contains(a, "-c:v"):
			return "", "", nil
		}
		return "", "", errors.New("unexpected " + a)
	}}
	c := &Checker{Exec: x, FFmpeg: "/usr/bin/ffmpeg", FFprobe: "/usr/bin/ffprobe"}
	first := c.Tools(context.Background())
	if len(first.Devices) != 1 || first.Devices[0].Decode["hevc"].Bit8 {
		t.Fatalf("first test: devices %+v, want hevc 8-bit decode failed", first.Devices)
	}
	mu.Lock()
	decodeFails = false
	mu.Unlock()
	again := c.Retest(context.Background())
	if len(again.Devices) != 1 || !again.Devices[0].Decode["hevc"].Bit8 {
		t.Errorf("after Retest: devices %+v, want hevc 8-bit decode passed (the cached failure was kept)", again.Devices)
	}
}

// hasHDRSideData reads the video stream by codec_type: an audio stream that comes first does not decide it.
func TestHDRSideDataPicksTheVideoStream(t *testing.T) {
	audioFirst := `{"streams":[{"codec_type":"audio"},{"codec_type":"video","side_data_list":[{"side_data_type":"Mastering display metadata"},{"side_data_type":"Content light level metadata"}]}]}`
	if !hasHDRSideData([]byte(audioFirst)) {
		t.Error("video stream with both side data after an audio stream: want true")
	}
	videoNoSide := `{"streams":[{"codec_type":"video"},{"codec_type":"audio","side_data_list":[{"side_data_type":"Mastering display metadata"},{"side_data_type":"Content light level metadata"}]}]}`
	if hasHDRSideData([]byte(videoNoSide)) {
		t.Error("side data on an audio stream counted for the video stream: want false")
	}
}
