// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Defaults for the safety checks.
const (
	// DefaultMinSSIM is the lowest picture similarity (0-1) accepted between the original and the new
	// file. 0.95 is a STARTING value, not a tuned one: it has not been calibrated on real material.
	DefaultMinSSIM = 0.95
	// DefaultFullDecodeMaxSec is the longest file that is decoded completely in "auto" mode.
	DefaultFullDecodeMaxSec = 3 * 3600
)

// Time limits for single ffmpeg/ffprobe calls in the checks, so a hung child cannot freeze the worker:
// it becomes "kept original: ... timed out". (The encode itself has no limit here: it legitimately
// takes hours; a watchdog for it is a later slice.) They are generous guesses and variables so tests
// can shorten them.
var (
	quickTimeout  = 60 * time.Second // ffprobe of one file, listing filters
	countTimeout  = 30 * time.Minute // reading every packet of a very large file
	sampleTimeout = 10 * time.Minute // one sampled playback or picture comparison
	// a full playback gets 10 minutes plus twice the file's length, but never more than 6 hours
	fullDecodeBase, fullDecodeCap = 10 * time.Minute, 6 * time.Hour
)

func fullDecodeTimeout(durationSec float64) time.Duration {
	d := fullDecodeBase + time.Duration(2*durationSec*float64(time.Second))
	if d > fullDecodeCap {
		d = fullDecodeCap
	}
	return d
}

// checkFailed turns an error from a check's ffmpeg/ffprobe call into the refusal reason, saying so
// plainly when it was our time limit that ended it.
func checkFailed(cctx context.Context, what string, err error) error {
	log.Printf("check failed: %s: %v", what, err) // the technical detail, for the log and Settings > About
	// both may pass (a slow or busy disk, a crash of the tool): tried again later (DD-2)
	if errors.Is(cctx.Err(), context.DeadlineExceeded) {
		return retryable(kept("%s took too long, so the original is kept. Press Retry later; a very slow disk or a damaged file can cause this", what), retryGeneral)
	}
	return retryable(kept("%s could not be done, so the original is kept. Press Retry; if it keeps failing, the file may be damaged", what), retryGeneral)
}

// KeptError means a safety check refused the new file (or could not run): the original is kept.
type KeptError struct {
	Code, Reason string   // Code: one of jobs.JobReasonCodes
	Detail       []string // the technical lines for the job log: which check, how far it got, the errors
}

func (e *KeptError) Error() string { return "kept original: " + e.Reason }

// pictureTooDifferent begins the picture check's refusal.
const pictureTooDifferent = "the picture of the new file is too different from the original"

func kept(format string, a ...any) error {
	return &KeptError{Code: jobs.ReasonKeptOriginal, Reason: fmt.Sprintf(format, a...)}
}

// verification is the judging settings in force for a result checked against exp.
func (w *Worker) verification(exp rules.Expect) rules.Verification {
	v := rules.Verification{MinSSIM: w.minSSIM(), DecodeCheck: "auto", FullDecodeMaxSec: w.FullDecodeMaxSec}
	if exp.MinSSIM > 0 {
		v.MinSSIM = exp.MinSSIM
	}
	if w.DecodeCheck == "always" {
		v.DecodeCheck = "always"
	}
	if v.FullDecodeMaxSec <= 0 {
		v.FullDecodeMaxSec = DefaultFullDecodeMaxSec
	}
	return v
}

func (w *Worker) minSSIM() float64 {
	if w.MinSSIM > 0 {
		return w.MinSSIM
	}
	return DefaultMinSSIM
}

func (w *Worker) freeSpace(dir string) (uint64, error) {
	if w.FreeSpace != nil {
		return w.FreeSpace(dir)
	}
	return diskFree(dir)
}

// SpaceNeeded is the free space converting a file of size bytes needs on its disk: its size plus a
// tenth plus 64 MB (the new file is normally smaller, but not guaranteed to be). The doctor (slice
// 20) uses the same rule for a whole folder, with its largest video.
func SpaceNeeded(size int64) uint64 {
	return uint64(size) + uint64(size)/10 + 64<<20
}

