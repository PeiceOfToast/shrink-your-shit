// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// K4's calibrated picture thresholds: the result fails when one compared stretch's mean SSIM is
// below MinMeanSSIM, or when any single paired frame is below MinFrameSSIM (build-order K4,
// 11-27; it replaces L-SAFE-3's uncalibrated 0.95).
const (
	MinMeanSSIM  = 0.93
	MinFrameSSIM = 0.80
)

// seekMargin is how far before a sample both files are sought. Where a seek lands is not exact:
// in MPEG-TS, ffmpeg starts an H.264 original at the next keyframe after the seek point but an
// HEVC result exactly at it. The comparison therefore never trusts where a seek landed: it decodes
// from the margin earlier, keeps the real timestamps, and cuts the same stretch out of both by time
// (L-SAFE-3's alignment).
const seekMargin = 12.0

// segment is one compared stretch, in seconds from the start of the video.
type segment struct{ start, length float64 }

// sampleSegments picks where to compare: the whole of a short result, otherwise ten seconds at the
// start, the middle and the end (L-SAFE-3).
func sampleSegments(duration float64) []segment {
	switch {
	case duration <= 0:
		return []segment{{0, 10}}
	case duration < 30:
		return []segment{{0, duration}}
	}
	return []segment{{0, 10}, {duration/2 - 5, 10}, {duration - 15, 10}}
}

// placeScore is one place's picture similarity as it was measured.
type placeScore struct{ at, score float64 }

// frameScore is one paired frame's picture similarity; n counts from 1 within the compared stretch.
type frameScore struct {
	n     int
	score float64
}

// pictureCheck compares the result's picture with the original's (L-SAFE-3) after the size and
// colour description the plan writes (K4). It returns (verdict, nil) for a "no" and (nil, err) for
// a check that could not run.
func pictureCheck(ctx context.Context, c media.Capturer, src, out media.Info, want WantedVideo) (*Verdict, error) {
	sv, ov := src.MainVideo(), out.MainVideo()
	if sv == nil || ov == nil {
		v := noVerdict(CodePicture, "there is no picture to compare")
		return &v, nil
	}
	if v := pictureShape(*ov, want); v != nil {
		return v, nil
	}
	if src.Path == "" || out.Path == "" {
		return nil, errors.New("picture: a file has no path to compare")
	}

	frame := 1.0 / 25
	if sv.FrameRate.Float() > 0 {
		frame = 1 / sv.FrameRate.Float()
	}
	segs := sampleSegments(src.Format.Duration.Seconds())
	scale := scaleTo(*sv, *ov)
	srcStart, outStart := sv.StartTime.Seconds(), ov.StartTime.Seconds()
	svOrdinal, ovOrdinal := src.MainVideoOrdinal(), out.MainVideoOrdinal()

	// A file has one alignment, so the one that passes a place is tried first on the others. The
	// extra tries only pay off once, and each one is a seek and decode of both files.
	nudges := []float64{0, -frame, frame}
	var done []placeScore
	var bestFrames []frameScore
	for _, seg := range segs {
		best := -1.0
		passed := false
		for _, nudge := range nudges {
			mean, frames, ok, err := ssimSegment(ctx, c, src.Path, out.Path, svOrdinal, ovOrdinal, seg, srcStart, outStart+nudge, scale)
			if err != nil {
				return nil, err
			}
			if !ok {
				v := noVerdict(CodePicture, "the picture comparison at "+clock(seg.start)+" gave no result")
				return &v, nil
			}
			if mean > best {
				best, bestFrames = mean, frames
			}
			if mean >= MinMeanSSIM && worstFrame(frames, mean) >= MinFrameSSIM {
				passed = true
				nudges = nudgeFirst(nudges, nudge)
				break
			}
		}
		done = append(done, placeScore{at: seg.start, score: best})
		if !passed {
			msg := fmt.Sprintf(
				"The new file's picture does not match the original: similarity %.3f at %s, and %.2f is required.",
				best, clock(seg.start), MinMeanSSIM)
			if low := worstFrame(bestFrames, best); best >= MinMeanSSIM && low < MinFrameSSIM {
				msg = fmt.Sprintf(
					"The new file's picture does not match the original at %s: one matched frame is only %.3f similar, and %.2f is required.",
					clock(seg.start), low, MinFrameSSIM)
			}
			v := noVerdict(CodePicture, msg)
			v.Detail = pictureDetail(segs, done, bestFrames, sv.FrameRate.Float())
			return &v, nil
		}
	}
	return nil, nil
}

