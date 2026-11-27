// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package encode

import (
	"context"
	"math"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// makeVideoSource writes a short source with ffmpeg; the arguments are the source's own.
func makeVideoSource(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	out := filepath.Join(dir, name)
	argv := append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}, args...)
	argv = append(argv, out)
	if o, err := exec.Command("ffmpeg", argv...).CombinedOutput(); err != nil {
		t.Fatalf("make %s: %v\n%s", name, err, o)
	}
	return out
}

// sdrSource is a 1080p 60 fps 8-bit H.264 clip: the size and frame-rate cases.
func sdrSource(t *testing.T, dir string) string {
	return makeVideoSource(t, dir, "sdr60.mkv",
		"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=60:duration=1",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p")
}

// hdr10Source is a 1080p 10-bit HDR10 HEVC clip with mastering display and content light metadata,
// the same source S3-1b's real test uses.
func hdr10Source(t *testing.T, dir string) string {
	return makeVideoSource(t, dir, "hdr10.mkv",
		"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=24:duration=1",
		"-pix_fmt", "yuv420p10le", "-c:v", "libx265",
		"-x265-params", "colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1):max-cll=1000,400:hdr-opt=1:repeat-headers=1",
		"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc")
}

// convertReal plans and runs one conversion with the processor and probes the output.
func convertReal(t *testing.T, src string, plan Plan) (media.Info, string) {
	t.Helper()
	info, err := media.Probe(context.Background(), src)
	if err != nil {
		t.Fatalf("probe the source: %v", err)
	}
	plan.Output = filepath.Join(filepath.Dir(src), "out-"+filepath.Base(src))
	plan.Container = ContainerMKV
	res, err := Build(plan, info, cpuRung(plan.Video.Codec))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := media.Run(context.Background(), append([]string{"ffmpeg"}, res.Argv...), nil); err != nil {
		t.Fatalf("ffmpeg: %v", err)
	}
	got, err := media.Probe(context.Background(), plan.Output)
	if err != nil {
		t.Fatalf("probe the output: %v", err)
	}
	return got, plan.Output
}

// TestRealSizeKeepsTheShape: a 1080p file to 720 is a 1280x720 picture (size and aspect).
func TestRealSizeKeepsTheShape(t *testing.T) {
	requireRealTools(t)
	src := sdrSource(t, t.TempDir())
	got, _ := convertReal(t, src, Plan{
		Video: VideoPlan{Codec: CodecHEVC, CRF: intp(32), Preset: "faster", Scale: Scale{Height: 720}},
	})
	v := got.Video[0]
	if v.Width != 1280 || v.Height != 720 {
		t.Errorf("picture = %dx%d, want 1280x720", v.Width, v.Height)
	}
}

// TestRealFrameRateSixtyToThirty: a 60 fps file asked for 30 is a 30 fps file.
func TestRealFrameRateSixtyToThirty(t *testing.T) {
	requireRealTools(t)
	src := sdrSource(t, t.TempDir())
	got, _ := convertReal(t, src, Plan{
		Video: VideoPlan{Codec: CodecHEVC, CRF: intp(32), Preset: "faster", FPS: 30},
	})
	if fps := got.Video[0].FrameRate.Float(); math.Abs(fps-30) > 0.01 {
		t.Errorf("frame rate = %v, want 30", fps)
	}
}

// TestRealHDR10StaysTenBitWithMetadata: an HDR10 source with colour depth 8 asked and hdr: keep is
// a ten-bit HEVC file that still carries mastering display and content light metadata (L-HW-16).
func TestRealHDR10StaysTenBitWithMetadata(t *testing.T) {
	requireRealTools(t)
	src := hdr10Source(t, t.TempDir())
	got, out := convertReal(t, src, Plan{
		Video: VideoPlan{Codec: CodecHEVC, CRF: intp(30), Preset: "faster", BitDepth: Depth8, HDR: HDRKeep},
	})
	v := got.Video[0]
	if v.BitDepth != 10 {
		t.Errorf("bit depth = %d, want 10 (HDR keeps ten-bit)", v.BitDepth)
	}
	if v.ColorTransfer != "smpte2084" {
		t.Errorf("transfer = %q, want smpte2084", v.ColorTransfer)
	}
	hdr, err := media.ProbeFrameHDR(context.Background(), out)
	if err != nil {
		t.Fatalf("frame HDR: %v", err)
	}
	if !hdr.MasteringDisplay || !hdr.ContentLight {
		t.Errorf("frame side data = %+v, want mastering display and content light", hdr)
	}
}

// tonemapReference is the chain written out literally, as the spec has it; Build must produce the
// same picture from its own arguments.
const tonemapReference = "zscale=tin=smpte2084:pin=bt2020:min=bt2020nc:t=linear:npl=100," +
	"format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0," +
	"zscale=t=bt709:m=bt709:r=tv,format=yuv420p"

// TestRealTonemapIsSDRBT709: hdr: tonemap gives an 8-bit BT.709 file whose picture matches the
// reference chain. The reference is the same chain written out in the test, so this checks the
// arguments carry it through; it is not an independent colour model.
func TestRealTonemapIsSDRBT709(t *testing.T) {
	requireRealTools(t)
	dir := t.TempDir()
	src := hdr10Source(t, dir)
	got, out := convertReal(t, src, Plan{
		Video: VideoPlan{Codec: CodecHEVC, CRF: intp(30), Preset: "faster", HDR: HDRTonemap},
	})
	v := got.Video[0]
	if v.BitDepth != 8 {
		t.Errorf("bit depth = %d, want 8 for an SDR output", v.BitDepth)
	}
	if v.ColorPrimaries != "bt709" || v.ColorTransfer != "bt709" || v.ColorSpace != "bt709" {
		t.Errorf("colour = %s/%s/%s, want bt709 on all three", v.ColorPrimaries, v.ColorTransfer, v.ColorSpace)
	}
	// The output must not describe an SDR picture as HDR: the source's mastering display and content light
	// metadata are deleted from the frames before the encoder sees them (#855).
	if hdr, _ := media.ProbeFrameHDR(context.Background(), out); hdr.MasteringDisplay || hdr.ContentLight {
		t.Errorf("tone-mapped file still carries HDR side data %+v", hdr)
	}

	ref := filepath.Join(dir, "ref.mkv")
	if o, err := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", src, "-vf", tonemapReference, "-c:v", "libx265", "-preset", "faster", "-crf", "30",
		"-color_primaries", "bt709", "-color_trc", "bt709", "-colorspace", "bt709", ref).CombinedOutput(); err != nil {
		t.Fatalf("reference chain: %v\n%s", err, o)
	}
	if psnr := psnrOf(t, out, ref); psnr < 40 {
		t.Errorf("picture vs the reference chain = %.1f dB, want at least 40", psnr)
	}
}

var psnrAverage = regexp.MustCompile(`average:([0-9.]+|inf)`)

// psnrOf is the average PSNR of a file against a reference, as ffmpeg's psnr filter reports it.
func psnrOf(t *testing.T, a, b string) float64 {
	t.Helper()
	out, _ := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-i", a, "-i", b,
		"-lavfi", "psnr", "-f", "null", "-").CombinedOutput()
	m := psnrAverage.FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("no PSNR in ffmpeg's output:\n%s", out)
	}
	if strings.TrimSpace(m[1]) == "inf" {
		return math.Inf(1)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