// DiskFree is the space this user may write on the volume holding dir.
func DiskFree(dir string) (uint64, error) { return diskFree(dir) }

// DiskSize is the size of the disk dir is on.
func DiskSize(dir string) (uint64, error) { return diskSize(dir) }

// preflight refuses to start when the volume cannot hold the new file next to the original: about the
// original's size plus a margin (the new file is normally smaller, but not guaranteed to be).
func (w *Worker) preflight(sourcePath string, sourceSize int64) error {
	dir := filepath.Dir(sourcePath)
	free, err := w.freeSpace(dir)
	if err != nil {
		log.Printf("free space of %s: %v", dir, err)
		return retryable(kept("the free disk space could not be checked, so converting did not start. Press Retry"), retryGeneral)
	}
	need := SpaceNeeded(sourceSize)
	if free < need {
		ke := kept("there is not enough free disk space next to this file: about %s is needed to convert it safely, %s is free",
			rules.HumanBytes(int64(need)), rules.HumanBytes(int64(free))).(*KeptError)
		ke.Code = jobs.ReasonLowDiskSpace
		return retryable(ke, retrySpace)
	}
	return nil
}

// verify is the safety floor. It decides whether the new file at tmp may replace the original, by
// comparing it with exp (what it must look like), never with the original directly. Every failure, and
// every check that cannot run, is a KeptError: there is no "replace anyway". Cheap checks first, the
// expensive ones (playback test, picture comparison) only for files that already look right.
func (w *Worker) verify(ctx context.Context, src media.Info, exp rules.Expect, srcPath, tmp string, origSize int64) error {
	st, err := os.Stat(tmp)
	if err != nil {
		log.Printf("new file %s: %v", tmp, err)
		return kept("the new file is missing")
	}
	if st.Size() == 0 {
		return kept("the new file is empty")
	}
	pctx, cancel := context.WithTimeout(ctx, quickTimeout)
	out, err := media.Probe(pctx, w.Exec, w.FFprobe, tmp)
	cancel()
	if err != nil {
		if errors.Is(pctx.Err(), context.DeadlineExceeded) {
			return checkFailed(pctx, "reading the new file", err)
		}
		log.Printf("reading the new file %s: %v", tmp, err)
		return kept("the new file cannot be read")
	}
	w.verified = out // the new file's probe, kept for the Files view once it is in place (S5)
	outPackets := int64(-1)
	if exp.VideoPackets > 0 {
		ord := out.MainVideoOrdinal()
		if ord < 0 {
			return kept("the new file has no video")
		}
		cctx, ccancel := context.WithTimeout(ctx, countTimeout)
		outPackets, err = media.CountVideoPackets(cctx, w.Exec, w.FFprobe, tmp, ord)
		ccancel()
		if err != nil {
			return checkFailed(cctx, "counting the new file's video frames", err)
		}
	}
	if problems := exp.Compare(out, outPackets); len(problems) > 0 {
		return kept("%s", strings.Join(problems, "; "))
	}
	// No size check: a checked conversion is kept even if it is not smaller ("always standardize",
	// USER-REVIEW 2026-11-07). Its before and after sizes are recorded on the job and in the log.
	if err := w.decodeCheck(ctx, out, tmp); err != nil {
		return err
	}
	if exp.VideoCopied { // repackaged (S4): the video is the same bytes, so there is no picture to compare
		return nil
	}
	if w.remote != nil { // the remote worker compared the pictures itself, against its copy (Q4 a)
		return nil
	}
	return w.ssimCheck(ctx, src, out, srcPath, tmp, exp.MinSSIM)
}

// ---- playback test ----

