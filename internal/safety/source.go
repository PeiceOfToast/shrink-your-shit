// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// maxDamagedPercent is the share of the original's pictures that may be damaged and still be
// converted (L-SAFE-4): a file that is 99 % good plays through, so it converts. One broken picture
// of a few hundred is a little damage (a bad macroblock in one frame); more than that means the
// source is failing, and converting it would write the damage into a new file and blame the encoder.
const maxDamagedPercent = 1

// maxDamageLinesShown is how many of ffmpeg's damage lines the job log quotes for a refused original.
const maxDamageLinesShown = 5

// SourceCheck probes and decodes the original before it is converted (L-SAFE-4). Damage above the
// budget, lost frames or a file-level error keeps it, with its reason: the file is never converted,
// and the damage is never blamed on the encoder. Light damage within the budget converts.
//
// A file with no video stream passes: there is no picture to count, and the probe has already
// refused a container it cannot read.
func SourceCheck(ctx context.Context, c media.Capturer, rec Recorder, path string, src media.Info, timeout time.Duration) (Verdict, error) {
	mainOrdinal := src.MainVideoOrdinal()
	if mainOrdinal < 0 {
		return rec.log("original", Verdict{OK: true}), nil
	}
	main := src.MainVideo()
	if timeout <= 0 {
		timeout = fullDecodeTimeout(src.Format.Duration)
	}
	args := []string{"ffmpeg", "-loglevel", "level+verbose", "-nostats", "-nostdin", "-copyts", "-i", path,
		"-map", fmt.Sprintf("0:v:%d", mainOrdinal), "-map", "0:a?", "-f", "null", "-"}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// No -xerror: the decoder must play on past a damaged picture so the damage can be counted.
	res, err := c.Capture(dctx, args)
	if ctx.Err() != nil {
		// The person pressed Stop: the run was cut short by us, so it says nothing about the file.
		return Verdict{}, fmt.Errorf("source check: %w", ctx.Err())
	}
	if errors.Is(dctx.Err(), context.DeadlineExceeded) {
		return Verdict{}, fmt.Errorf("source check: %w", err)
	}
	if err != nil && res.Result.PID == 0 {
		return Verdict{}, fmt.Errorf("source check: %w", err)
	}

	// The error-level lines are the damage; the end-of-run report, printed only at the verbose
	// level, says how many packets were read and how many pictures came out of the decoder.
	rep := sourceDamage(errorLines(res.Stderr))
	frames, exact := sourceFrameCount(src)
	decoded := int64(-1)
	if st, ok := media.InputStreamStats(string(res.Stderr), 0, main.Index); ok {
		decoded = st.Frames
	}
	// Lost pictures are measured against the count the file itself promises. The stream's own
	// duration is the exact count. With none (an MKV usually has none), the container's length is
	// the only count there is: it includes any audio that runs past the picture, so the first
	// max(2 s, the 1 % damage budget) of the gap is that tail and only a longer gap is lost
	// pictures — a silently truncated file has exactly that shape and prints no decoder line
	// (review2 B1, then the re-review on #861).
	count := frames
	switch {
	case exact:
		rep = withLostFrames(rep, frames, decoded)
	case decoded >= 0 && picturesStopEarly(src, frames, decoded):
		rep = withLostFrames(rep, frames, decoded)
	case decoded >= 0:
		// The share of damage is measured against the pictures that exist: the decoder's own count
		// when the container's length promises no more than a tail's worth.
		count = decoded
	}
	if !rep.Found() || damageTolerated(rep, count) {
		return rec.log("original", Verdict{OK: true}), nil
	}
	v := Verdict{
		OK:         false,
		Code:       CodeOriginalDamaged,
		Message:    originalDamageMessage(rep, count),
		NextAction: NextKeepOriginal,
		Detail:     append([]string{"Check: decoding the original before converting it"}, rep.Lines...),
	}
	if decoded >= 0 {
		if exact && frames > 0 {
			v.Detail = append(v.Detail, fmt.Sprintf("Decoded: %d of %d frames", decoded, frames))
		} else {
			v.Detail = append(v.Detail, fmt.Sprintf("Decoded: %d frames", decoded))
		}
	}
	return rec.log("original", v), nil
}

// originalDamageMessage is the plain sentence for a damaged original: what is wrong and what
// happens to the file. It never mentions the encoder, because the encoder did nothing wrong.
func originalDamageMessage(rep sourceDamageReport, frames int64) string {
	if rep.FileLevel {
		return "The original file is damaged (its container or an end of the file is broken), so it is kept as it is and not converted."
	}
	pictures := rep.Pictures
	word := "pictures"
	if pictures == 1 {
		word = "picture"
	}
	if frames <= 0 {
		return fmt.Sprintf("The original file is damaged (%d damaged %s, and its frame count is not known), so it is kept as it is and not converted.", pictures, word)
	}
	return fmt.Sprintf("The original file is damaged (%d damaged %s of %d, %.2f%%), so it is kept as it is and not converted.",
		pictures, word, frames, 100*float64(pictures)/float64(frames))
}

