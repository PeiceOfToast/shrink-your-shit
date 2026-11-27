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
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// DefaultFullDecodeMaxSec is the longest result that is played back whole. A longer one is sampled
// (L-SAFE-2), so a film does not cost hours of decoding before it is known to play.
const DefaultFullDecodeMaxSec = 3 * time.Hour

// playbackEvery is how far apart a long file's playback samples start, in seconds. Damage anywhere
// is then within a minute of a sample.
const playbackEvery = 600.0

// The time limits for one check's ffmpeg call, so a hung child cannot hold the worker: it becomes a
// check that could not run, which the job retries (L-SAFE-1).
var (
	// sampleTimeout bounds one sampled playback or one picture comparison.
	sampleTimeout = 10 * time.Minute
	// fullDecodeBase and fullDecodeCap give a whole-file playback ten minutes plus twice the
	// file's length, and never more than six hours.
	fullDecodeBase, fullDecodeCap = 10 * time.Minute, 6 * time.Hour
)

func fullDecodeTimeout(d time.Duration) time.Duration {
	limit := fullDecodeBase + 2*d
	if limit > fullDecodeCap {
		limit = fullDecodeCap
	}
	return limit
}

// playbackCheck plays the result through ffmpeg's decoders to the null muxer (L-SAFE-2): a file
// that does not decode end to end is damaged, however good its streams look. A file longer than
// DefaultFullDecodeMaxSec is played a minute at a time, so damage anywhere is within reach of a
// sample. It returns (verdict, nil) for a "no" and (nil, err) for a check that could not run.
func playbackCheck(ctx context.Context, c media.Capturer, out media.Info, path string) (*Verdict, error) {
	ordinal := out.MainVideoOrdinal()
	if ordinal < 0 {
		// Check 3 already refused a result with no video; nothing is left to play.
		return nil, nil
	}
	var fps float64
	if v := out.MainVideo(); v != nil {
		fps = v.FrameRate.Float()
	}
	duration := out.Format.Duration.Seconds()
	if duration <= 0 || duration <= DefaultFullDecodeMaxSec.Seconds() {
		return decodeRun(ctx, c, path, out, ordinal, 0, 0, duration, fps)
	}
	for _, start := range playbackStarts(duration) {
		if v, err := decodeRun(ctx, c, path, out, ordinal, start, 60, 60, fps); v != nil || err != nil {
			return v, err
		}
	}
	return nil, nil
}

// playbackStarts are where a long result's one-minute playback samples start: one at 0, then every
// playbackEvery, and the last one so that it ends 5 s before the file does (L-SAFE-2). A sample is
// never cut short by the one after it, and none starts past the end. When the stretch left unplayed
// before the last sample is bigger than the rest, one sample goes in its middle.
func playbackStarts(duration float64) []float64 {
	last := duration - 65
	if last < 0 {
		last = 0
	}
	var starts []float64
	for s := 0.0; s+60 <= last; s += playbackEvery {
		starts = append(starts, s)
	}
	if n := len(starts); n > 0 {
		if gap := last - (starts[n-1] + 60); gap > playbackEvery-60 {
			starts = append(starts, starts[n-1]+60+(gap-60)/2)
		}
	}
	return append(starts, last)
}

// opusParserRe is the one message ffmpeg's demuxer prints for the last Opus packet of a good file,
// even when only the video is played (ffmpeg 8: any Opus audio in MKV/WebM). It says nothing about
// the file that is being checked.
var opusParserRe = regexp.MustCompile(`(?m)^\[opus @ [0-9a-fx]+\] Error parsing Opus packet header\.\s*$`)

// addrRe and numRe make two ffmpeg error lines the same when only their addresses and numbers differ.
var (
	addrRe = regexp.MustCompile(` @ 0x[0-9a-fA-F]+`)
	numRe  = regexp.MustCompile(`[0-9]+`)
)

// logLevelRe finds the level ffmpeg puts in front of each log line when asked for it
// (-loglevel level+verbose): "[error] " at the start, or after the log context, which can be more
// than one part ("[vist#0:0/h264 @ 0x...] [dec:h264 @ 0x...] [error] ...").
var logLevelRe = regexp.MustCompile(`^(?:\[[^\]]*\] )*\[(quiet|panic|fatal|error|warning|info|verbose|debug|trace)\] `)

// libvaChatterRe matches the libva driver's own start-up lines. They reach stderr without ffmpeg's
// level marker and say nothing about the file, so they are never damage (L-SAFE-2).
var libvaChatterRe = regexp.MustCompile(`(?i)^libva (info|warning):`)

