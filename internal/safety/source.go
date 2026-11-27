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
	// A picture can only be called missing against the video stream's own count. The container's
	// duration includes any audio that runs past the video, so a pristine MKV would read as
	// damaged (review2 B1 on #861: 61 expected from a 2.023 s container, 60 decoded, no decode
	// error). Without an exact count the decode's own error lines still count normally.
	if exact {
		rep = withLostFrames(rep, frames, decoded)
	}

	// The share of damage is measured against the pictures that exist: the stream's own count when
	// it has one, else the number the decoder delivered. The container's duration includes an audio
	// tail, so it is only a last resort (review2 B1 on #861).
	count := frames
	if !exact && decoded >= 0 {
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
// frames must be the video stream's own picture count (sourceFrameCount's exact answer): only that
// count can prove a frame is missing. decoded is the frame count the run reported; -1 means it did
// not say, and then nothing is added.
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
