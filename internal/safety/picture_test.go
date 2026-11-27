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

// ssimScript is one comparison's answer: the summary ffmpeg prints on stderr and, when given, the
// per-frame lines it writes to the stats file on stdout.
func ssimScript(mean string, frames ...string) fake.CaptureScript {
	script := fake.CaptureScript{
		Stderr: []byte("[Parsed_ssim_4 @ 0x1] SSIM Y:0.99 U:0.99 V:0.99 All:" + mean + " (18.2)\n"),
	}
	if len(frames) > 0 {
		script.Stdout = []byte(strings.Join(frames, "\n") + "\n")
	}
	return script
}

// comparedPair is a source and a result of the same 90 s size, both with a picture that starts at
// time 0.
func comparedPair(d time.Duration) (media.Info, media.Info) {
	mk := func() media.Info {
		return media.Info{
			Path:   "/x.mkv",
			Format: media.Format{Duration: d},
			Video: []media.VideoTrack{{
				Index: 0, Codec: "h264", Width: 320, Height: 180,
				FrameRate: media.Rational{Num: 25, Den: 1}, Duration: d,
			}},
		}
	}
	src, out := mk(), mk()
	src.Path, out.Path = "/src.mkv", "/out.mkv"
	return src, out
}

func TestPicturePassesAMatchingEncode(t *testing.T) {
	src, out := comparedPair(20 * time.Second)
	m := &fake.Media{Captures: []fake.CaptureScript{ssimScript("0.991", "n:1 Y:0.99 U:0.99 V:0.99 All:0.991 (18.2)")}}
	v, err := pictureCheck(context.Background(), m, src, out, WantedVideo{Compare: true, Width: 320, Height: 180})
	if v != nil || err != nil {
		t.Fatalf("verdict = %+v, %v; want the pictures to match", v, err)
	}
	argv := strings.Join(m.CaptureArgvs()[0], " ")
	for _, want := range []string{"ssim=stats_file=-", "trim=start=0.000:duration=20.000", "setpts=PTS-(0.000000)/TB", "-f null -"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv = %q, want %q", argv, want)
		}
	}
}

// TestSSIMWorstSegmentDecides: a file that matches at its start and not in the middle is kept; the
// sentence names the place that failed and the log says where the comparison stopped.
func TestSSIMWorstSegmentDecides(t *testing.T) {
	src, out := comparedPair(90 * time.Second)
	m := &fake.Media{Captures: []fake.CaptureScript{
		ssimScript("0.990"),
		ssimScript("0.900"), ssimScript("0.900"), ssimScript("0.900"), // every alignment at the middle
	}}
	v, err := pictureCheck(context.Background(), m, src, out, WantedVideo{Compare: true, Width: 320, Height: 180})
	if err != nil || v == nil || v.Code != CodePicture || v.NextAction != NextKeepOriginal {
		t.Fatalf("verdict = %+v, %v; want check.picture with keep_original", v, err)
	}
	if !strings.Contains(v.Message, "0.900") || !strings.Contains(v.Message, "0:00:40") || !strings.Contains(v.Message, "0.93") {
		t.Errorf("message = %q; want the failing place and the floor", v.Message)
	}
	detail := strings.Join(v.Detail, "\n")
	for _, want := range []string{"0:00:40", "0:01:15 not measured"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail = %v; want %q", v.Detail, want)
		}
	}
}

// TestSSIMAPairedFrameBelowTheFloorFails: K4 fails the picture when any paired frame is below
// 0.80, even when the stretch's mean is above 0.93.
func TestSSIMAPairedFrameBelowTheFloorFails(t *testing.T) {
	src, out := comparedPair(20 * time.Second)
	m := &fake.Media{Captures: []fake.CaptureScript{
		ssimScript("0.960", "n:1 Y:0.99 U:0.99 V:0.99 All:0.990 (20)", "n:2 Y:0.70 U:0.70 V:0.70 All:0.700 (8)"),
	}}
	v, err := pictureCheck(context.Background(), m, src, out, WantedVideo{Compare: true, Width: 320, Height: 180})
	if err != nil || v == nil || v.Code != CodePicture {
		t.Fatalf("verdict = %+v, %v; want one frame below the floor to fail", v, err)
	}
	if !strings.Contains(v.Message, "0.700") || !strings.Contains(v.Message, "0.80") {
		t.Errorf("message = %q; want the failing frame and the frame floor", v.Message)
	}
	if !strings.Contains(strings.Join(v.Detail, "\n"), "frame 2") {
		t.Errorf("detail = %v; want the failing frame named", v.Detail)
	}
}

