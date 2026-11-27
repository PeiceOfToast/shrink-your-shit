// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media/fake"
)

// playedInfo is a result with one video stream and the given audio codecs.
func playedInfo(codecs ...string) media.Info {
	info := media.Info{
		Format: media.Format{Duration: 90 * time.Second},
		Video:  []media.VideoTrack{{Index: 0, Codec: "hevc", FrameRate: media.Rational{Num: 25, Den: 1}}},
	}
	for i, c := range codecs {
		info.Audio = append(info.Audio, media.AudioTrack{Index: i + 1, Codec: c})
	}
	return info
}

// capture plays the given stderr (and stdout) as one ffmpeg run.
func capture(stderr string) fake.CaptureScript {
	return fake.CaptureScript{Stderr: []byte(stderr)}
}

func TestPlaybackPassesACleanRun(t *testing.T) {
	m := &fake.Media{Captures: []fake.CaptureScript{capture("")}}
	v, err := playbackCheck(context.Background(), m, playedInfo("aac"), "/out.mkv")
	if v != nil || err != nil {
		t.Fatalf("verdict = %+v, %v; want a pass", v, err)
	}
	argv := strings.Join(m.CaptureArgvs()[0], " ")
	for _, want := range []string{"-xerror", "-f null -", "-map 0:v:0 -map 0:a? "} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv = %q, want %q", argv, want)
		}
	}
	if strings.Contains(argv, "-ss ") {
		t.Errorf("a whole-file check must not seek: %q", argv)
	}
}

// TestAudioTimingIsNotADecodeError: the null muxer's dts warning on an audio stream (and the
// "Last message repeated" line after it) is timing in the muxer, not a decode error (R2). The same
// warning on the video stream still counts.
func TestAudioTimingIsNotADecodeError(t *testing.T) {
	audioDTS := "[null @ 0x17cf7080] Application provided invalid, non monotonically increasing dts to muxer in stream 1: 1536 >= 1536\n" +
		"Last message repeated 1 times\n"
	m := &fake.Media{Captures: []fake.CaptureScript{capture(audioDTS)}}
	if v, err := playbackCheck(context.Background(), m, playedInfo("aac"), "/out.mkv"); v != nil || err != nil {
		t.Fatalf("verdict = %+v, %v; want the audio timing warning ignored", v, err)
	}

	videoDTS := "[null @ 0x17cf7080] Application provided invalid, non monotonically increasing dts to muxer in stream 0: 60 >= 60\n"
	m = &fake.Media{Captures: []fake.CaptureScript{capture(videoDTS)}}
	v, err := playbackCheck(context.Background(), m, playedInfo("aac"), "/out.mkv")
	if err != nil || v == nil || v.Code != CodePlayback {
		t.Fatalf("verdict = %+v, %v; want a video timestamp warning kept", v, err)
	}
	if strings.Contains(v.Message, "audio track") {
		t.Errorf("message = %q; a video warning must not name an audio track", v.Message)
	}
}

// TestAudioDamageKeepsWithTrackNamed: an error from an audio decoder is pinned to the track whose
// codec reported it, so the sentence names the track the person has to look at.
func TestAudioDamageKeepsWithTrackNamed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		line  string
		codec []string
		want  string
	}{
		{"aac is track 1", "[aac @ 0x55d1c2a0] Error decoding AAC frame header.\n", []string{"aac", "ac3"}, "audio track 1"},
		{"ac3 is track 2", "[ac3 @ 0x55d1c2a0] frame sync error\n", []string{"aac", "ac3"}, "audio track 2"},
		{"ffmpeg 7 prefix", "[aist#0:0/aac @ 0x55d1c2a0] [error] Error decoding AAC frame header.\n", []string{"aac"}, "audio track 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &fake.Media{Captures: []fake.CaptureScript{{Stderr: []byte(tc.line), Result: media.Result{ExitCode: 1, PID: 4242}, Err: errors.New("ffmpeg exited with code 1")}}}
			v, err := playbackCheck(context.Background(), m, playedInfo(tc.codec...), "/out.mkv")
			if err != nil || v == nil {
				t.Fatalf("verdict = %+v, %v; want a kept original", v, err)
			}
			if v.Code != CodePlayback || v.NextAction != NextKeepOriginal {
				t.Errorf("verdict = %+v; want check.playback with keep_original", v)
			}
			if !strings.Contains(v.Message, tc.want) {
				t.Errorf("message = %q, want %q", v.Message, tc.want)
			}
			if len(v.Detail) == 0 || !strings.Contains(strings.Join(v.Detail, "\n"), "Error decoding AAC") &&
				!strings.Contains(strings.Join(v.Detail, "\n"), "frame sync error") {
				t.Errorf("detail = %v; want ffmpeg's line quoted", v.Detail)
			}
		})
	}
}

