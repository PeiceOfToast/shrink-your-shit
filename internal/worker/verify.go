// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
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
	if errors.Is(cctx.Err(), context.DeadlineExceeded) {
		return kept("%s timed out; the original is kept (ffmpeg may be stuck or the file very slow to read)", what)
	}
	return kept("%s failed (%v)", what, err)
}

// KeptError means a safety check refused the new file (or could not run): the original is kept.
type KeptError struct{ Reason string }

func (e *KeptError) Error() string { return "kept original: " + e.Reason }

func kept(format string, a ...any) error { return &KeptError{Reason: fmt.Sprintf(format, a...)} }

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

// preflight refuses to start when the volume cannot hold the new file next to the original: about the
// original's size plus a margin (the new file is normally smaller, but not guaranteed to be).
func (w *Worker) preflight(sourcePath string, sourceSize int64) error {
	dir := filepath.Dir(sourcePath)
	free, err := w.freeSpace(dir)
	if err != nil {
		return kept("could not check the free disk space in %s (%v)", dir, err)
	}
	need := uint64(sourceSize) + uint64(sourceSize)/10 + 64<<20
	if free < need {
		return kept("not enough free disk space in %s: about %s is needed to convert this file safely, %s is free",
			dir, humanBytes(int64(need)), humanBytes(int64(free)))
	}
	return nil
}

// verify is the safety floor. It decides whether the new file at tmp may replace the original, by
// comparing it with exp (what it must look like), never with the original directly. Every failure, and
// every check that cannot run, is a KeptError: there is no "replace anyway". Cheap checks first, the
// expensive ones (playback test, picture comparison) only for files that already look right.
func (w *Worker) verify(ctx context.Context, src media.Info, exp Expect, srcPath, tmp string, origSize int64) error {
	st, err := os.Stat(tmp)
	if err != nil {
		return kept("the new file is missing (%v)", err)
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
		return kept("the new file cannot be read (%v)", err)
	}
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
	if limit := float64(origSize) * (1 - exp.MinSavings); float64(st.Size()) >= limit {
		if exp.MinSavings > 0 {
			return kept("the new file (%s) saves less than the required %.0f%% of the original (%s)",
				humanBytes(st.Size()), exp.MinSavings*100, humanBytes(origSize))
		}
		return kept("the new file (%s) is not smaller than the original (%s)", humanBytes(st.Size()), humanBytes(origSize))
	}
	if err := w.decodeCheck(ctx, out, tmp); err != nil {
		return err
	}
	return w.ssimCheck(ctx, src, out, srcPath, tmp)
}

// ---- playback test ----