// decodeCheck plays the new file's video and audio through ffmpeg's decoders to nowhere: any error means a
// damaged file.
// There is no way to turn it off: the floor is not removable (only "always" vs the default "auto").
func (w *Worker) decodeCheck(ctx context.Context, out media.Info, tmp string) error {
	limit := w.FullDecodeMaxSec
	if limit <= 0 {
		limit = DefaultFullDecodeMaxSec
	}
	var fps float64 // the frame rate turns ffmpeg's frame count into a position for the job log
	if v := out.Video(); v != nil {
		fps = v.FrameRate
	}
	if w.DecodeCheck == "always" || out.DurationSec <= 0 || out.DurationSec <= limit {
		return w.decodeRun(ctx, tmp, out, 0, 0, out.DurationSec, fps)
	}
	// Long file: three samples of a minute each, at the start, middle and end.
	for _, start := range []float64{0, out.DurationSec/2 - 30, out.DurationSec - 65} {
		if err := w.decodeRun(ctx, tmp, out, start, 60, 60, fps); err != nil {
			return err
		}
	}
	return nil
}

// opusParserRe is the one message ffmpeg 8's demuxer prints for the last Opus packet of a good file, even
// when only the video is played (ffmpeg 8.0.1: any Opus audio in MKV/WebM, ours or not; the file plays
// to the end and the exit status is 0). It says nothing about the video that is being checked.
var opusParserRe = regexp.MustCompile(`(?m)^\[opus @ [0-9a-fx]+\] Error parsing Opus packet header\.\s*$`)

// addrRe and numRe make two ffmpeg error lines the same when only their addresses and numbers differ.
var (
	addrRe = regexp.MustCompile(` @ 0x[0-9a-fA-F]+`)
	numRe  = regexp.MustCompile(`[0-9]+`)
)

func (w *Worker) decodeRun(ctx context.Context, tmp string, out media.Info, start, length, playSec, fps float64) error {
	args := []string{"-v", "error", "-xerror", "-nostdin", "-progress", "pipe:1"}
	// -ss and -t go before -i as input options. With -copyts an output -t is an absolute end time, so a
	// sample starting at 5 s with length 3 played nothing.
	if length > 0 {
		args = append(args, "-ss", secs(start), "-t", secs(length))
	}
	// -copyts: keep the file's own timestamps. Without it ffmpeg shifts AAC's priming (about -21 ms) into
	// the video, and the null muxer then reports false "non monotonically increasing dts" errors.
	args = append(args, "-copyts", "-i", tmp)
	// The video and the audio are played: both are what this program wrote. Subtitles and attachments are
	// copied bit for bit from the original, so any oddity in them was already in the original.
	args = append(args, "-map", fmt.Sprintf("0:v:%d", out.MainVideoOrdinal()), "-map", "0:a?", "-f", "null", "-")
	timeout := sampleTimeout
	if length == 0 {
		timeout = fullDecodeTimeout(playSec)
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	progress, raw, err := w.Exec.Capture(dctx, w.FFmpeg, args...)
	// The null muxer's dts warnings on an audio stream are timing, not decode errors (R2); video ones still count.
	stderr, dropped := dropAudioTiming(string(raw))
	if dropped > 0 {
		log.Printf("playback test of %s: %d audio timestamp warnings from the muxer are not decode errors", tmp, dropped)
	}
	text := strings.TrimSpace(opusParserRe.ReplaceAllString(stderr, ""))
	if errors.Is(dctx.Err(), context.DeadlineExceeded) {
		return checkFailed(dctx, "the playback test of the new file", err)
	}
	if err != nil || text != "" {
		if text == "" {
			text = err.Error()
		}
		log.Printf("playback test of %s: %s", tmp, firstLines(text, 3))
		err := playbackKept(progress, stderr, start, length, playSec, fps)
		if ke, ok := err.(*KeptError); ok {
			if tracks, other := audioErrors(text, out); len(tracks) > 0 {
				ke.Reason = audioReason(tracks, other)
			}
		}
		return err
	}
	// A sample that played no frames proves nothing: keep the original rather than pass it silently.
	if frame, ok := progressFrame(progress); length > 0 && (!ok || frame == 0) {
		return playedNothingKept(progress, start, length, fps)
	}
	return nil
}

// playedNothingKept is the refusal for a sampled playback test that decoded no frames.
func playedNothingKept(progress []byte, start, length, fps float64) error {
	ke := kept("the playback test of the new file played nothing from its sample, so the original is kept").(*KeptError)
	where := "Where: playback did not report how far it got"
	if frame, ok := progressFrame(progress); ok {
		where = fmt.Sprintf("Where: playback reached frame %d", frame)
		if fps > 0 {
			where += fmt.Sprintf(" (about %s)", rules.Clock(start+float64(frame)/fps))
		}
	}
	ke.Detail = []string{fmt.Sprintf("Check: playback test, %d s sample from %s", int(length), rules.Clock(start)), where}
	return ke
}

// playbackKept is the refusal for a file that does not play cleanly. The reason is plain; the job log gets the
// technical detail: which check ran, how far playback got, and ffmpeg's distinct error lines.
func playbackKept(progress []byte, stderr string, start, length, playSec, fps float64) error {
	var lines []string
	for _, l := range strings.Split(stderr, "\n") {
		if l = strings.TrimSpace(l); l != "" && !opusParserRe.MatchString(l) {
			lines = append(lines, l)
		}
	}
	seen := map[string]bool{}
	var shown []string
	for _, l := range lines {
		key := numRe.ReplaceAllString(addrRe.ReplaceAllString(l, ""), "#")
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(shown) < 5 {
			shown = append(shown, cutRunes(l, 200))
		}
	}
	n := len(lines)
	reason := "the new file does not play cleanly"
	switch {
	case n == 1:
		reason += ": it reported 1 error when played back"
	case n > 1:
		reason += fmt.Sprintf(": it reported %d errors when played back", n)
	default:
		reason += " (playback stopped with an error)"
	}
	check := fmt.Sprintf("Check: playback test, %d s sample from %s", int(length), rules.Clock(start))
	if length == 0 {
		check = "Check: playback test of the whole new file (0:00:00 to " + rules.Clock(playSec) + ")"
	}
	// ffmpeg's out_time is 0 when the output is the null muxer, so the position is the frame count over the rate.
	where := "Where: playback did not report how far it got"
	if frame, ok := progressFrame(progress); ok {
		where = fmt.Sprintf("Where: playback reached frame %d", frame)
		if fps > 0 {
			where += fmt.Sprintf(" (about %s)", rules.Clock(start+float64(frame)/fps))
		}
	}
	ke := kept("%s", reason).(*KeptError)
	ke.Detail = append([]string{check, where, fmt.Sprintf("Errors: %d error lines from ffmpeg, %d different", n, len(seen))}, shown...)
	return ke
}

// progressFrame reads ffmpeg's -progress output: the last frame playback reached.
func progressFrame(out []byte) (frame int64, ok bool) {
	for _, l := range strings.Split(string(out), "\n") {
		k, v, found := strings.Cut(strings.TrimSpace(l), "=")
		if !found || k != "frame" {
			continue
		}
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			frame, ok = n, true
		}
	}
	return frame, ok
}

