// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media/fake"
)

// sourceInfo is an original of d seconds at 25 fps: its main video stream runs the whole time.
func sourceInfo(d time.Duration) media.Info {
	return media.Info{
		Path:   "/original.mkv",
		Format: media.Format{Duration: d},
		Video: []media.VideoTrack{{
			Index: 0, Codec: "h264", Width: 640, Height: 360,
			FrameRate: media.Rational{Num: 25, Den: 1}, Duration: d,
		}},
	}
}

// report is ffmpeg's end-of-run line for the video stream: packets read and pictures decoded.
func report(in int, packets, frames int) string {
	return "[verbose]   Input stream #0:" + itoa(in) + " (video): " + itoa(packets) + " packets read (1000 bytes); " + itoa(frames) + " frames decoded; 0 decode errors;\n"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// brokenPicture is the decoder's report for one damaged picture.
const brokenPicture = "[h264 @ 0x2] [error] error while decoding MB 27 15, bytestream 5552\n"

func TestSourceDamageCountsOnePicturePerBrokenSlice(t *testing.T) {
	raw := "[matroska,webm @ 0x1] [error] 0x00 at pos 2005687 (0x1e9ab7) invalid as first byte of an EBML number\n" +
		"[h264 @ 0x2] [error] cabac decode of qscale diff failed at 27 15\n" +
		brokenPicture +
		"[h264 @ 0x2] [info] concealing 344 DC, 344 AC, 344 MV errors in B frame\n"
	rep := sourceDamage(errorLines([]byte(raw)))
	if rep.Pictures != 1 {
		t.Errorf("pictures = %d; want one (the burst around one broken slice)", rep.Pictures)
	}
	if rep.FileLevel {
		t.Error("a broken macroblock is not damage to the file as a whole")
	}
	if len(rep.Lines) != 2 || !strings.Contains(rep.Lines[1], "error while decoding") {
		t.Errorf("lines = %v; want the error-level lines in order", rep.Lines)
	}
}

// TestSourceDamageIgnoresBenignLines: a broadcast recording that starts in the middle of a group of
// pictures prints these on every play; none of them is damage.
func TestSourceDamageIgnoresBenignLines(t *testing.T) {
	raw := "[h264 @ 0x1] non-existing PPS 0 referenced\n" +
		"[h264 @ 0x1] no frame!\n" +
		"Last message repeated 3 times\n" +
		"x265 [info]: 25 fps, 1 2x2\n" +
		"[h264 @ 0x1] decode_slice_header error\n"
	rep := sourceDamage(errorLines([]byte(raw)))
	if rep.Found() {
		t.Errorf("report = %+v; want no damage", rep)
	}
}

func TestSourceDamageCountsFileLevelErrors(t *testing.T) {
	for _, line := range []string{
		"[mov,mp4,m4a,3gp,3g2,mj2 @ 0x1] moov atom not found\n",
		"[matroska,webm @ 0x1] File ended prematurely\n",
		"[h264 @ 0x1] error while decoding MB 1 2\n",
	} {
		rep := sourceDamage(errorLines([]byte(line)))
		if !rep.FileLevel && rep.Pictures == 0 {
			t.Errorf("%q was not counted as damage", line)
		}
	}
	if rep := sourceDamage(errorLines([]byte("[matroska,webm @ 0x1] File ended prematurely\n"))); !rep.FileLevel {
		t.Error("a file that ends early must be damage to the file as a whole")
	}
}

// TestSourceDamageToleranceIsOnePercent: up to 1 % of the pictures may be damaged and the file
// still converts (L-SAFE-4); a file-level error and a file whose count is unknown never convert.
func TestSourceDamageToleranceIsOnePercent(t *testing.T) {
	if !damageTolerated(sourceDamageReport{Pictures: 5}, 500) {
		t.Error("5 damaged pictures of 500 is within the budget")
	}
	if damageTolerated(sourceDamageReport{Pictures: 6}, 500) {
		t.Error("6 damaged pictures of 500 is above the budget")
	}
	if damageTolerated(sourceDamageReport{Pictures: 1, FileLevel: true}, 500) {
		t.Error("damage to the file as a whole is never tolerated")
	}
	if damageTolerated(sourceDamageReport{Pictures: 1}, 0) {
		t.Error("a file whose frame count is unknown is never tolerated")
	}
}

// TestSourceCheckToleratesALittleDamage: one broken picture of 500 converts, and nothing is said
// about the encoder.
func TestSourceCheckToleratesALittleDamage(t *testing.T) {
	m := &fake.Media{Captures: []fake.CaptureScript{{Stderr: []byte("[h264 @ 0x2] error while decoding MB 27 15\n" + report(0, 500, 500))}}}
	v, err := SourceCheck(context.Background(), m, Recorder{}, "/original.mkv", sourceInfo(20*time.Second), time.Minute)
	if err != nil || !v.OK {
		t.Fatalf("verdict = %+v, %v; want a little damage tolerated", v, err)
	}
}

// TestSourceCheckRefusesDamageAboveTheBudget: 6 broken pictures of 500 is 1.2 %, so the original is
// kept, with the share and the reason in plain words.
func TestSourceCheckRefusesDamageAboveTheBudget(t *testing.T) {
	var stderr strings.Builder
	for i := 0; i < 6; i++ {
		stderr.WriteString("[h264 @ 0x2] [error] error while decoding MB 27 15\n")
	}
	stderr.WriteString(report(0, 500, 500))
	m := &fake.Media{Captures: []fake.CaptureScript{{Stderr: []byte(stderr.String())}}}
	v, err := SourceCheck(context.Background(), m, Recorder{}, "/original.mkv", sourceInfo(20*time.Second), time.Minute)
	if err != nil || v.OK {
		t.Fatalf("verdict = %+v, %v; want the original kept", v, err)
	}
	if v.Code != CodeOriginalDamaged || v.NextAction != NextKeepOriginal {
		t.Errorf("verdict = %+v; want check.original_damaged with keep_original", v)
	}
	for _, want := range []string{"6 damaged pictures of 500", "1.20%", "kept"} {
		if !strings.Contains(v.Message, want) {
			t.Errorf("message = %q, want %q", v.Message, want)
		}
	}
	for _, banned := range []string{"encoder", "x265", "hevc", "libx264"} {
		if strings.Contains(strings.ToLower(v.Message), banned) {
			t.Errorf("message = %q blames the encoder", v.Message)
		}
	}
	if !strings.Contains(strings.Join(v.Detail, "\n"), "error while decoding") {
		t.Errorf("detail = %v; want ffmpeg's lines", v.Detail)
	}
}

// TestSourceCheckCountsFramesTheDecoderNeverDelivered: a stretch can lose pictures with no decoder
// message (a damaged cluster is skipped). 470 of 500 decoded is 30 lost, so it is kept.
func TestSourceCheckCountsFramesTheDecoderNeverDelivered(t *testing.T) {
	m := &fake.Media{Captures: []fake.CaptureScript{{Stderr: []byte(report(0, 500, 470))}}}
	v, err := SourceCheck(context.Background(), m, Recorder{}, "/original.mkv", sourceInfo(20*time.Second), time.Minute)
	if err != nil || v.OK {
		t.Fatalf("verdict = %+v, %v; want the lost pictures to keep the original", v, err)
	}
	if !strings.Contains(v.Message, "30 damaged pictures of 500") {
		t.Errorf("message = %q; want the lost 30 counted", v.Message)
	}
	if !strings.Contains(strings.Join(v.Detail, "\n"), "30 of 500 frames were not decoded") {
		t.Errorf("detail = %v; want the lost frames named", v.Detail)
	}
}

// TestSourceCheckAllowsAnAudioTailPastThePicture: the container's duration includes any audio that
// runs past the video, so with no stream duration the first max(2 s, 1 %) of the gap between the
// pictures decoded and the container's length is a tail, not damage (review2 B1 on #861: a 2.023 s
// container against 60 decoded pictures and no decode error was called damaged).
func TestSourceCheckAllowsAnAudioTailPastThePicture(t *testing.T) {
	info := sourceInfo(20 * time.Second)
	info.Video[0].Duration = 0 // an MKV often carries no stream duration
	info.Format.Duration = 20*time.Second + 600*time.Millisecond
	m := &fake.Media{Captures: []fake.CaptureScript{{Stderr: []byte(report(0, 500, 500))}}}
	v, err := SourceCheck(context.Background(), m, Recorder{}, "/original.mkv", info, time.Minute)
	if err != nil || !v.OK {
		t.Fatalf("verdict = %+v, %v; want the 0.6 s audio tail ignored", v, err)
	}
}

// TestSourceCheckKeepsPicturesLostBeyondTheAudioTail: 500 pictures are 20 s of a 25 s container, so
// five seconds of picture are missing with no decoder error line. That is past the max(2 s, 1 %) a
// tail can explain, so the original is kept (re-review on #861: a silently truncated MKV must not
// pass just because it carries no stream duration).
func TestSourceCheckKeepsPicturesLostBeyondTheAudioTail(t *testing.T) {
	info := sourceInfo(20 * time.Second)
	info.Video[0].Duration = 0
	info.Format.Duration = 25 * time.Second
	m := &fake.Media{Captures: []fake.CaptureScript{{Stderr: []byte(report(0, 500, 500))}}}
	v, err := SourceCheck(context.Background(), m, Recorder{}, "/original.mkv", info, time.Minute)
	if err != nil || v.OK || v.Code != CodeOriginalDamaged {
		t.Fatalf("verdict = %+v, %v; want the original kept for five seconds of lost pictures", v, err)
	}
}

// TestSourceCheckAllowsATailWithinTheDamageBudget: on a long file the 1 % budget is bigger than 2 s,
// so pictures may stop that much short of the container (re-review on #861: max(2 s, 1 %)).
func TestSourceCheckAllowsATailWithinTheDamageBudget(t *testing.T) {
	info := sourceInfo(10 * time.Minute) // 15 000 pictures
	info.Video[0].Duration = 0
	info.Format.Duration = 10*time.Minute + 5*time.Second // 1 % is 6.05 s
	m := &fake.Media{Captures: []fake.CaptureScript{{Stderr: []byte(report(0, 15000, 15000))}}}
	v, err := SourceCheck(context.Background(), m, Recorder{}, "/original.mkv", info, time.Minute)
	if err != nil || !v.OK {
		t.Fatalf("verdict = %+v, %v; want a five second tail inside the one percent budget", v, err)
	}
}

// TestSourceCheckCancelledIsACheckThatCouldNotRun: a Stop while the original is being decoded is
// not a verdict about the file, whatever the decoder had printed so far.
func TestSourceCheckCancelledIsACheckThatCouldNotRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := &fake.Media{Captures: []fake.CaptureScript{{
		Stderr: []byte(brokenPicture), Result: media.Result{PID: 4242},
	}}}
	if v, err := SourceCheck(ctx, m, Recorder{}, "/original.mkv", sourceInfo(20*time.Second), time.Minute); err == nil {
		t.Fatalf("verdict = %+v; a stopped run must be an error, not a verdict", v)
	}
}