// TestPlaybackIgnoresLibvaChatter: the driver's start-up lines reach stderr without an ffmpeg
// level marker and say nothing about the file (L-SAFE-2).
func TestPlaybackIgnoresLibvaChatter(t *testing.T) {
	chatter := "libva info: VA-API version 1.20.0\nlibva info: User environment variable requested driver 'iHD'\nlibva warning: unknown libva error, driver_name = null\n"
	m := &fake.Media{Captures: []fake.CaptureScript{capture(chatter)}}
	if v, err := playbackCheck(context.Background(), m, playedInfo("aac"), "/out.mkv"); v != nil || err != nil {
		t.Fatalf("verdict = %+v, %v; want libva chatter ignored", v, err)
	}
}

// TestPlaybackIgnoresTheOpusParserLine: ffmpeg's Opus demuxer prints one parser error for the last
// packet of a good file, with the audio not even played (ffmpeg 8).
func TestPlaybackIgnoresTheOpusParserLine(t *testing.T) {
	line := "[opus @ 0x55d1c2a0] Error parsing Opus packet header.\n"
	m := &fake.Media{Captures: []fake.CaptureScript{capture(line)}}
	if v, err := playbackCheck(context.Background(), m, playedInfo("opus"), "/out.mkv"); v != nil || err != nil {
		t.Fatalf("verdict = %+v, %v; want the Opus parser line ignored", v, err)
	}
}

// TestPlaybackErrorLinesKeepOnlyErrors: with -loglevel level+verbose every line carries its level;
// only error-level lines are damage, and the marker is removed so the text reads as -v error.
func TestPlaybackErrorLinesKeepOnlyErrors(t *testing.T) {
	raw := "" +
		"[info] ffmpeg version 7.0.2\n" +
		"[warning] [h264 @ 0x1] [warning] some warning\n" +
		"[h264 @ 0x1] [error] error while decoding MB 1 2, bytestream 3\n" +
		"[verbose]   Input stream #0:0 (video): 5 packets read (9 bytes); 5 frames decoded; 0 decode errors;\n" +
		"plain text with no marker\n" +
		"libva info: VA-API version 1.20.0\n"
	got := errorLines([]byte(raw))
	if strings.Contains(got, "ffmpeg version") || strings.Contains(got, "some warning") || strings.Contains(got, "Input stream") || strings.Contains(got, "libva") {
		t.Errorf("errorLines kept a non-error line:\n%s", got)
	}
	for _, want := range []string{"[h264 @ 0x1] error while decoding MB 1 2, bytestream 3", "plain text with no marker"} {
		if !strings.Contains(got, want) {
			t.Errorf("errorLines dropped %q:\n%s", want, got)
		}
	}
}

// TestPlaybackNamesTheErrorsWhenTheTrackIsUnknown: a decoder the file does not have, or a muxer
// line, is not pinned to a track; the sentence then counts ffmpeg's errors instead.
func TestPlaybackNamesTheErrorsWhenTheTrackIsUnknown(t *testing.T) {
	text := "[h264 @ 0x1] error while decoding MB 4 5\n" +
		"[h264 @ 0x2] error while decoding MB 6 7\n" +
		"[h264 @ 0x3] error while decoding MB 6 7\n" +
		"[h264 @ 0x4] cabac decode of qscale diff failed\n"
	m := &fake.Media{Captures: []fake.CaptureScript{{Stderr: []byte(text), Result: media.Result{ExitCode: 1, PID: 4242}, Err: errors.New("ffmpeg exited with code 1")}}}
	v, err := playbackCheck(context.Background(), m, playedInfo("aac"), "/out.mkv")
	if err != nil || v == nil {
		t.Fatalf("verdict = %+v, %v", v, err)
	}
	if strings.Contains(v.Message, "audio track") {
		t.Errorf("message = %q; a video decoder's error is not an audio track's", v.Message)
	}
	if !strings.Contains(v.Message, "4 errors") {
		t.Errorf("message = %q; want every line counted", v.Message)
	}
	// Three lines differ only in their addresses and macroblock numbers, so the detail shows two
	// different errors out of four.
	if !strings.Contains(strings.Join(v.Detail, "\n"), "2 different") {
		t.Errorf("detail = %v; want repeated errors collapsed", v.Detail)
	}
}