func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// ---- picture quality ----

var ssimRe = regexp.MustCompile(`SSIM\b.*\bAll:([0-9.]+)`)

// ssimCheck compares the picture of the original and the new file at up to three places, using
// ffmpeg's built-in SSIM measure (1.0 = identical). It needs ffmpeg's ssim filter; without it the
// check cannot run, and then the original is kept.
func (w *Worker) ssimCheck(ctx context.Context, src, out media.Info, srcPath, tmp string, planSSIM float64) error {
	sv, ov := src.MainVideoOrdinal(), out.MainVideoOrdinal()
	if sv < 0 || ov < 0 {
		return kept("there is no video to compare")
	}
	has, err := w.hasFilter(ctx, "ssim")
	if err != nil {
		log.Printf("picture comparison filter: %v", err)
		return kept("it could not be checked whether picture quality can be compared. Press Retry; if it keeps failing, Settings > About says what is wrong")
	}
	if !has {
		return kept("the installed converter cannot compare picture quality, which is needed before a file is replaced. Settings > About says what to install")
	}
	threshold := w.minSSIM()
	if planSSIM > 0 { // the flow's own threshold (a slider position's SSIM gate)
		threshold = planSSIM
	}
	segs := sampleSegments(src.DurationSec)
	// Both pictures are put on one timeline by their own video start time (the encoder can delay the
	// first frame: measured, a good encode scored 0.93 instead of 0.985 without this). If that still
	// scores low, the new file is tried one frame earlier and later too and the best counts: a
	// genuinely bad encode stays low at every alignment, a merely misaligned good one jumps up.
	sMain, oMain := src.Video(), out.Video()
	frame := 1.0 / 25
	if sMain.FrameRate > 0 {
		frame = 1 / sMain.FrameRate
	}
	var done []placeScore // the places measured so far, for the job log
	// A file has one alignment, so the one that passes a place is tried first on the others (P3): the
	// extra tries only ever pay off once, and each one is a seek and decode of both files.
	nudges := []float64{0, -frame, frame}
	for _, seg := range segs {
		best := -1.0
		var bestFrames []frameScore // the best alignment's frames: the worst of them are the job log's evidence
		passed := nudges[0]
		for _, nudge := range nudges {
			score, frames, err := w.ssimSegment(ctx, srcPath, tmp, sv, ov, seg, sMain.StartTime, oMain.StartTime+nudge, scaleTo(sMain, oMain))
			if err != nil {
				return err
			}
			if score > best {
				best, bestFrames = score, frames
			}
			if best >= threshold {
				passed = nudge
				break
			}
		}
		done = append(done, placeScore{at: seg.start, score: best})
		w.check = &CheckScore{Score: best, Min: threshold, Passed: best >= threshold} // the score that decided it (the job's check step)
		if best < threshold {                                                         // one place that is too different is enough
			ke := kept(pictureTooDifferent+" (similarity %.3f at %s, %.2f is required)",
				best, rules.Clock(seg.start), threshold).(*KeptError)
			ke.Detail = ssimDetail(src.DurationSec, segs, done, bestFrames, sMain.FrameRate, threshold)
			return ke
		}
		if passed != nudges[0] {
			order := []float64{passed}
			for _, n := range nudges {
				if n != passed {
					order = append(order, n)
				}
			}
			nudges = order
		}
	}
	return nil
}