// TestSourceCheckRefusesAFileLevelErrorWhateverTheCount: a container or an end of the file that is
// broken keeps the original however few pictures are named.
func TestSourceCheckRefusesAFileLevelErrorWhateverTheCount(t *testing.T) {
	m := &fake.Media{Captures: []fake.CaptureScript{{Stderr: []byte("[in#0/matroska,webm @ 0x1] [error] File ended prematurely\n" + report(0, 500000, 499900))}}}
	v, err := SourceCheck(context.Background(), m, Recorder{}, "/original.mkv", sourceInfo(5*time.Hour), time.Minute)
	if err != nil || v.OK || v.Code != CodeOriginalDamaged {
		t.Fatalf("verdict = %+v, %v; want the original kept", v, err)
	}
	if !strings.Contains(v.Message, "damaged as a whole") && !strings.Contains(v.Message, "container") {
		t.Errorf("message = %q; want the file-level reason", v.Message)
	}
}

// TestSourceCheckSaysNothingAboutAFileWithoutVideo: there is no picture to count, and the probe has
// already refused a container it cannot read.
func TestSourceCheckSaysNothingAboutAFileWithoutVideo(t *testing.T) {
	m := &fake.Media{}
	info := sourceInfo(20 * time.Second)
	info.Video = nil
	v, err := SourceCheck(context.Background(), m, Recorder{}, "/original.mp3", info, time.Minute)
	if err != nil || !v.OK {
		t.Fatalf("verdict = %+v, %v", v, err)
	}
	if len(m.CaptureArgvs()) != 0 {
		t.Error("a file without video needs no decode")
	}
}