// sourceFrameCount is the main video stream's picture count as ffmpeg outputs it: the stream's own
// duration times its frame rate, else the container's duration times it. The duration comes first
// because an MP4 edit list (a trimmed start) keeps the trimmed frames in the stream's count but not
// in its duration, and ffmpeg outputs only the duration's frames. exact is false when only the
// container's duration was there: audio or subtitles may run past the video, so that count can be
// too high.
func sourceFrameCount(src media.Info) (frames int64, exact bool) {
	main := src.MainVideo()
	if main == nil || main.FrameRate.Float() <= 0 {
		return 0, false
	}
	if main.Duration > 0 {
		return int64(main.Duration.Seconds()*main.FrameRate.Float() + 0.5), true
	}
	if src.Format.Duration > 0 {
		return int64(src.Format.Duration.Seconds()*main.FrameRate.Float() + 0.5), false
	}
	return 0, false
}

// damageTolerated says whether an original's damage is little enough to convert: no damage to the
// file as a whole (that is never tolerated, however few pictures), and the damaged pictures are at
// most maxDamagedPercent of its frames. A file whose frame count is not known is not tolerated:
// there is no share to measure.
func damageTolerated(rep sourceDamageReport, frames int64) bool {
	if rep.FileLevel || frames <= 0 {
		return false
	}
	return int64(rep.Pictures)*100 <= frames*maxDamagedPercent
}

// withLostFrames counts frames that never came out of the decoder as damaged pictures. A stretch
// can lose frames with no decoder message (470 of 500 decoded, no error line for the lost 30).
// frames is the count the loss is measured against: the video stream's own picture count
// (sourceFrameCount's exact answer) when it has one, else the container's count once the gap is
// past an audio tail (picturesStopEarly). decoded is the frame count the run reported; -1 means it
// did not say, and then nothing is added.
func withLostFrames(rep sourceDamageReport, frames, decoded int64) sourceDamageReport {
	if frames <= 0 || decoded < 0 || decoded >= frames {
		return rep
	}
	lost := frames - decoded
	// The pictures ffmpeg names and the frames it drops are usually the same ones, so the larger
	// count is the damage, not their sum.
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

// tailTolerance is how far past the decoded picture a container's own length may run before the
// difference is lost pictures rather than audio: max(2 s, the 1 % damage budget of that length)
// (re-review on #861). Two seconds covers an ordinary audio track that outlasts the picture; on a
// long file the budget the check already uses is the bigger allowance.
func tailTolerance(container time.Duration) time.Duration {
	t := container / 100
	if t < 2*time.Second {
		t = 2 * time.Second
	}
	return t
}

// picturesStopEarly is whether the decoder delivered fewer pictures than the container's length
// promises by more than an audio tail can explain. frames is that container count (sourceFrameCount
// with no stream duration) and decoded what the run reported; the frame rate turns the missing
// pictures into the seconds the tolerance is measured in.
func picturesStopEarly(src media.Info, frames, decoded int64) bool {
	main := src.MainVideo()
	if main == nil || main.FrameRate.Float() <= 0 || frames <= decoded {
		return false
	}
	gap := time.Duration(float64(frames-decoded) / main.FrameRate.Float() * float64(time.Second))
	return gap > tailTolerance(src.Format.Duration)
}

// benignRe matches ffmpeg messages that appear on perfectly usable files (a broadcast recording
// that starts in the middle of a group of pictures, for instance) and so must not count as damage.
var benignRe = regexp.MustCompile(`(?i)last message repeated|non-existing pps|non-existing sps|no frame!|decode_slice_header error|mmco: unref short failure|increasing reorder buffer|timestamps are unset|^x265 \[`)

// fileDamageRe matches ffmpeg messages that mean the file as a whole is damaged (its container or
// header is broken, or it ends early). Any of these keeps the original, however few. "damaged" is
// counted here too: when the message does not say which picture, the file as a whole is the safe
// reading. "corrupt" is not: ffmpeg's corrupt lines are per picture or packet.
var fileDamageRe = regexp.MustCompile(`(?i)moov atom not found|invalid data found when processing input|truncated|unexpected end|file ended prematurely|header missing|damaged`)

// pictureDamageRe matches ffmpeg messages about one damaged picture (a broken slice or macroblock
// run).
var pictureDamageRe = regexp.MustCompile(`(?i)error while decoding|cabac decode|concealing|invalid nal|slice end not reached|overread|out of range|missing reference|reference picture missing|no frame decoded|corrupted macroblock|corrupt decoded frame|packet corrupt`)

// brokenSliceRe matches the decoder's line for one broken slice. A picture's burst prints other
// lines too (cabac decode, concealing), but they belong to the same picture, so only this line is
// counted.
var brokenSliceRe = regexp.MustCompile(`(?i)error while decoding`)

// sourceDamageReport is what sourceDamage found in the original.
type sourceDamageReport struct {
	Pictures  int      // damaged pictures: one per broken-slice line, or per other picture line when there is none
	FileLevel bool     // the file as a whole is damaged
	Lines     []string // the first few damage lines (at most maxDamageLinesShown)
}

// Found says whether anything in the original looked damaged.
func (d sourceDamageReport) Found() bool { return d.Pictures > 0 || d.FileLevel }

// sourceDamage looks at what ffmpeg printed (at the error level) while decoding the original and
// reports the damage that shows the file itself is broken. Counting: a broken picture usually
// prints a few lines (cabac decode of ..., error while decoding MB ..., concealing ...); one "error
// while decoding" line per picture is what is counted, so the burst counts as one.
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