// placeScore is one place's picture similarity as it was measured.
type placeScore struct{ at, score float64 }

// frameScore is one frame's picture similarity; n counts from 1 within the compared stretch.
type frameScore struct {
	n     int
	score float64
}

// ssimFrameRe reads ffmpeg's per-frame stats lines, e.g. "n:12 Y:0.991 U:0.998 V:0.997 All:0.993 (21.5)".
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

// ssimDetail is the job log's account of a picture refusal, in plain words: where the comparison ran, the
// score at each place measured so far, and the worst frames of the failing place (fps 0: frame numbers only).
func ssimDetail(duration float64, segs []segment, done []placeScore, frames []frameScore, fps, threshold float64) []string {
	var lines []string
	if duration > 0 && duration < 30 {
		lines = append(lines, "Check: picture comparison (SSIM) of the whole file")
	} else {
		var at []string
		for _, s := range segs {
			at = append(at, rules.Clock(s.start))
		}
		lines = append(lines, fmt.Sprintf("Check: picture comparison (SSIM) at %d places, %d s each: %s",
			len(segs), int(segs[0].length), strings.Join(at, ", ")))
	}
	var measured []string
	for _, p := range done {
		measured = append(measured, fmt.Sprintf("%s %.3f", rules.Clock(p.at), p.score))
	}
	scores := strings.Join(measured, ", ") + fmt.Sprintf(" (%.2f required)", threshold)
	if rest := segs[len(done):]; len(rest) > 0 {
		var na []string
		for _, s := range rest {
			na = append(na, rules.Clock(s.start)+" not measured")
		}
		scores += "; " + strings.Join(na, ", ")
	}
	lines = append(lines, "Scores: "+scores,
		"Alignment: the new file was also tried one frame earlier and later; the best score counts")
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
				where = rules.Clock(at + float64(f.n-1)/fps)
			}
			shown = append(shown, fmt.Sprintf("%s (%.3f)", where, f.score))
		}
		lines = append(lines, "Worst frames: "+strings.Join(shown, ", "))
	}
	return lines
}

type segment struct{ start, length float64 }