// TestPlaybackSampleThatDecodesNothingKeepsTheOriginal: a sample that played no frame proves
// nothing about the file, so the original is kept rather than passed silently.
func TestPlaybackSampleThatDecodesNothingKeepsTheOriginal(t *testing.T) {
	m := &fake.Media{Captures: []fake.CaptureScript{{Stdout: []byte("frame=0\nprogress=end\n")}}}
	v, err := decodeRun(context.Background(), m, "/out.mkv", playedInfo(), 0, 600, 60, 60, 25)
	if err != nil || v == nil || v.Code != CodePlayback {
		t.Fatalf("verdict = %+v, %v; want the sample's empty playback kept", v, err)
	}
	if !strings.Contains(v.Message, "played nothing") {
		t.Errorf("message = %q", v.Message)
	}
}

// TestPlaybackReportsHowFarItGot: ffmpeg's out_time is 0 with the null muxer, so the position comes
// from the frame count over the rate.
func TestPlaybackReportsHowFarItGot(t *testing.T) {
	script := fake.CaptureScript{
		Stdout: []byte("frame=42\nprogress=end\n"),
		Stderr: []byte("[h264 @ 0x1] error while decoding MB 4 5\n"),
		Result: media.Result{ExitCode: 1, PID: 4242}, Err: errors.New("ffmpeg exited with code 1"),
	}
	m := &fake.Media{Captures: []fake.CaptureScript{script, script}}
	v, err := decodeRun(context.Background(), m, "/out.mkv", playedInfo(), 0, 30, 60, 60, 25)
	if err != nil || v == nil {
		t.Fatalf("verdict = %+v, %v", v, err)
	}
	detail := strings.Join(v.Detail, "\n")
	if !strings.Contains(detail, "frame 42") || !strings.Contains(detail, "0:00:31") {
		t.Errorf("detail = %v; want frame 42 at about 0:00:31", v.Detail)
	}
}

// TestPlaybackRunThatNeverStartedIsACheckThatCouldNotRun: a missing ffmpeg is an error (the job
// retries), not a "no".
func TestPlaybackRunThatNeverStartedIsACheckThatCouldNotRun(t *testing.T) {
	m := &fake.Media{Captures: []fake.CaptureScript{{Err: errors.New("ffmpeg: not found")}}}
	if _, err := playbackCheck(context.Background(), m, playedInfo(), "/out.mkv"); err == nil {
		t.Fatal("a run that never started must be an error, so the job retries")
	}
}

// TestPlaybackCancelledIsACheckThatCouldNotRun: a Stop while the result is being played back must
// be a check that could not run, never "does not play cleanly" recorded as a finished job
// (review2 B2 on #861). A cancelled run has started (its PID is set), so it is not the
// never-started case.
func TestPlaybackCancelledIsACheckThatCouldNotRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := &fake.Media{Captures: []fake.CaptureScript{{
		Stderr: []byte("[h264 @ 0x1] error while decoding MB 4 5\n"),
		Result: media.Result{PID: 4242},
	}}}
	v, err := playbackCheck(ctx, m, playedInfo("aac"), "/out.mkv")
	if err == nil {
		t.Fatalf("verdict = %+v; a stopped playback must be an error, not a verdict", v)
	}
}

// TestPlaybackSampleThatHangsIsACheckThatCouldNotRun: the timeout turns a hung child into an
// error rather than a verdict.
func TestPlaybackSampleThatHangsIsACheckThatCouldNotRun(t *testing.T) {
	old := sampleTimeout
	sampleTimeout = 20 * time.Millisecond
	defer func() { sampleTimeout = old }()
	m := &fake.Media{Captures: []fake.CaptureScript{{Hang: true}}}
	if _, err := decodeRun(context.Background(), m, "/out.mkv", playedInfo(), 0, 0, 60, 60, 25); err == nil {
		t.Fatal("a hung sample must time out into an error")
	}
}

