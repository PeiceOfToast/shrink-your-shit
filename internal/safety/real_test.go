// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// requireFFmpeg is the media package's L-INST-7 rule: skip only when ffmpeg is missing and
// SYP_REQUIRE_FFMPEG is unset; CI sets it and also fails on any "--- SKIP: TestReal".
func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		if os.Getenv("SYP_REQUIRE_FFMPEG") != "" {
			t.Fatalf("ffmpeg is not on PATH and SYP_REQUIRE_FFMPEG is set: the real check test must not skip")
		}
		t.Skipf("ffmpeg is not on PATH (set SYP_REQUIRE_FFMPEG=1 to fail instead)")
	}
}

func runFFmpeg(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("ffmpeg", append([]string{"-hide_banner", "-v", "error", "-nostdin", "-y"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// realClip makes a 20 s, 640x360, 25 fps H.264 MKV in dir: 500 pictures, about five megabytes.
func realClip(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "original.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=20",
		"-c:v", "libx264", "-preset", "veryfast", "-b:v", "2M", path)
	return path
}

// realInfo is the probe's answer for realClip (or for a result encoded from it): it is written by
// hand so the test never needs ffprobe.
func realInfo(path string) media.Info {
	return media.Info{
		Path:   path,
		Format: media.Format{Duration: 20 * time.Second},
		Video: []media.VideoTrack{{
			Index: 0, Codec: "h264", Width: 640, Height: 360,
			FrameRate: media.Rational{Num: 25, Den: 1}, Duration: 20 * time.Second,
		}},
	}
}

// wreck copies src to dst and overwrites n bytes at offset with zeros.
func wreck(t *testing.T, src, dst string, offset int, n int) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if offset < 0 || offset+n > len(data) {
		t.Fatalf("wreck %d bytes at %d is outside a %d-byte file", n, offset, len(data))
	}
	for i := offset; i < offset+n; i++ {
		data[i] = 0
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRealSourceCheckKeepsATruncatedFile: an original that ends early is kept, with its reason
// (L-SAFE-4). Nothing is blamed on the encoder.
func TestRealSourceCheckKeepsATruncatedFile(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := realClip(t, dir)
	st, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	cut := filepath.Join(dir, "truncated.mkv")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cut, data[:int64(len(data))*3/5], 0o600); err != nil {
		t.Fatal(err)
	}
	if st.Size() == 0 {
		t.Fatal("the clip is empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	v, err := SourceCheck(ctx, media.MediaFuncs{CaptureFunc: media.Capture}, Recorder{}, cut, realInfo(cut), 0)
	if err != nil {
		t.Fatal(err)
	}
	if v.OK || v.Code != CodeOriginalDamaged {
		t.Fatalf("verdict = %+v; want the truncated original kept", v)
	}
	if !strings.Contains(v.Message, "damaged") {
		t.Errorf("message = %q; want a plain sentence", v.Message)
	}
}

// TestRealSourceCheckKeepsALargeHole: 300 kB zeroed in the middle of the video (6 % of the file)
// loses pictures; the original is kept.
func TestRealSourceCheckKeepsALargeHole(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := realClip(t, dir)
	hole := filepath.Join(dir, "hole.mkv")
	wreck(t, src, hole, lenOf(t, src)/2, 300_000)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	v, err := SourceCheck(ctx, media.MediaFuncs{CaptureFunc: media.Capture}, Recorder{}, hole, realInfo(hole), 0)
	if err != nil {
		t.Fatal(err)
	}
	if v.OK || v.Code != CodeOriginalDamaged {
		t.Fatalf("verdict = %+v; want the damaged original kept", v)
	}
	if !strings.Contains(strings.Join(v.Detail, "\n"), "Decoded:") {
		t.Errorf("detail = %v; want how many pictures the decoder delivered", v.Detail)
	}
}

// TestRealSourceCheckConvertsALittleDamage: a byte flipped in the video damages one picture of 500,
// inside the 1 % budget, so the file converts (L-SAFE-4).
func TestRealSourceCheckConvertsALittleDamage(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := realClip(t, dir)
	size := lenOf(t, src)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	capturer := media.MediaFuncs{CaptureFunc: media.Capture}
	// Where a flipped byte lands is not predictable (a cluster header, a slice, a frame's
	// metadata), so the test tries places in the video payload until it finds one the check
	// tolerates: that is what "little damage converts" means.
	for _, fraction := range []float64{0.30, 0.35, 0.40, 0.45, 0.50, 0.55, 0.60, 0.65, 0.70, 0.75} {
		damaged := filepath.Join(dir, "little.mkv")
		wreck(t, src, damaged, int(float64(size)*fraction), 1)
		v, err := SourceCheck(ctx, capturer, Recorder{}, damaged, realInfo(damaged), 0)
		if err != nil {
			t.Fatal(err)
		}
		if v.OK {
			return // a little damage was tolerated, which is the rule
		}
	}
	t.Fatalf("no single damaged byte of %d was tolerated; the budget may be wrong", size)
}

// TestRealPictureCheckPassesAnHonestEncode: a plain re-encode of the clip matches the original's
// picture (L-SAFE-3, K4).
func TestRealPictureCheckPassesAnHonestEncode(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := realClip(t, dir)
	out := filepath.Join(dir, "honest.mkv")
	runFFmpeg(t, "-i", src, "-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-an", out)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	want := WantedVideo{Compare: true, Width: 640, Height: 360}
	v, err := pictureCheck(ctx, media.MediaFuncs{CaptureFunc: media.Capture}, realInfo(src), realInfo(out), want)
	if err != nil || v != nil {
		t.Fatalf("verdict = %+v, %v; want the honest encode's picture to match", v, err)
	}
}

// TestRealPictureCheckRefusesAGreyEncode: an encode that loses the colour is a different picture,
// and the result check keeps the original. Loosening MinMeanSSIM to 0 must make this test fail.
func TestRealPictureCheckRefusesAGreyEncode(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := realClip(t, dir)
	out := filepath.Join(dir, "grey.mkv")
	runFFmpeg(t, "-i", src, "-vf", "hue=s=0", "-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-an", out)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	want := WantedVideo{Compare: true, Width: 640, Height: 360}
	v, err := pictureCheck(ctx, media.MediaFuncs{CaptureFunc: media.Capture}, realInfo(src), realInfo(out), want)
	if err != nil {
		t.Fatal(err)
	}
	if v == nil || v.Code != CodePicture {
		t.Fatalf("verdict = %+v; want the grey picture refused", v)
	}
	if !strings.Contains(v.Message, "does not match") {
		t.Errorf("message = %q", v.Message)
	}
}

func lenOf(t *testing.T, path string) int {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return int(st.Size())
}