// sampleSegments picks where to compare: the whole of a short file, otherwise ten seconds at the start,
// the middle and the end.
func sampleSegments(duration float64) []segment {
	switch {
	case duration <= 0:
		return []segment{{0, 10}}
	case duration < 30:
		return []segment{{0, duration}}
	}
	return []segment{{0, 10}, {duration/2 - 5, 10}, {duration - 15, 10}}
}

// seekMargin is how far before a sample both files are sought. Where a seek lands is not exact: in
// MPEG-TS, ffmpeg 7.1 starts the H.264 original at the next keyframe after the seek point (up to ten
// seconds later with x264's defaults) but the HEVC result exactly at it. So the comparison never
// trusts where a seek landed: it decodes from a little earlier, keeps the real timestamps, and cuts
// the same stretch out of both by time. A keyframe gap longer than the margin only shortens the
// stretch that is compared; it cannot pair different pictures. Wherever a seek lands earlier than
// from, the trim still covers the whole sample, because the input is not cut short by a -t.
const seekMargin = 12.0

// scaleTo is the filter that brings the original's picture to the new file's size when a flow scaled it
// down (selectors' resolution), so the comparison pairs pictures of one size: "" when they are equal.
func scaleTo(src, out *media.Stream) string {
	if src == nil || out == nil || out.Width <= 0 || out.Height <= 0 || (src.Width == out.Width && src.Height == out.Height) {
		return ""
	}
	return fmt.Sprintf(",scale=%d:%d", out.Width, out.Height)
}

// ssimSegment compares seg (in seconds from the start of the video) of the original's video stream
// sv and the new file's video stream ov. srcStart and outStart are the times at which each file's
// video starts; subtracting them puts both on one timeline, where the ssim filter pairs frames by
// time.
func (w *Worker) ssimSegment(ctx context.Context, srcPath, tmp string, sv, ov int, seg segment, srcStart, outStart float64, scale string) (float64, []frameScore, error) {
	from := max(seg.start-seekMargin, 0)
	cut := func(in, stream int, start float64, extra, label string) string {
		// a start time can be negative (MP4 edit lists), so it is not formatted with secs
		return fmt.Sprintf("[%d:v:%d]setpts=PTS-(%s)/TB,trim=start=%s:duration=%s%s[%s]",
			in, stream, strconv.FormatFloat(start, 'f', 6, 64), secs(seg.start), secs(seg.length), extra, label)
	}
	// Reading from the start needs no seek, and must not have one: in an AVI, "-ss 0" jumps to a
	// later keyframe (measured: 8.4 s in the original, 1.4 s in the new file), so a good file's
	// start sample scored 0.878-0.934 and was refused.
	// No input -t: it counts from where the seek lands, which can be ten seconds before from, and would
	// cut the sample short. The trim filters end the stretch, and ffmpeg stops reading once they are done.
	input := func(path string) []string {
		a := []string{"-noaccurate_seek"}
		if from > 0 {
			a = append(a, "-ss", secs(from))
		}
		return append(a, "-i", path)
	}
	args := []string{"-hide_banner", "-nostdin", "-v", "info", "-nostats", "-copyts"}
	args = append(args, input(srcPath)...)
	args = append(args, input(tmp)...)
	args = append(args,
		"-lavfi", cut(0, sv, srcStart, scale, "a")+";"+cut(1, ov, outStart, "", "b")+";[a][b]ssim=stats_file=-",
		"-f", "null", "-")
	sctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()
	stdout, stderr, err := w.Exec.Capture(sctx, w.FFmpeg, args...)
	if err != nil {
		return 0, nil, checkFailed(sctx, fmt.Sprintf("the picture comparison at %s", rules.Clock(seg.start)), err)
	}
	m := ssimRe.FindSubmatch(stderr)
	if m == nil {
		return 0, nil, kept("the picture comparison at %s gave no result", rules.Clock(seg.start))
	}
	score, perr := strconv.ParseFloat(string(m[1]), 64)
	if perr != nil {
		return 0, nil, kept("the picture comparison at %s gave an unreadable result", rules.Clock(seg.start))
	}
	return score, parseFrameScores(stdout), nil
}