// errorLines keeps only the error-level lines of a run logged with -loglevel level+verbose, with
// the level marker removed: the text then reads as -v error writes it, which is what the playback
// check judges. A line with no marker is kept as it is (a test runner answers with plain error
// text), except libva's chatter.
func errorLines(raw []byte) string {
	var kept []string
	for _, l := range strings.Split(string(raw), "\n") {
		if libvaChatterRe.MatchString(strings.TrimSpace(l)) {
			continue
		}
		m := logLevelRe.FindStringSubmatchIndex(l)
		if m == nil {
			kept = append(kept, l)
			continue
		}
		switch l[m[2]:m[3]] {
		case "panic", "fatal", "error":
			kept = append(kept, l[:m[2]-1]+l[m[1]:]) // drop the marker and its space
		}
	}
	return strings.Join(kept, "\n")
}

// decodeRun plays the file, or a sample of it, through the decoders and reports a "no" when it does
// not play cleanly. length 0 plays the whole file (playSec is its length, for the log).
func decodeRun(ctx context.Context, c media.Capturer, path string, out media.Info, ordinal int, start, length, playSec, fps float64) (*Verdict, error) {
	// level+verbose: the report that names a broken picture is printed at the error level, and the
	// end-of-run report at the verbose level; the markers let the two be told apart.
	args := []string{"ffmpeg", "-loglevel", "level+verbose", "-nostats", "-xerror", "-nostdin", "-progress", "pipe:1"}
	// -ss and -t before -i are input options. -copyts makes them absolute timeline times and keeps
	// AAC's priming from being shifted into the video, where the null muxer would call it a dts error.
	if length > 0 {
		args = append(args, "-ss", secs(start), "-t", secs(length))
	}
	args = append(args,
		"-copyts", "-i", path,
		// The video and the audio are what this program wrote. Subtitles and attachments are copied
		// bit for bit from the original, so any oddity in them was already in the original.
		"-map", fmt.Sprintf("0:v:%d", ordinal), "-map", "0:a?",
		"-f", "null", "-")
	timeout := sampleTimeout
	if length == 0 {
		timeout = fullDecodeTimeout(out.Format.Duration)
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := c.Capture(dctx, args)
	if errors.Is(dctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("playback: %w", err)
	}
	if err != nil && res.Result.PID == 0 {
		// The run never started (ffmpeg missing, a pipe failure): a check that could not run.
		return nil, fmt.Errorf("playback: %w", err)
	}
	// The null muxer's dts warning on an audio stream is timing, not a decode error (R2); a video
	// one still counts.
	text, _ := dropAudioTiming(errorLines(res.Stderr))
	text = strings.TrimSpace(opusParserRe.ReplaceAllString(text, ""))
	if err != nil || text != "" {
		v := playbackKept(res.Stdout, text, err, out, start, length, playSec, fps)
		return &v, nil
	}
	// A sample that played no frames proves nothing: keep the original rather than pass it silently.
	if frame, ok := progressFrame(res.Stdout); length > 0 && (!ok || frame == 0) {
		v := playedNothingKept(frame, ok, start, length, fps)
		return &v, nil
	}
	return nil, nil
}

// playbackKept is the "no" for a result that does not play cleanly. The message is a plain sentence;
// the detail carries which check ran, how far playback got and ffmpeg's distinct error lines.
func playbackKept(progress []byte, text string, runErr error, out media.Info, start, length, playSec, fps float64) Verdict {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 && runErr != nil {
		lines = append(lines, runErr.Error())
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

	tracks, other := audioErrors(text, out)
	message := "The new file does not play cleanly."
	switch {
	case len(tracks) > 0 && !other:
		message = "The new file's " + trackWords(tracks) + " does not play cleanly."
	case len(lines) == 1:
		message = "The new file does not play cleanly: it reported 1 error when played back."
	case len(lines) > 1:
		message = fmt.Sprintf("The new file does not play cleanly: it reported %d errors when played back.", len(lines))
	}

	check := "Check: playback test of the whole new file (0:00:00 to " + clock(playSec) + ")"
	if length > 0 {
		check = fmt.Sprintf("Check: playback test, %d s sample from %s", int(length), clock(start))
	}
	// ffmpeg's out_time is 0 with the null muxer, so the position is the frame count over the rate.
	where := "Where: playback did not report how far it got"
	if frame, ok := progressFrame(progress); ok {
		where = fmt.Sprintf("Where: playback reached frame %d", frame)
		if fps > 0 {
			where += " (about " + clock(start+float64(frame)/fps) + ")"
		}
	}
	detail := []string{check, where, fmt.Sprintf("Errors: %d error lines from ffmpeg, %d different", len(lines), len(seen))}
	detail = append(detail, shown...)
	return Verdict{OK: false, Code: CodePlayback, Message: message, NextAction: NextKeepOriginal, Detail: detail}
}

// playedNothingKept is the "no" for a sampled playback that decoded no frames: the sample proves
// nothing, so the original is kept.
func playedNothingKept(frame int64, ok bool, start, length, fps float64) Verdict {
	where := "Where: playback did not report how far it got"
	if ok {
		where = fmt.Sprintf("Where: playback reached frame %d", frame)
		if fps > 0 {
			where += " (about " + clock(start+float64(frame)/fps) + ")"
		}
	}
	return Verdict{
		OK: false, Code: CodePlayback,
		Message:    "The playback test of the new file played nothing from its sample, so the original is kept.",
		NextAction: NextKeepOriginal,
		Detail:     []string{fmt.Sprintf("Check: playback test, %d s sample from %s", int(length), clock(start)), where},
	}
}

// progressFrame reads ffmpeg's -progress output: the last frame playback reached.
func progressFrame(out []byte) (int64, bool) {
	var frame int64
	found := false
	for _, l := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok || k != "frame" {
			continue
		}
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			frame, found = n, true
		}
	}
	return frame, found
}