// decodeCheck plays the new file through ffmpeg's decoder to nowhere: any error means a damaged file.
// There is no way to turn it off: the floor is not removable (only "always" vs the default "auto").
func (w *Worker) decodeCheck(ctx context.Context, out media.Info, tmp string) error {
	limit := w.FullDecodeMaxSec
	if limit <= 0 {
		limit = DefaultFullDecodeMaxSec
	}
	ord := out.MainVideoOrdinal()
	if w.DecodeCheck == "always" || out.DurationSec <= 0 || out.DurationSec <= limit {
		return w.decodeRun(ctx, tmp, ord, 0, 0, out.DurationSec)
	}
	// Long file: three samples of a minute each, at the start, middle and end.
	for _, start := range []float64{0, out.DurationSec/2 - 30, out.DurationSec - 65} {
		if err := w.decodeRun(ctx, tmp, ord, start, 60, 60); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) decodeRun(ctx context.Context, tmp string, videoOrd int, start, length, playSec float64) error {
	args := []string{"-v", "error", "-xerror", "-nostdin"}
	if length > 0 {
		args = append(args, "-ss", secs(start))
	}
	args = append(args, "-i", tmp)
	if length > 0 {
		args = append(args, "-t", secs(length))
	}
	// Only the video is played: it is the part this program re-encoded. The audio and subtitles are
	// copied bit for bit from the original, so any oddity in them was already in the original.
	args = append(args, "-map", fmt.Sprintf("0:v:%d", videoOrd), "-f", "null", "-")
	timeout := sampleTimeout
	if length == 0 {
		timeout = fullDecodeTimeout(playSec)
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, stderr, err := w.Exec.Capture(dctx, w.FFmpeg, args...)
	text := strings.TrimSpace(string(stderr))
	if errors.Is(dctx.Err(), context.DeadlineExceeded) {
		return checkFailed(dctx, "the playback test of the new file", err)
	}
	if err != nil || text != "" {
		if text == "" {
			text = err.Error()
		}
		return kept("the new file does not play cleanly: %s", firstLines(text, 3))
	}
	return nil
}

// ---- picture quality ----

var ssimRe = regexp.MustCompile(`SSIM\b.*\bAll:([0-9.]+)`)

// ssimCheck compares the picture of the original and the new file at up to three places, using
// ffmpeg's built-in SSIM measure (1.0 = identical). It needs ffmpeg's ssim filter; without it the
// check cannot run, and then the original is kept.
func (w *Worker) ssimCheck(ctx context.Context, src, out media.Info, srcPath, tmp string) error {
	sv, ov := src.MainVideoOrdinal(), out.MainVideoOrdinal()
	if sv < 0 || ov < 0 {
		return kept("there is no video to compare")
	}
	has, err := w.hasFilter(ctx, "ssim")
	if err != nil {
		return kept("could not ask ffmpeg whether it can compare picture quality (%v)", err)
	}
	if !has {
		return kept("this ffmpeg has no 'ssim' filter, which shrink-your-shit needs to check picture quality; install a complete ffmpeg build")
	}
	threshold := w.minSSIM()
	// Both pictures are put on one timeline by their own video start time (the encoder can delay the
	// first frame: measured, a good encode scored 0.93 instead of 0.985 without this). If that still
	// scores low, the new file is tried one frame earlier and later too and the best counts: a
	// genuinely bad encode stays low at every alignment, a merely misaligned good one jumps up.
	sMain, oMain := src.Video(), out.Video()
	frame := 1.0 / 25
	if sMain.FrameRate > 0 {
		frame = 1 / sMain.FrameRate
	}
	for _, seg := range sampleSegments(src.DurationSec) {
		best := -1.0
		for _, nudge := range []float64{0, -frame, frame} {
			score, err := w.ssimSegment(ctx, srcPath, tmp, sv, ov, seg, sMain.StartTime, oMain.StartTime+nudge)
			if err != nil {
				return err
			}
			if score > best {
				best = score
			}
			if best >= threshold {
				break
			}
		}
		if best < threshold { // one place that is too different is enough
			return kept("the picture of the new file is too different from the original (similarity %.3f at %s, %.2f is required)",
				best, clock(seg.start), threshold)
		}
	}
	return nil
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
// stretch that is compared; it cannot pair different pictures.
const seekMargin = 12.0

// ssimSegment compares seg (in seconds from the start of the video) of the original's video stream
// sv and the new file's video stream ov. srcStart and outStart are the times at which each file's
// video starts; subtracting them puts both on one timeline, where the ssim filter pairs frames by
// time.
func (w *Worker) ssimSegment(ctx context.Context, srcPath, tmp string, sv, ov int, seg segment, srcStart, outStart float64) (float64, error) {
	from := max(seg.start-seekMargin, 0)
	read := secs(seg.start + seg.length - from + 1)
	cut := func(in, stream int, start float64, label string) string {
		// a start time can be negative (MP4 edit lists), so it is not formatted with secs
		return fmt.Sprintf("[%d:v:%d]setpts=PTS-(%s)/TB,trim=start=%s:duration=%s[%s]",
			in, stream, strconv.FormatFloat(start, 'f', 6, 64), secs(seg.start), secs(seg.length), label)
	}
	// Reading from the start needs no seek, and must not have one: in an AVI, "-ss 0" jumps to a
	// later keyframe (measured: 8.4 s in the original, 1.4 s in the new file), so a good file's
	// start sample scored 0.878-0.934 and was refused.
	input := func(path string) []string {
		a := []string{"-noaccurate_seek"}
		if from > 0 {
			a = append(a, "-ss", secs(from))
		}
		return append(a, "-t", read, "-i", path)
	}
	args := []string{"-hide_banner", "-nostdin", "-v", "info", "-nostats", "-copyts"}
	args = append(args, input(srcPath)...)
	args = append(args, input(tmp)...)
	args = append(args,
		"-lavfi", cut(0, sv, srcStart, "a")+";"+cut(1, ov, outStart, "b")+";[a][b]ssim",
		"-f", "null", "-")
	sctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()
	_, stderr, err := w.Exec.Capture(sctx, w.FFmpeg, args...)
	if err != nil {
		return 0, checkFailed(sctx, fmt.Sprintf("the picture comparison at %s", clock(seg.start)), err)
	}
	m := ssimRe.FindSubmatch(stderr)
	if m == nil {
		return 0, kept("the picture comparison at %s gave no result", clock(seg.start))
	}
	score, perr := strconv.ParseFloat(string(m[1]), 64)
	if perr != nil {
		return 0, kept("the picture comparison at %s gave an unreadable result", clock(seg.start))
	}
	return score, nil
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

// damageRe matches ffmpeg messages that mean the coded data itself is broken.
var damageRe = regexp.MustCompile(`(?i)error while decoding|invalid nal|concealing|corrupt|invalid data found|overread|cabac decode|slice end not reached|damaged|truncated|unexpected end|moov atom not found|header missing|out of range|missing reference|reference picture missing`)

// sourceDamage looks at what ffmpeg printed (at the error level) while converting and returns the first
// few lines that show the original file itself is damaged, or "" if there are none. The encode decodes
// the original's video, so decode errors in it show up here at no extra cost.
func sourceDamage(stderr string) string {
	var hits []string
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || benignRe.MatchString(line) {
			continue
		}
		if damageRe.MatchString(line) {
			hits = append(hits, line)
		}
	}
	if len(hits) == 0 {
		return ""
	}
	return firstLines(strings.Join(hits, "\n"), 3)
}