// TestSSIMGenuinelyBadStaysBadAtEveryAlignment: a picture that is different, not merely late, fails
// at every alignment. (Loosening MinMeanSSIM to 0 must make this test fail: it is the threshold
// that refuses the picture.)
func TestSSIMGenuinelyBadStaysBadAtEveryAlignment(t *testing.T) {
	src, out := comparedPair(20 * time.Second)
	m := &fake.Media{Captures: []fake.CaptureScript{ssimScript("0.500")}}
	v, err := pictureCheck(context.Background(), m, src, out, WantedVideo{Compare: true, Width: 320, Height: 180})
	if err != nil || v == nil || v.Code != CodePicture {
		t.Fatalf("verdict = %+v, %v; want the bad picture kept", v, err)
	}
	if got := len(m.CaptureArgvs()); got != 3 {
		t.Errorf("%d comparisons; want all three alignments tried", got)
	}
	if !strings.Contains(v.Message, "0.500") {
		t.Errorf("message = %q; want the score", v.Message)
	}
}

// TestSSIMAlignsTheNewFileByTheEncodersStartDelay: where a seek lands is not exact (an H.264
// original starts at the next keyframe, an HEVC result exactly at the seek point), so the same time
// stretch is cut out of both and a one-frame nudge is tried. The alignment that passes is tried
// first at the places after it.
func TestSSIMAlignsTheNewFileByTheEncodersStartDelay(t *testing.T) {
	src, out := comparedPair(90 * time.Second)
	out.Video[0].StartTime = 40 * time.Millisecond // the result starts one frame later
	m := &fake.Media{Captures: []fake.CaptureScript{
		ssimScript("0.400"), // the first place at no nudge
		ssimScript("0.990"), // ... and one frame earlier
		ssimScript("0.990"), // the second place, starting from the alignment that passed
		ssimScript("0.990"), // the third
	}}
	v, err := pictureCheck(context.Background(), m, src, out, WantedVideo{Compare: true, Width: 320, Height: 180})
	if v != nil || err != nil {
		t.Fatalf("verdict = %+v, %v; want the one-frame-early alignment to pass", v, err)
	}
	argvs := m.CaptureArgvs()
	if len(argvs) != 4 {
		t.Fatalf("%d comparisons; want 4 (one retry at the first place)", len(argvs))
	}
	first := strings.Join(argvs[0], " ")
	if !strings.Contains(first, "[1:v:0]setpts=PTS-(0.040000)/TB") {
		t.Errorf("first argv = %q; want the result cut at its own start time", first)
	}
	if second := strings.Join(argvs[1], " "); !strings.Contains(second, "[1:v:0]setpts=PTS-(0.000000)/TB") {
		t.Errorf("retry argv = %q; want the one-frame-early nudge", second)
	}
	for _, i := range []int{2, 3} {
		if argv := strings.Join(argvs[i], " "); !strings.Contains(argv, "[1:v:0]setpts=PTS-(0.000000)/TB") {
			t.Errorf("place %d argv = %q; want it to start from the alignment that passed", i, argv)
		}
	}
}

// TestSSIMUsesTheMeanWhenNoFrameLinesArrive: ffmpeg's stats file is the only per-frame source; a
// run without one is judged by its mean alone.
func TestSSIMUsesTheMeanWhenNoFrameLinesArrive(t *testing.T) {
	src, out := comparedPair(20 * time.Second)
	m := &fake.Media{Captures: []fake.CaptureScript{ssimScript("0.960")}}
	if v, err := pictureCheck(context.Background(), m, src, out, WantedVideo{Compare: true, Width: 320, Height: 180}); v != nil || err != nil {
		t.Fatalf("verdict = %+v, %v; want a mean-only pass", v, err)
	}
}