// decoderRe is the name in front of an ffmpeg error line: "[aac @ 0x55d1c2a0] ...". ffmpeg 7 adds
// its own prefixes to it, as in "[aist#0:0/aac @ ...]" and "[dec:aac @ ...]"; the decoder is the
// part after the last "/" or ":".
var decoderRe = regexp.MustCompile(`^\[([^ \]]+) @ `)

func decoderName(line string) (string, bool) {
	m := decoderRe.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	name := m[1]
	if i := strings.LastIndexAny(name, "/:"); i >= 0 {
		name = name[i+1:]
	}
	return name, true
}

// audioErrors says which audio tracks the playback test's error lines are about. Each line names the
// decoder that reported it; a line is the audio track's when its decoder is that track's codec
// ("aac" for AAC, "mp3float" for MP3). Tracks are numbered from 1 among the file's audio tracks.
// other is true when some line is not clearly an audio track's (a video decoder's, or the muxer's).
func audioErrors(text string, out media.Info) (tracks []int, other bool) {
	seen := map[int]bool{}
	for _, l := range strings.Split(text, "\n") {
		name, ok := decoderName(l)
		if !ok {
			other = true
			continue
		}
		matched := false
		for i, a := range out.Audio {
			if sameDecoder(name, a.Codec) {
				matched = true
				if !seen[i+1] {
					seen[i+1] = true
					tracks = append(tracks, i+1)
				}
			}
		}
		if !matched {
			other = true
		}
	}
	sort.Ints(tracks)
	return tracks, other
}

// sameDecoder is whether an ffmpeg decoder name belongs to a codec: the same name, or a variant of
// it ("mp3float" for "mp3", "ac3_fixed" for "ac3").
func sameDecoder(decoder, codec string) bool {
	return codec != "" && (decoder == codec || strings.HasPrefix(decoder, codec))
}

// muxerDTSRe is the null muxer's timestamp warning, "Application provided invalid, non monotonically
// increasing dts to muxer in stream N". The playback check maps the video first and the audio after,
// so stream 0 is the video and any other stream is an audio track.
var muxerDTSRe = regexp.MustCompile(`non monotonically increasing dts to muxer in stream ([0-9]+)`)

var repeatedRe = regexp.MustCompile(`^Last message repeated [0-9]+ times$`)

// dropAudioTiming removes the muxer's dts warnings for audio streams, and the "Last message
// repeated" lines that follow them. They are timing in the muxer, not decode errors, so audio is
// judged by the decoder's errors alone (R2). Video warnings stay. It returns the text kept and how
// many warnings were dropped.
func dropAudioTiming(text string) (string, int) {
	var kept []string
	dropped := 0
	afterAudio := false
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		if m := muxerDTSRe.FindStringSubmatch(t); m != nil && m[1] != "0" {
			dropped++
			afterAudio = true
			continue
		}
		if afterAudio && repeatedRe.MatchString(t) {
			continue
		}
		afterAudio = false
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n"), dropped
}

// trackWords names audio tracks in plain words: "audio track 1", "audio tracks 1 and 2".
func trackWords(tracks []int) string {
	names := make([]string, len(tracks))
	for i, n := range tracks {
		names[i] = strconv.Itoa(n)
	}
	if len(names) == 1 {
		return "audio track " + names[0]
	}
	return "audio tracks " + strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// clock writes a position as H:MM:SS for a job-log line.
func clock(seconds float64) string {
	if seconds < 0 || seconds != seconds { // negative, or NaN
		seconds = 0
	}
	t := time.Duration(seconds * float64(time.Second))
	h := t / time.Hour
	t -= h * time.Hour
	m := t / time.Minute
	s := (t - m*time.Minute) / time.Second
	return fmt.Sprintf("%d:%02d:%02d", h, m, s)
}

// secs writes a time for an ffmpeg argument, with three decimals.
func secs(f float64) string {
	if f < 0 {
		f = 0
	}
	return strconv.FormatFloat(f, 'f', 3, 64)
}

// cutRunes shortens a line to n runes, for the detail.
func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
