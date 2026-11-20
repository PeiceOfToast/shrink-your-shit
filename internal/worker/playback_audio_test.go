// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// argsExec is a fake ffmpeg: it keeps the arguments of its last call and answers with the stderr it is given.
type argsExec struct {
	media.Exec // the other methods are unused here
	stderr     string
	args       []string
}

func (x *argsExec) Capture(_ context.Context, _ string, args ...string) ([]byte, []byte, error) {
	x.args = args
	return nil, []byte(x.stderr), nil
}

// twoAudio is a file with one video stream and two audio streams (AAC, then AC-3), as the probe gives them.
func twoAudio() media.Info {
	return media.Info{Streams: []media.Stream{
		{Index: 0, Type: "video", Codec: "h264"},
		{Index: 1, Type: "audio", Codec: "aac"},
		{Index: 2, Type: "audio", Codec: "ac3"},
	}}
}

// The playback test plays the audio as well as the video, so damage in the audio is seen.
func TestDecodeRunMapsAudio(t *testing.T) {
	x := &argsExec{}
	w := &Worker{Exec: x, FFmpeg: "ffmpeg"}
	if err := w.decodeRun(context.Background(), "x.mkv", twoAudio(), 0, 0, 10, 24); err != nil {
		t.Fatalf("clean run: %v", err)
	}
	i := slices.Index(x.args, "-map")
	if i < 0 || !slices.Contains(x.args, "0:v:0") || !slices.Contains(x.args, "0:a?") {
		t.Fatalf("args %v: want the video map 0:v:0 and the audio map 0:a?", x.args)
	}
}

// An error line is the audio track's when its decoder is that track's codec; tracks count from 1.
func TestAudioErrorsAreAttributed(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		wantTrack []int
		wantOther bool
	}{
		{"aac audio", "[aac @ 0x1] TYPE_FIL: Input buffer exhausted", []int{1}, false},
		{"ac-3 audio is track 2", "[ac3 @ 0x2] bad frame", []int{2}, false},
		{"both tracks", "[aac @ 0x1] x\n[ac3 @ 0x2] y", []int{1, 2}, false},
		{"video only", "[h264 @ 0x3] Missing reference picture", nil, true},
		{"demuxer only", "[matroska,webm @ 0x4] EBML length exceeds max", nil, true},
		{"audio and video", "[aac @ 0x1] x\n[h264 @ 0x3] y", []int{1}, true},
		{"ffmpeg 7 prefixes", "[aist#0:0/aac @ 0x1] Error submitting packet\n[dec:ac3 @ 0x2] y", []int{1, 2}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tracks, other := audioErrors(c.text, twoAudio())
			if !slices.Equal(tracks, c.wantTrack) || other != c.wantOther {
				t.Fatalf("tracks %v other %v, want %v %v", tracks, other, c.wantTrack, c.wantOther)
			}
		})
	}
}

// Damage in the audio keeps the original, and the reason names the audio track in plain words.
func TestAudioDamageKeepsWithTrackNamed(t *testing.T) {
	x := &argsExec{stderr: "[aac @ 0x1] TYPE_FIL: Input buffer exhausted\n"}
	w := &Worker{Exec: x, FFmpeg: "ffmpeg"}
	err := w.decodeRun(context.Background(), "x.mkv", twoAudio(), 0, 0, 10, 24)
	var k *KeptError
	if !errors.As(err, &k) {
		t.Fatalf("decodeRun returned %v, want a *KeptError", err)
	}
	if want := "the new file's audio track 1 does not play cleanly"; k.Reason != want {
		t.Fatalf("reason %q, want %q", k.Reason, want)
	}
	assertPlain(t, k.Reason)
	if !strings.Contains(strings.Join(k.Detail, "\n"), "TYPE_FIL") {
		t.Errorf("detail %q does not carry the ffmpeg line", k.Detail)
	}
}

// R2: the muxer's dts warning on an audio stream is timing, not a decode error, and the "Last message repeated" line
// after it goes with it. A video stream's warning (stream 0) still counts, and a decoder's error always counts.
func TestAudioTimingIsNotADecodeError(t *testing.T) {
	const audioDTS = "[null @ 0x17cf7080] Application provided invalid, non monotonically increasing dts to muxer in stream 1: 1536 >= 1536"
	const videoDTS = "[null @ 0x17cf7080] Application provided invalid, non monotonically increasing dts to muxer in stream 0: 60 >= 60"
	const aacErr = "[aac @ 0x55d1c2a0] Error decoding AAC frame header."
	const repeated = "Last message repeated 1 times"

	// Only audio timing: nothing is left, two warnings dropped with their repeat line.
	if got, n := dropAudioTiming(audioDTS + "\n" + repeated + "\n" + audioDTS); got != "" || n != 2 {
		t.Fatalf("audio timing only: kept %q, dropped %d", got, n)
	}
	// A video warning stays, with its own repeat line; the audio one goes.
	if got, n := dropAudioTiming(audioDTS + "\n" + videoDTS + "\n" + repeated); n != 1 || strings.Contains(got, "stream 1") ||
		!strings.Contains(got, "stream 0") || !strings.Contains(got, repeated) {
		t.Fatalf("video warning must stay: kept %q, dropped %d", got, n)
	}
	// A decoder error on audio is not timing and is kept whole.
	if got, n := dropAudioTiming(aacErr + "\n" + audioDTS); n != 1 || got != aacErr {
		t.Fatalf("decoder error must stay: kept %q, dropped %d", got, n)
	}
}