// pictureShape compares the probe's picture size and colour description with what the plan writes:
// both are known from the probe alone, so a wrong picture is refused before either file is decoded.
func pictureShape(out media.VideoTrack, want WantedVideo) *Verdict {
	if want.Width > 0 && want.Height > 0 && !sameCodedsize(want.Width, want.Height, out.Width, out.Height) {
		v := noVerdict(CodePictureSize, fmt.Sprintf(
			"The new file's picture is %dx%d, and the plan writes %dx%d.",
			out.Width, out.Height, want.Width, want.Height))
		return &v
	}
	var wrong []string
	for _, tag := range []struct {
		name, want, got string
	}{
		{"primaries", want.ColorPrimaries, out.ColorPrimaries},
		{"transfer", want.ColorTransfer, out.ColorTransfer},
		{"space", want.ColorSpace, out.ColorSpace},
	} {
		if tag.want == "" || strings.EqualFold(tag.got, tag.want) {
			continue
		}
		got := tag.got
		if got == "" {
			got = "none"
		}
		wrong = append(wrong, tag.name+" "+tag.want+" (the result says "+got+")")
	}
	if len(wrong) > 0 {
		v := noVerdict(CodeColor, "The new file's colour description is not the plan's: "+strings.Join(wrong, ", ")+".")
		return &v
	}
	return nil
}

// sameCodedsize reports whether the output's coded picture is the plan's size. An encoder may pad
// the coded size to its alignment unit (an AMD VAAPI encoder writes 1920x1088 for a 1080p plan), so
// the output may be up to one alignment block bigger on each side; smaller, or bigger by more, is a
// picture the plan did not ask for.
func sameCodedsize(wantW, wantH, gotW, gotH int) bool {
	const pad = 32
	return gotW >= wantW && gotW-wantW <= pad && gotH >= wantH && gotH-wantH <= pad
}

// nudgeFirst puts the alignment that passed first, so the next place starts from it.
func nudgeFirst(nudges []float64, passed float64) []float64 {
	out := []float64{passed}
	for _, n := range nudges {
		if n != passed {
			out = append(out, n)
		}
	}
	return out
}

// worstFrame is the lowest paired frame's similarity; a run with no per-frame lines is judged by
// its mean alone.
func worstFrame(frames []frameScore, mean float64) float64 {
	if len(frames) == 0 {
		return mean
	}
	worst := frames[0].score
	for _, f := range frames[1:] {
		if f.score < worst {
			worst = f.score
		}
	}
	return worst
}

// pictureDetail is the job log's account of a picture refusal: where the comparison ran, the score
// at each place, and the worst frames of the failing place.
func pictureDetail(segs []segment, done []placeScore, frames []frameScore, fps float64) []string {
	var lines []string
	if len(segs) == 1 && segs[0].length < 30 {
		lines = append(lines, "Check: picture comparison (SSIM) of the whole file")
	} else {
		var at []string
		for _, s := range segs {
			at = append(at, clock(s.start))
		}
		lines = append(lines, fmt.Sprintf("Check: picture comparison (SSIM) at %d places, %d s each: %s",
			len(segs), int(segs[0].length), strings.Join(at, ", ")))
	}
	var measured []string
	for _, p := range done {
		measured = append(measured, fmt.Sprintf("%s %.3f", clock(p.at), p.score))
	}
	scores := strings.Join(measured, ", ") + fmt.Sprintf(" (%.2f required)", MinMeanSSIM)
	if rest := segs[len(done):]; len(rest) > 0 {
		var na []string
		for _, s := range rest {
			na = append(na, clock(s.start)+" not measured")
		}
		scores += "; " + strings.Join(na, ", ")
	}
	lines = append(lines, "Scores: "+scores,
		fmt.Sprintf("Alignment: the new file was also tried one frame earlier and later; %.2f is the lowest frame allowed", MinFrameSSIM))
	if len(frames) > 0 {
		worst := append([]frameScore(nil), frames...)
		sort.SliceStable(worst, func(i, j int) bool { return worst[i].score < worst[j].score })
		if len(worst) > 3 {
			worst = worst[:3]
		}
		at := done[len(done)-1].at
		var shown []string
		for _, f := range worst {
			where := fmt.Sprintf("frame %d", f.n)
			if fps > 0 {
				where += " at " + clock(at+float64(f.n-1)/fps)
			}
			shown = append(shown, fmt.Sprintf("%s (%.3f)", where, f.score))
		}
		lines = append(lines, "Worst frames: "+strings.Join(shown, ", "))
	}
	return lines
}

// ssimRe reads ffmpeg's summary line, e.g. "SSIM Y:0.98 U:0.99 V:0.99 All:0.985 (18.2)".
var ssimRe = regexp.MustCompile(`SSIM\b.*\bAll:([0-9.]+)`)

