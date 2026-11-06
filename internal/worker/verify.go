// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

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
	out, err := media.Probe(ctx, w.Exec, w.FFprobe, tmp)
	if err != nil {
		return kept("the new file cannot be read (%v)", err)
	}
	outPackets := int64(-1)
	if exp.VideoPackets > 0 {
		ord := out.MainVideoOrdinal()
		if ord < 0 {
			return kept("the new file has no video")
		}
		if outPackets, err = media.CountVideoPackets(ctx, w.Exec, w.FFprobe, tmp, ord); err != nil {
			return kept("could not count the new file's video frames (%v)", err)
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

// decodeCheck plays the new file through ffmpeg's decoder to /dev/null: any error means a damaged file.
func (w *Worker) decodeCheck(ctx context.Context, out media.Info, tmp string) error {
	mode := w.DecodeCheck
	if mode == "" {
		mode = "auto"
	}
	if mode == "off" {
		return nil
	}
	limit := w.FullDecodeMaxSec
	if limit <= 0 {
		limit = DefaultFullDecodeMaxSec
	}
	ord := out.MainVideoOrdinal()
	if mode == "always" || out.DurationSec <= 0 || out.DurationSec <= limit {
		return w.decodeRun(ctx, tmp, ord, 0, 0)
	}
	// Long file: three samples of a minute each, at the start, middle and end.
	for _, start := range []float64{0, out.DurationSec/2 - 30, out.DurationSec - 65} {
		if err := w.decodeRun(ctx, tmp, ord, start, 60); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) decodeRun(ctx context.Context, tmp string, videoOrd int, start, length float64) error {
	args := []string{"-v", "error", "-xerror", "-nostdin"}
	if length > 0 {
		args = append(args, "-ss", secs(start))
	}
	args = append(args, "-i", tmp)
	if length > 0 {
		args = append(args, "-t", secs(length))
	}
	args = append(args, "-map", fmt.Sprintf("0:v:%d", videoOrd), "-map", "0:a?", "-f", "null", "-")
	_, stderr, err := w.Exec.Capture(ctx, w.FFmpeg, args...)
	text := strings.TrimSpace(string(stderr))
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
	worst, worstAt := 2.0, 0.0
	for _, seg := range sampleSegments(src.DurationSec) {
		score, err := w.ssimSegment(ctx, srcPath, tmp, sv, ov, seg)
		if err != nil {
			return err
		}
		if score < worst {
			worst, worstAt = score, seg.start
		}
	}
	if worst < threshold {
		return kept("the picture of the new file is too different from the original (similarity %.3f at %s, %.2f is required)",
			worst, clock(worstAt), threshold)
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

func (w *Worker) ssimSegment(ctx context.Context, srcPath, tmp string, sv, ov int, seg segment) (float64, error) {
	args := []string{"-hide_banner", "-nostdin", "-v", "info", "-nostats",
		"-ss", secs(seg.start), "-t", secs(seg.length), "-i", srcPath,
		"-ss", secs(seg.start), "-t", secs(seg.length), "-i", tmp,
		// Each picture starts at time zero before comparing: the encoder can delay the first frame
		// (measured: 0.04s), which would otherwise pair every frame with its neighbour and score a
		// perfectly good file at 0.89.
		"-lavfi", fmt.Sprintf("[0:v:%d]setpts=PTS-STARTPTS[a];[1:v:%d]setpts=PTS-STARTPTS[b];[a][b]ssim", sv, ov),
		"-f", "null", "-"}
	_, stderr, err := w.Exec.Capture(ctx, w.FFmpeg, args...)
	if err != nil {
		return 0, kept("the picture comparison failed at %s (%v)", clock(seg.start), err)
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
	stdout, _, err := w.Exec.Capture(ctx, w.FFmpeg, "-hide_banner", "-filters")
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