// TestSSIMNoSummaryIsANo: a comparison that printed no summary proves nothing, so the original is
// kept rather than passed.
func TestSSIMNoSummaryIsANo(t *testing.T) {
	src, out := comparedPair(20 * time.Second)
	m := &fake.Media{Captures: []fake.CaptureScript{{Stderr: []byte("nothing here\n")}}}
	v, err := pictureCheck(context.Background(), m, src, out, WantedVideo{Compare: true, Width: 320, Height: 180})
	if err != nil || v == nil || v.Code != CodePicture {
		t.Fatalf("verdict = %+v, %v; want a comparison with no result kept", v, err)
	}
	if !strings.Contains(v.Message, "gave no result") {
		t.Errorf("message = %q", v.Message)
	}
}

// TestSSIMFilterFailureIsACheckThatCouldNotRun: the ssim filter refusing (both inputs must decode
// to one size, say) is not a bad picture.
func TestSSIMFilterFailureIsACheckThatCouldNotRun(t *testing.T) {
	src, out := comparedPair(20 * time.Second)
	m := &fake.Media{Captures: []fake.CaptureScript{{
		Stderr: []byte("[Parsed_ssim_4 @ 0x1] Failed to configure output pad\n"),
		Result: media.Result{ExitCode: 1, PID: 4242}, Err: errors.New("ffmpeg exited with code 1"),
	}}}
	v, err := pictureCheck(context.Background(), m, src, out, WantedVideo{Compare: true, Width: 320, Height: 180})
	if err == nil || v != nil {
		t.Fatalf("verdict = %+v, err = %v; want an error", v, err)
	}
	if !strings.Contains(err.Error(), "Failed to configure") {
		t.Errorf("error = %v; want ffmpeg's line", err)
	}
}

// TestSampleSegments picks the compared stretches: a short file whole, a longer one at three places.
func TestSampleSegments(t *testing.T) {
	if s := sampleSegments(20); len(s) != 1 || s[0].start != 0 || s[0].length != 20 {
		t.Errorf("short file segments = %+v; want the whole 20 s", s)
	}
	s := sampleSegments(600)
	want := []segment{{0, 10}, {295, 10}, {585, 10}}
	if len(s) != len(want) {
		t.Fatalf("segments = %+v; want %+v", s, want)
	}
	for i := range want {
		if s[i] != want[i] {
			t.Errorf("segment %d = %+v; want %+v", i, s[i], want[i])
		}
	}
}

// TestPictureSizeAllowsEncoderPaddingNotAnotherPicture: an encoder may pad the coded size to its
// alignment unit (an AMD VAAPI encoder writes 1920x1088 for 1080p), but a smaller picture or a
// bigger pad is a picture the plan did not ask for.
func TestPictureSizeAllowsEncoderPaddingNotAnotherPicture(t *testing.T) {
	want := WantedVideo{Compare: true, Width: 1920, Height: 1080}
	for _, tc := range []struct {
		name     string
		w, h     int
		wantCode string
	}{
		{"the plan's size", 1920, 1080, ""},
		{"one alignment block padded", 1920, 1088, ""},
		{"a 720p picture", 1280, 720, CodePictureSize},
		{"padded by more than a block", 1920, 1120, CodePictureSize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := pictureShape(media.VideoTrack{Width: tc.w, Height: tc.h}, want)
			if tc.wantCode == "" {
				if v != nil {
					t.Fatalf("verdict = %+v; want a pass", v)
				}
				return
			}
			if v == nil || v.Code != tc.wantCode {
				t.Fatalf("verdict = %+v; want %s", v, tc.wantCode)
			}
		})
	}
}