// TestSourceCheckRunThatNeverStartedIsACheckThatCouldNotRun: a missing ffmpeg is an error (the job
// retries), never a "no" about the file.
func TestSourceCheckRunThatNeverStartedIsACheckThatCouldNotRun(t *testing.T) {
	m := &fake.Media{Captures: []fake.CaptureScript{{Err: errors.New("ffmpeg: not found")}}}
	if _, err := SourceCheck(context.Background(), m, Recorder{}, "/original.mkv", sourceInfo(20*time.Second), time.Minute); err == nil {
		t.Fatal("a run that never started must be an error")
	}
}

// TestSourceCheckDecodesTheWholeOriginal: the source check is the whole file at the error level,
// with the verbosity that adds the packet and frame report, and without -xerror (the decoder must
// play on past a damaged picture so the damage can be counted).
func TestSourceCheckDecodesTheWholeOriginal(t *testing.T) {
	m := &fake.Media{Captures: []fake.CaptureScript{{Stdout: []byte("noise\n")}}}
	if v, err := SourceCheck(context.Background(), m, Recorder{}, "/original.mkv", sourceInfo(20*time.Second), time.Minute); err != nil || !v.OK {
		t.Fatalf("verdict = %+v, %v", v, err)
	}
	argv := strings.Join(m.CaptureArgvs()[0], " ")
	for _, want := range []string{"-loglevel level+verbose", "-copyts", "-map 0:v:0 -map 0:a? ", "-f null -"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv = %q, want %q", argv, want)
		}
	}
	if strings.Contains(argv, "-xerror") {
		t.Errorf("argv = %q; a damaged picture must not stop the decode", argv)
	}
	if strings.Contains(argv, "-ss ") || strings.Contains(argv, "-t ") {
		t.Errorf("argv = %q; the source check is the whole file", argv)
	}
}

func TestSourceFrameCountPrefersTheStreamDuration(t *testing.T) {
	src := sourceInfo(20 * time.Second)
	if n, exact := sourceFrameCount(src); n != 500 || !exact {
		t.Errorf("frames = %d (exact %v); want 500 from the stream", n, exact)
	}
	src.Video[0].Duration = 0
	if n, exact := sourceFrameCount(src); n != 500 || exact {
		t.Errorf("frames = %d (exact %v); want the container's 500, not exact", n, exact)
	}
	src.Video[0].FrameRate = media.Rational{}
	if n, _ := sourceFrameCount(src); n != 0 {
		t.Errorf("frames = %d; want 0 when the rate is unknown", n)
	}
}