// ssimFrameRe reads ffmpeg's per-frame stats lines, e.g.
// "n:12 Y:0.991 U:0.998 V:0.997 All:0.993 (21.5)".
var ssimFrameRe = regexp.MustCompile(`(?m)^n:([0-9]+) .*?All:([0-9.]+)`)

func parseFrameScores(stdout []byte) []frameScore {
	var out []frameScore
	for _, m := range ssimFrameRe.FindAllSubmatch(stdout, -1) {
		n, err1 := strconv.Atoi(string(m[1]))
		score, err2 := strconv.ParseFloat(string(m[2]), 64)
		if err1 == nil && err2 == nil {
			out = append(out, frameScore{n: n, score: score})
		}
	}
	return out
}

// ssimSegment compares seg of the original's video stream sv with the new file's stream ov. srcStart
// and outStart are the times at which each file's video starts; subtracting them puts both on one
// timeline. The frames are then paired in the order they come out of the two trims: frame i of the
// result against frame i of the original, with the alignment nudge applied to the result's start.
func ssimSegment(ctx context.Context, c media.Capturer, srcPath, outPath string, sv, ov int, seg segment, srcStart, outStart float64, scale string) (mean float64, frames []frameScore, ok bool, err error) {
	from := seg.start - seekMargin
	if from < 0 {
		from = 0
	}
	cut := func(in, stream int, start float64, extra, label string) string {
		// A start time can be negative (MP4 edit lists), so it is formatted as a number, not by secs.
		return fmt.Sprintf("[%d:v:%d]setpts=PTS-(%s)/TB,trim=start=%s:duration=%s%s[%s]",
			in, stream, strconv.FormatFloat(start, 'f', 6, 64), secs(seg.start), secs(seg.length), extra, label)
	}
	// Reading from the start needs no seek, and must not have one: in an AVI, "-ss 0" jumps to a
	// later keyframe. No input -t either: it counts from where the seek lands, and would cut the
	// sample short; the trim filters end the stretch and ffmpeg stops reading once they are done.
	input := func(path string) []string {
		a := []string{"-noaccurate_seek"}
		if from > 0 {
			a = append(a, "-ss", secs(from))
		}
		return append(a, "-i", path)
	}
	args := []string{"ffmpeg", "-hide_banner", "-nostdin", "-v", "info", "-nostats", "-copyts"}
	args = append(args, input(srcPath)...)
	args = append(args, input(outPath)...)
	args = append(args,
		"-lavfi", cut(0, sv, srcStart, scale, "a")+";"+cut(1, ov, outStart, "", "b")+";[a][b]ssim=stats_file=-",
		"-f", "null", "-")
	sctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()
	res, err := c.Capture(sctx, args)
	if ctx.Err() != nil {
		// The person pressed Stop: the comparison was cut short by us, so it says nothing about
		// the pictures (the same rule as the playback and source checks, review2 B2 on #861).
		return 0, nil, false, fmt.Errorf("picture comparison: %w", ctx.Err())
	}
	if errors.Is(sctx.Err(), context.DeadlineExceeded) {
		return 0, nil, false, fmt.Errorf("picture comparison: %w", err)
	}
	if err != nil {
		if res.Result.PID == 0 {
			return 0, nil, false, fmt.Errorf("picture comparison: %w", err)
		}
		// The filter itself refused (both inputs must decode to the same size, say): that is a
		// comparison that could not run, not a bad picture.
		return 0, nil, false, fmt.Errorf("picture comparison at %s: %w%s", clock(seg.start), err, lastStderrLine(res.Stderr))
	}
	m := ssimRe.FindSubmatch(res.Stderr)
	if m == nil {
		return 0, nil, false, nil
	}
	score, perr := strconv.ParseFloat(string(m[1]), 64)
	if perr != nil {
		return 0, nil, false, nil
	}
	return score, parseFrameScores(res.Stdout), true, nil
}

// lastStderrLine is ": <line>" for the last non-empty line of an ffmpeg run, or "".
func lastStderrLine(stderr []byte) string {
	lines := strings.Split(strings.TrimSpace(string(stderr)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return ": " + cutRunes(l, 200)
		}
	}
	return ""
}

// scaleTo is the filter that brings the original's picture to the new file's coded size when a flow
// scaled it down, so the comparison pairs pictures of one size: "" when they are equal.
func scaleTo(src, out media.VideoTrack) string {
	if out.Width <= 0 || out.Height <= 0 || (src.Width == out.Width && src.Height == out.Height) {
		return ""
	}
	return fmt.Sprintf(",scale=%d:%d", out.Width, out.Height)
}