// hasFilter asks ffmpeg once whether it has a filter.
func (w *Worker) hasFilter(ctx context.Context, name string) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if v, ok := w.filterMemo[name]; ok {
		return v, nil
	}
	fctx, cancel := context.WithTimeout(ctx, quickTimeout)
	defer cancel()
	stdout, _, err := w.Exec.Capture(fctx, w.FFmpeg, "-hide_banner", "-filters")
	if err != nil {
		return false, err
	}
	re := regexp.MustCompile(`(?m)^\s*\S+\s+` + regexp.QuoteMeta(name) + `\s`)
	has := re.Match(stdout)
	if w.filterMemo == nil {
		w.filterMemo = map[string]bool{}
	}
	w.filterMemo[name] = has
	return has, nil
}

func secs(f float64) string {
	if f < 0 {
		f = 0
	}
	return strconv.FormatFloat(f, 'f', 3, 64)
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = append(lines[:n], "...")
	}
	return strings.Join(lines, " | ")
}

// ---- damage in the ORIGINAL, seen while it was being converted ----

// benignRe matches ffmpeg messages that appear on perfectly usable files (a broadcast recording that
// starts in the middle of a group of pictures, for instance) and so must not count as damage.
var benignRe = regexp.MustCompile(`(?i)last message repeated|non-existing pps|non-existing sps|no frame!|decode_slice_header error|mmco: unref short failure|increasing reorder buffer|timestamps are unset|^x265 \[`)

// plural picks the word for a count: plural(1, "picture", "pictures") is "picture".
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// maxDamagedPercent is the share of the original's pictures that may be damaged and still be converted: a file
// that is 99% good plays through, so it converts. One broken picture of a few hundred is a little damage (a bad
// macroblock in one P-frame, as in a copied file); more than that means the source is failing.
const maxDamagedPercent = 1

// sourceFrameCount is the main video stream's picture count as ffmpeg outputs it: the stream's own duration times
// its frame rate, else the count the stream states. The duration comes first because an mp4 edit list (a trimmed
// start) keeps the trimmed frames in the stream's count but not in its duration, and ffmpeg outputs only the
// duration's frames. exact is false when only the container's duration was there: audio or subtitles may run past
// the video, so that count can be too high and must not decide anything on its own.
func sourceFrameCount(src media.Info) (frames int64, exact bool) {
	if k := src.MainVideoIndex(); k >= 0 {
		s := src.Streams[k]
		switch {
		case s.Duration > 0:
			return int64(s.Duration*s.FrameRate + 0.5), true
		case s.Frames > 0:
			return s.Frames, true
		}
		return int64(src.DurationSec*s.FrameRate + 0.5), false
	}
	return 0, false
}

// sourceFrames is the main video stream's picture count (0 when unknown); see sourceFrameCount.
func sourceFrames(src media.Info) int64 {
	n, _ := sourceFrameCount(src)
	return n
}

// expectedPackets is the main video frame count the converted file must match. counted is the original's packet
// count, which for an mp4 edit list includes the trimmed frames ffmpeg never outputs; when the presented count is
// exact, that is the number to expect.
func expectedPackets(src media.Info, counted int64) int64 {
	if n, exact := sourceFrameCount(src); exact && n > 0 {
		return n
	}
	return counted
}

// damageTolerated says whether an original's damage is little enough to convert: no damage to the file as a
// whole (that is never tolerated, however few pictures), and the damaged pictures are at most maxDamagedPercent
// of its frames. A file whose frame count is not known is not tolerated: there is no share to measure.
func damageTolerated(rep sourceDamageReport, frames int64) bool {
	if rep.FileLevel || frames <= 0 {
		return false
	}
	return int64(rep.Pictures)*100 <= frames*maxDamagedPercent
}