// TestPictureColourMustBeThePlans: the tags the result carries are compared with the description
// the plan writes; a tone-mapped result is expected BT.709, and an unknown expected tag is not
// compared.
func TestPictureColourMustBeThePlans(t *testing.T) {
	tonemap := WantedVideo{Compare: true, ColorPrimaries: "bt709", ColorTransfer: "bt709", ColorSpace: "bt709"}
	if v := pictureShape(media.VideoTrack{ColorPrimaries: "bt709", ColorTransfer: "bt709", ColorSpace: "bt709"}, tonemap); v != nil {
		t.Fatalf("verdict = %+v; want BT.709 to pass", v)
	}
	v := pictureShape(media.VideoTrack{ColorPrimaries: "bt2020", ColorTransfer: "smpte2084", ColorSpace: "bt2020nc"}, tonemap)
	if v == nil || v.Code != CodeColor {
		t.Fatalf("verdict = %+v; want the colour description refused", v)
	}
	for _, want := range []string{"transfer bt709 (the result says smpte2084)", "primaries bt709 (the result says bt2020)"} {
		if !strings.Contains(v.Message, want) {
			t.Errorf("message = %q, want %q", v.Message, want)
		}
	}
	if v := pictureShape(media.VideoTrack{}, tonemap); v == nil || !strings.Contains(v.Message, "says none") {
		t.Errorf("verdict = %+v; want an absent tag named as none", v)
	}
	if v := pictureShape(media.VideoTrack{}, WantedVideo{Compare: true}); v != nil {
		t.Errorf("verdict = %+v; an unknown expected description is not compared", v)
	}
}

// TestPictureCheckRefusesAWrongShapeBeforeDecoding: the size and colour are known from the probe,
// so a wrong picture costs no decode.
func TestPictureCheckRefusesAWrongShapeBeforeDecoding(t *testing.T) {
	src, out := comparedPair(20 * time.Second)
	out.Video[0].Width, out.Video[0].Height = 160, 90
	m := &fake.Media{}
	v, err := pictureCheck(context.Background(), m, src, out, WantedVideo{Compare: true, Width: 320, Height: 180})
	if err != nil || v == nil || v.Code != CodePictureSize {
		t.Fatalf("verdict = %+v, %v; want the shape refused", v, err)
	}
	if len(m.CaptureArgvs()) != 0 {
		t.Errorf("%d decodes ran; a wrong shape is known from the probe", len(m.CaptureArgvs()))
	}
}

// matchedPair is goodInfo's streams with a picture the plan knows, for the checks after the probe.
func matchedPair(d time.Duration) (src, out media.Info) {
	mk := func() media.Info {
		info := goodInfo(d)
		info.Video[0].Width, info.Video[0].Height = 320, 180
		info.Video[0].FrameRate = media.Rational{Num: 25, Den: 1}
		info.Video[0].Duration = d
		return info
	}
	return mk(), mk()
}

// TestPictureIsNotComparedWhenTheVideoIsCopied: a copied video has no new picture; the check runs
// the playback test and stops there.
func TestPictureIsNotComparedWhenTheVideoIsCopied(t *testing.T) {
	src, out := matchedPair(20 * time.Second)
	st, _ := stagedWith(t, "x")
	m := &fake.Media{Probes: map[string]media.Info{st.Path: out}, Captures: []fake.CaptureScript{{}}}
	v, err := Verify(context.Background(), m, Recorder{}, st, src, wantAll, time.Minute)
	if err != nil || !v.OK {
		t.Fatalf("verdict = %+v, %v; want a pass", v, err)
	}
	if got := len(m.CaptureArgvs()); got != 1 {
		t.Errorf("%d ffmpeg runs; want only the playback test", got)
	}
	if argv := strings.Join(m.CaptureArgvs()[0], " "); !strings.Contains(argv, "-xerror") {
		t.Errorf("argv = %q; want the playback test", argv)
	}
}

// TestPictureRunsAfterPlaybackInVerify: with a new picture to compare, the picture check is the
// last one and its verdict is the run's.
func TestPictureRunsAfterPlaybackInVerify(t *testing.T) {
	src, out := matchedPair(20 * time.Second)
	st, _ := stagedWith(t, "x")
	src.Path, out.Path = "/src.mkv", st.Path
	m := &fake.Media{
		Probes: map[string]media.Info{st.Path: out},
		Captures: []fake.CaptureScript{
			{},                 // playback
			ssimScript("0.40"), // the picture, at every alignment
		},
	}
	want := wantAll
	want.Video = WantedVideo{Compare: true, Width: 320, Height: 180}
	v, err := Verify(context.Background(), m, Recorder{}, st, src, want, time.Minute)
	if err != nil || v.OK || v.Code != CodePicture {
		t.Fatalf("verdict = %+v, %v; want the picture check's no", v, err)
	}
	if got := len(m.CaptureArgvs()); got != 4 {
		t.Errorf("%d ffmpeg runs; want playback then three alignments", got)
	}
}