// TestLongPlaybackSamplesTheWholeFile: a result longer than DefaultFullDecodeMaxSec is played a
// minute at a time, so damage anywhere is within a minute of a sample (L-SAFE-2).
func TestLongPlaybackSamplesTheWholeFile(t *testing.T) {
	// Every sample plays fifty pictures and says nothing else: the last script repeats.
	m := &fake.Media{Captures: []fake.CaptureScript{{Stdout: []byte("frame=1500\nprogress=end\n")}}}
	info := playedInfo()
	info.Format.Duration = 4 * time.Hour
	if v, err := playbackCheck(context.Background(), m, info, "/out.mkv"); v != nil || err != nil {
		t.Fatalf("verdict = %+v, %v", v, err)
	}
	argvs := m.CaptureArgvs()
	if len(argvs) < 20 {
		t.Fatalf("%d samples for a four-hour file; want one every ten minutes", len(argvs))
	}
	starts := playbackStarts(info.Format.Duration.Seconds())
	if len(starts) != len(argvs) {
		t.Errorf("%d starts, %d runs", len(starts), len(argvs))
	}
	for i, start := range starts {
		if start < 0 || start+60 > info.Format.Duration.Seconds() {
			t.Errorf("sample %d starts at %.0f s, past the end of the file", i, start)
		}
		if i > 0 && start-starts[i-1] > playbackEvery {
			t.Errorf("samples %d and %d are %.0f s apart, further than %.0f", i-1, i, start-starts[i-1], playbackEvery)
		}
		argv := strings.Join(argvs[i], " ")
		if !strings.Contains(argv, "-ss "+secs(start)) || !strings.Contains(argv, "-t 60.000") {
			t.Errorf("sample %d argv = %q; want -ss %s -t 60.000", i, argv, secs(start))
		}
	}
	if last := starts[len(starts)-1]; last != info.Format.Duration.Seconds()-65 {
		t.Errorf("last sample starts at %.0f; want the file's length minus 65 s", last)
	}
}

// TestLongPlaybackFindsDamageBetweenTheSamples is L-SAFE-2's given: damage between the sample points
// the old three-point check would have used is found, because a long file is sampled throughout, and
// the sample that hit it is named.
func TestLongPlaybackFindsDamageBetweenTheSamples(t *testing.T) {
	clean := fake.CaptureScript{Stdout: []byte("frame=1500\nprogress=end\n")}
	broken := fake.CaptureScript{
		Stdout: []byte("frame=310\nprogress=end\n"),
		Stderr: []byte("[h264 @ 0x1] [error] error while decoding MB 4 5\n"),
	}
	m := &fake.Media{Captures: []fake.CaptureScript{clean, clean, clean, clean, clean, clean, clean, broken}}
	info := playedInfo()
	info.Format.Duration = 4 * time.Hour
	v, err := playbackCheck(context.Background(), m, info, "/out.mkv")
	if err != nil || v == nil || v.Code != CodePlayback {
		t.Fatalf("verdict = %+v, %v; want the damaged sample kept", v, err)
	}
	if got := len(m.CaptureArgvs()); got != 8 {
		t.Errorf("%d samples played; want a stop at the damaged one", got)
	}
	starts := playbackStarts(info.Format.Duration.Seconds())
	want := fmt.Sprintf("Check: playback test, 60 s sample from %s", clock(starts[7]))
	if len(v.Detail) == 0 || v.Detail[0] != want {
		t.Errorf("detail = %v; want %q", v.Detail, want)
	}
}

// TestPlaybackStartsNeverLeaveALargeGap: when the stretch before the last sample is bigger than the
// rest, one more sample goes in its middle.
func TestPlaybackStartsNeverLeaveALargeGap(t *testing.T) {
	for _, d := range []float64{2000, 3000, 3725, 4000, 7200, 14400, 40000} {
		starts := playbackStarts(d)
		if len(starts) == 0 {
			t.Fatalf("%.0f s: no samples", d)
		}
		for i := 1; i < len(starts); i++ {
			if gap := starts[i] - starts[i-1]; gap > playbackEvery {
				t.Errorf("%.0f s: samples %.0f and %.0f are %.0f s apart", d, starts[i-1], starts[i], gap)
			}
		}
		if last := starts[len(starts)-1]; last != d-65 {
			t.Errorf("%.0f s: last sample at %.0f, want %.0f", d, last, d-65)
		}
	}
}