// withLostFrames counts frames that never came out of the decoder as damaged pictures. A stretch can lose frames
// with no decoder message (the 300000-byte wreck in the real tests: 470 of 500 decoded, no error line). decoded is
// the frame count the conversion reported; -1 means it did not say (a remote worker), and then nothing is added.
func withLostFrames(rep sourceDamageReport, frames, decoded int64) sourceDamageReport {
	if frames <= 0 || decoded < 0 || decoded >= frames {
		return rep
	}
	lost := frames - decoded
	// The pictures ffmpeg names and the frames it drops are usually the same ones (the QA wreck had 54 named vs 53
	// lost), so the larger count is the damage, not their sum.
	if int(lost) > rep.Pictures {
		rep.Pictures = int(lost)
	}
	line := fmt.Sprintf("%d of %d frames were not decoded, and ffmpeg printed no message for them", lost, frames)
	if len(rep.Lines) > 0 {
		line = fmt.Sprintf("%d of %d frames were not decoded", lost, frames)
	}
	rep.Lines = append(rep.Lines, line)
	return rep
}

// damagedShare says how much damage there is, for the job log: the pictures out of the frames, and the share.
func damagedShare(pictures int, frames int64) string {
	if frames <= 0 {
		return fmt.Sprintf("%d damaged %s; the frame count is not known", pictures, plural(pictures, "picture", "pictures"))
	}
	return fmt.Sprintf("%d damaged %s of %d frames (%.2f%%)", pictures, plural(pictures, "picture", "pictures"), frames, 100*float64(pictures)/float64(frames))
}

// fileDamageRe matches ffmpeg messages that mean the file as a whole is damaged (its container or header is
// broken, or it ends early). Any of these keeps the original, however few. "damaged" is counted here too: when
// the message does not say which picture, the file as a whole is the safe reading. "corrupt" is not: ffmpeg's
// corrupt lines are per picture or packet (corrupted macroblock, corrupt decoded frame, Packet corrupt).
var fileDamageRe = regexp.MustCompile(`(?i)moov atom not found|invalid data found when processing input|truncated|unexpected end|file ended prematurely|header missing|damaged`)

// pictureDamageRe matches ffmpeg messages about one damaged picture (a broken slice or macroblock run).
var pictureDamageRe = regexp.MustCompile(`(?i)error while decoding|cabac decode|concealing|invalid nal|slice end not reached|overread|out of range|missing reference|reference picture missing|no frame decoded|corrupted macroblock|corrupt decoded frame|packet corrupt`)

// brokenSliceRe matches the decoder's line for one broken slice. A picture's burst prints other lines too
// (cabac decode, concealing), but they belong to the same picture, so only this line is counted.
var brokenSliceRe = regexp.MustCompile(`(?i)error while decoding`)

// sourceDamageReport is what sourceDamage found in the original.
type sourceDamageReport struct {
	Pictures  int      // damaged pictures: one per broken-slice line, or per other picture line when there is none
	FileLevel bool     // the file as a whole is damaged
	Lines     []string // the first few damage lines (at most maxDamageLinesShown), for the job log
}

// maxDamageLinesShown is how many of ffmpeg's damage lines the job log quotes for a refused original.
const maxDamageLinesShown = 5

// Found says whether anything in the original looked damaged.
func (d sourceDamageReport) Found() bool { return d.Pictures > 0 || d.FileLevel }

// sourceDamage looks at what ffmpeg printed (at the error level) while converting and reports the damage that
// shows the original file itself is broken. The encode decodes the original's video, so decode errors in it show
// up here at no extra cost. Counting: a broken picture usually prints a few lines (cabac decode of ..., error while
// decoding MB ..., concealing ...); one "error while decoding" line per picture is what is counted, so the burst
// counts as one. The count is a proxy for damage that clusters over a visible stretch: ffmpeg gives no times at
// the error level, so a run of damaged pictures cannot be measured in seconds here.
func sourceDamage(stderr string) sourceDamageReport {
	var rep sourceDamageReport
	var hits []string
	slices, pictureLines := 0, 0
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || benignRe.MatchString(line) {
			continue
		}
		switch {
		case fileDamageRe.MatchString(line):
			rep.FileLevel = true
		case pictureDamageRe.MatchString(line):
			pictureLines++
			if brokenSliceRe.MatchString(line) {
				slices++
			}
		default:
			continue
		}
		hits = append(hits, line)
	}
	rep.Pictures = slices
	if slices == 0 {
		rep.Pictures = pictureLines
	}
	if len(hits) > maxDamageLinesShown {
		hits = hits[:maxDamageLinesShown]
	}
	rep.Lines = hits
	return rep
}
