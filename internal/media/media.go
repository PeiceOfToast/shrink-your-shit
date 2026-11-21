// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package media wraps ffprobe and ffmpeg. All process execution goes through
// the Exec interface so tests can fake it without real ffmpeg installed.
package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Exec runs external programs.
type Exec interface {
	// Output runs a program and returns its stdout.
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
	// Stream runs a program, calls onLine for each stdout line, and returns the first part of its
	// stderr (ffmpeg reports decode errors there, even when it exits 0). err is non-nil if it
	// exited non-zero or could not start.
	Stream(ctx context.Context, onLine func(string), name string, args ...string) (stderr string, err error)
	// Capture runs a program and returns its stdout and (the first part of) its stderr, which is
	// where ffmpeg reports decode errors and filter results. err is non-nil if it exited non-zero;
	// the output is returned either way.
	Capture(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

// OSExec is the real Exec.
type OSExec struct{}

func (OSExec) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := command(ctx, name, args...)
	var out limitedBuffer
	var stderr tail
	var errText strings.Builder
	stderr.b, stderr.max = &errText, 2000
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		if out.over {
			return nil, fmt.Errorf("%s: output is larger than %d bytes", name, maxProbeBytes)
		}
		return out.buf, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(errText.String()))
	}
	return out.buf, nil
}

// limitedBuffer collects output but refuses more than maxProbeBytes (the writer error stops the
// program), so a runaway ffprobe cannot use unbounded memory.
type limitedBuffer struct {
	buf  []byte
	over bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if len(l.buf)+len(p) > maxProbeBytes {
		l.over = true
		return 0, fmt.Errorf("output too large")
	}
	l.buf = append(l.buf, p...)
	return len(p), nil
}

func (OSExec) Capture(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := command(ctx, name, args...)
	var out limitedBuffer
	errBuf := headBuffer{max: maxStderrBytes}
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	if out.over {
		return nil, errBuf.buf, fmt.Errorf("%s: output is larger than %d bytes", name, maxProbeBytes)
	}
	if err != nil {
		err = fmt.Errorf("%s: %w", name, err)
	}
	return out.buf, errBuf.buf, err
}

// maxStderrBytes bounds what Capture keeps of a program's error output (a failing decode can print
// an error for every frame; the first part is all anyone reads).
const maxStderrBytes = 256 << 10

// headBuffer keeps the first max bytes written and silently drops the rest (the program keeps running).
type headBuffer struct {
	buf []byte
	max int
}

func (h *headBuffer) Write(p []byte) (int, error) {
	if room := h.max - len(h.buf); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		h.buf = append(h.buf, p[:room]...)
	}
	return len(p), nil
}

// maxReportBytes bounds what Stream keeps of ffmpeg's end-of-run input stream report lines, apart from
// the error lines: they come last, so a noisy run must not crowd them out of the error lines' budget.
const maxReportBytes = 64 << 10

// keepBuffer is what Stream keeps of a program's stderr: its error lines, its unmarked lines (a run at
// -v error prints the whole message with no level marker), and ffmpeg's end-of-run "Input stream #..."
// report, which is at [verbose] level. Everything else (the info lines a level+verbose run starts with)
// is dropped as it arrives, so however much a run prints, the errors and the report are still there
// (review-588 round 1: a noisy or damaged file's report was being cut off after 256 KiB).
type keepBuffer struct {
	lines, reports, part []byte
	max, maxReports      int
	over                 bool // this line is longer than can be kept: drop its rest
}

func (k *keepBuffer) Write(p []byte) (int, error) {
	n := len(p)
	for {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			if !k.over {
				k.part = append(k.part, p...)
			}
			break
		}
		if !k.over {
			k.part = append(k.part, p[:i]...)
		}
		k.line(k.part)
		k.part, k.over = k.part[:0], false
		p = p[i+1:]
	}
	if len(k.part) > k.max {
		k.part, k.over = k.part[:k.max], true // the line's start is what says what it is
	}
	return n, nil
}

// line keeps l if it is a report line, an error line, or a line with no level marker at all.
func (k *keepBuffer) line(l []byte) {
	switch {
	case bytes.Contains(l, []byte("Input stream #")):
		k.reports = appendCapped(k.reports, l, k.maxReports)
	case keepStderrLine(string(l)):
		k.lines = appendCapped(k.lines, l, k.max)
	}
}

func (k *keepBuffer) flush() {
	if len(k.part) > 0 {
		k.line(k.part)
		k.part = nil
	}
}

func keepStderrLine(s string) bool {
	m := logLevelRe.FindStringSubmatchIndex(s)
	if m == nil {
		return true // no marker: a run at -v error, or a program that does not prefix its levels
	}
	switch s[m[2]:m[3]] {
	case "panic", "fatal", "error":
		return true
	}
	return false
}

func appendCapped(buf, l []byte, max int) []byte {
	if room := max - len(buf); room > 1 { // one byte of it is the line end
		if len(l) > room-1 {
			l = l[:room-1]
		}
		buf = append(append(buf, l...), '\n')
	}
	return buf
}

func (OSExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) (string, error) {
	cmd := command(ctx, name, args...)
	// lines go through a writer, not a read loop: Wait (with WaitDelay) then ends a stopped run even when
	// something it started still holds its output open (QA finding B)
	lines := &lineWriter{onLine: onLine}
	var last strings.Builder
	keep := &keepBuffer{max: maxStderrBytes, maxReports: maxReportBytes}
	cmd.Stdout, cmd.Stderr = lines, io.MultiWriter(&tail{b: &last, max: 2000}, keep)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	err := cmd.Wait()
	lines.flush()
	keep.flush()
	stderr := string(keep.lines) + string(keep.reports)
	if err != nil {
		return stderr, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(last.String()))
	}
	return stderr, nil
}

// StopWaitDelay is how long a stopped program's output may stay open, held by something it started,
// before the run is ended anyway (QA finding B: a converter's leftover child held the job in Running).
var StopWaitDelay = 5 * time.Second

// command is name with args, ended with ctx: killed with everything it started (its own process group,
// where the system has them), it dies with this program, and its run ends at most StopWaitDelay after
// the kill even if a leftover still holds its output.
func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	dieWithParent(cmd)
	killGroup(cmd)
	cmd.WaitDelay = StopWaitDelay
	return cmd
}

// lineWriter calls onLine for each line written to it, without its line end (as bufio.ScanLines).
type lineWriter struct {
	buf    []byte
	onLine func(string)
}

func (l *lineWriter) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		l.onLine(strings.TrimSuffix(string(l.buf[:i]), "\r"))
		l.buf = l.buf[i+1:]
	}
}

// flush gives the last line, if it had no line end.
func (l *lineWriter) flush() {
	if len(l.buf) > 0 {
		l.onLine(strings.TrimSuffix(string(l.buf), "\r"))
		l.buf = nil
	}
}

// tail keeps only the last max bytes written (ffmpeg's error is at the end).
type tail struct {
	b   *strings.Builder
	max int
}

func (t *tail) Write(p []byte) (int, error) {
	t.b.Write(p)
	if t.b.Len() > t.max*2 {
		s := t.b.String()
		t.b.Reset()
		t.b.WriteString(s[len(s)-t.max:])
	}
	return len(p), nil
}

var _ io.Writer = (*tail)(nil)

// Info is what we need to know about a video file.
type Info struct {
	// Probe is the ProbeVersion it was taken with: a cached probe from an older version is taken again.
	Probe     int    `json:"probe,omitempty"`
	Container string `json:"container"`
	// Ext is the file's extension, lower case (".webm"): ffprobe names WebM and MKV alike.
	Ext         string  `json:"ext,omitempty"`
	VideoCodec  string  `json:"videoCodec"`
	BitrateKbps int     `json:"bitrateKbps"`
	DurationSec float64 `json:"durationSec"`
	SizeBytes   int64   `json:"sizeBytes"`
	// Streams lists every stream in file order; Chapters is the chapter count.
	Streams  []Stream `json:"streams,omitempty"`
	Chapters int      `json:"chapters,omitempty"`
}

// ProbeVersion is what Probe reads; raise it when it reads more, so cached probes are taken again.
// 2 (S3): the first frame's HDR side data, the Dolby Vision profile and tag, mastering display and CLL.
// 3 (review 7ab8f72): the Dolby Vision compatibility id (DVCompat).
// 4 (the S3/#81 merge): the Display Matrix rotation too (probes cached by an S3 build lack it).
// 5 (#450): the hearing_impaired disposition, so SDH .srt files are named .sdh on upgraded installs too.
// 6 (R5): the headline codec and bitrate are the main picture's, not a cover image's.
// 7 (R3): every stream's own duration (audio too), which the A/V length check reads; cached probes lack audio durations.
// 8 (slice 13): VFR is marked when the average and nominal rates differ by more than 1% (3% before).
const ProbeVersion = 8

// Probe runs ffprobe on path. For a video that may carry HDR or Dolby Vision (HEVC, AV1, or PQ/HLG) it
// also reads the main video's first frame: in TS files and raw streams, the Dolby Vision RPU, the
// mastering display and the light levels are only there.
func Probe(ctx context.Context, x Exec, ffprobe, path string) (Info, error) {
	out, err := x.Output(ctx, ffprobe, "-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", "-show_chapters", path)
	if err != nil {
		return Info{}, err
	}
	info, err := ParseProbe(out)
	info.Ext = strings.ToLower(filepath.Ext(path))
	if err != nil {
		return info, err
	}
	if v := info.Video(); v != nil && (v.Codec == "hevc" || v.Codec == "av1" || v.ColorTransfer == "smpte2084" || v.ColorTransfer == "arib-std-b67") {
		fr, err := x.Output(ctx, ffprobe, "-v", "error", "-print_format", "json", "-select_streams",
			fmt.Sprintf("v:%d", info.MainVideoOrdinal()), "-read_intervals", "%+#1", "-show_frames", path)
		if err != nil {
			return Info{}, fmt.Errorf("reading the first frame: %w", err) // a missed Dolby Vision would convert to wrong colours
		}
		info.AddFirstFrame(fr)
	}
	return info, nil
}

// AddFirstFrame adds the HDR side data of the main video's first frame (ffprobe -show_frames JSON) to
// its stream. Output without frames changes nothing.
func (i *Info) AddFirstFrame(data []byte) {
	var f struct {
		Frames []map[string]any `json:"frames"`
	}
	if json.Unmarshal(data, &f) != nil || len(f.Frames) == 0 {
		return
	}
	for k := range i.Streams {
		if &i.Streams[k] == i.Video() {
			i.Streams[k].addSideData(f.Frames[0])
		}
	}
}

// maxProbeBytes bounds how much ffprobe output is accepted (a file with thousands of chapters or
// streams can produce a lot; a few MB is already far more than any real video needs).
const maxProbeBytes = 8 << 20

// CountVideoPackets counts the packets (one per frame for video) of the n-th video stream by reading
// the file's container without decoding it. It is how a truncated or frame-dropping output is caught.
func CountVideoPackets(ctx context.Context, x Exec, ffprobe, path string, n int) (int64, error) {
	out, err := x.Output(ctx, ffprobe, "-v", "error", "-select_streams", fmt.Sprintf("v:%d", n),
		"-count_packets", "-show_entries", "stream=nb_read_packets", "-of", "csv=p=0", path)
	if err != nil {
		return 0, err
	}
	// The first field of the first line: ffprobe 7.1 prints "75," for a stream with side data (HDR, S3).
	first, _, _ := strings.Cut(strings.SplitN(string(out), "\n", 2)[0], ",")
	c, perr := strconv.ParseInt(strings.TrimSpace(first), 10, 64)
	if perr != nil || c < 0 {
		return 0, fmt.Errorf("could not read the frame count of %s", path)
	}
	return c, nil
}

// maxLeadingPackets bounds how far LeadingUndecodable reads: a stream's first keyframe comes within one group of pictures.
const maxLeadingPackets = 1000

// LeadingUndecodable counts the packets of the n-th video stream that the decoder did not show: they come before
// the stream's first keyframe and their presentation time is earlier than the first picture the decoder outputs.
// A leading picture that decodes (an open group of pictures) is shown, so it is not credited. Any probe error or
// missing time gives no credit: the strict count stands.
func LeadingUndecodable(ctx context.Context, x Exec, ffprobe, path string, n int) (int64, error) {
	sel := fmt.Sprintf("v:%d", n)
	pk, err := x.Output(ctx, ffprobe, "-v", "error", "-select_streams", sel,
		"-read_intervals", fmt.Sprintf("%%+#%d", maxLeadingPackets), "-show_entries", "packet=flags,pts_time", "-of", "csv=p=0", path)
	if err != nil {
		return 0, err
	}
	// The first picture the decoder outputs: the undecodable leading pictures print nothing, so the window is
	// by time, long enough to reach the first keyframe's picture in any ordinary group of pictures.
	fr, err := x.Output(ctx, ffprobe, "-v", "error", "-select_streams", sel, "-show_frames",
		"-read_intervals", "%+10", "-show_entries", "frame=pts_time", "-of", "csv=p=0", path)
	if err != nil {
		return 0, err
	}
	first, ok := csvTime(strings.SplitN(string(fr), "\n", 2)[0])
	if !ok { // no picture was output: nothing can be credited
		return 0, nil
	}
	var lead int64
	for _, line := range strings.Split(string(pk), "\n") {
		t, flags, ok := packetTimeFlags(line)
		if !ok {
			continue
		}
		if strings.HasPrefix(flags, "K") { // flags: K marks a keyframe
			return lead, nil
		}
		if t < first {
			lead++
		}
	}
	return 0, nil
}

// packetTimeFlags reads one packet=flags,pts_time line. ffprobe's field order is not the order asked for, so
// tell the two fields apart by content: the time parses as a number, the flags do not (they are K, _ and similar).
func packetTimeFlags(line string) (t float64, flags string, ok bool) {
	a, b, _ := strings.Cut(strings.TrimSpace(line), ",")
	if t, ok := csvTime(a); ok {
		return t, b, true
	}
	if t, ok := csvTime(b); ok {
		return t, a, true
	}
	return 0, "", false
}

// UndecodableLead is how much of the n-th video stream's start the decoder cannot show (seconds): a recording
// that begins in the middle of a group of pictures has nothing before its first keyframe, and those packets are
// in no timeline any player has. start is the stream's own start; 0 is returned when the stream starts on a
// keyframe, when the first picture is at or before start, or when nothing can be read.
func UndecodableLead(ctx context.Context, x Exec, ffprobe, path string, n int, start float64) (float64, error) {
	sel := fmt.Sprintf("v:%d", n)
	// A stream that starts on a keyframe decodes from its first packet, so there is no lead and nothing to
	// decode; a stream that starts later pays for the decode below.
	one, err := x.Output(ctx, ffprobe, "-v", "error", "-select_streams", sel,
		"-read_intervals", "%+#1", "-show_entries", "packet=flags,pts_time", "-of", "csv=p=0", path)
	if err != nil {
		return 0, err
	}
	if _, flags, ok := packetTimeFlags(string(one)); ok && strings.HasPrefix(flags, "K") {
		return 0, nil
	}
	// The first picture the decoder outputs: the undecodable leading pictures print nothing, so the window is
	// by time, long enough to reach the first keyframe's picture in any ordinary group of pictures.
	fr, err := x.Output(ctx, ffprobe, "-v", "error", "-select_streams", sel, "-show_frames",
		"-read_intervals", "%+10", "-show_entries", "frame=pts_time", "-of", "csv=p=0", path)
	if err != nil {
		return 0, err
	}
	if first, ok := csvTime(strings.SplitN(string(fr), "\n", 2)[0]); ok && first > start {
		return first - start, nil
	}
	return 0, nil
}

// timingPackets bounds how many packets FramesFollowNominalRate reads: the share is a sample, and a few
// thousand gaps carry it.
const timingPackets = 4000

// FramesFollowNominalRate says whether the n-th video stream's frames are spaced at its declared (nominal)
// frame interval: more than two thirds of the gaps between consecutive frames are within 10% of 1/nominal.
//
// It tells the two streams whose report reads exactly what it decodes yet comes up short of duration × rate
// apart (review-588 round 3). A demuxer that silently skipped packets leaves gaps where packets are missing
// and keeps the interval between the frames that remain, so the share stays high: those frames are lost. A
// variable-rate stream's declared rate is not its real one, so most of its frames sit elsewhere: nothing is
// lost, its count for the encoding is simply shorter. Steadiness is also true when there is too little to
// judge (probe error, fewer than two frames, no nominal rate): the caller counts the shortfall, and a false
// refusal is cheaper than a lost original.
func FramesFollowNominalRate(ctx context.Context, x Exec, ffprobe, path string, n int, nominal float64) (bool, error) {
	if nominal <= 0 {
		return true, nil
	}
	out, err := x.Output(ctx, ffprobe, "-v", "error", "-select_streams", fmt.Sprintf("v:%d", n),
		"-read_intervals", fmt.Sprintf("%%+#%d", timingPackets), "-show_entries", "packet=pts_time", "-of", "csv=p=0", path)
	if err != nil {
		return false, err
	}
	ts := make([]float64, 0, 1024)
	for _, line := range strings.Split(string(out), "\n") {
		if t, ok := csvTime(line); ok {
			ts = append(ts, t)
		}
	}
	if len(ts) < 2 {
		return true, nil
	}
	sort.Float64s(ts)
	step, at := 1/nominal, 0
	for i := 1; i < len(ts); i++ {
		if d := ts[i] - ts[i-1]; d > 0 && math.Abs(d-step) <= 0.1*step {
			at++
		}
	}
	return at*3 >= (len(ts)-1)*2, nil
}

// csvTime reads one pts_time field (seconds, a trailing comma allowed); N/A or junk is not a time.
func csvTime(s string) (float64, bool) {
	t, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), ",")), 64)
	return t, err == nil
}

// ParseProbe turns ffprobe JSON into Info. It errors if there is no video stream. Missing or odd
// fields are tolerated (they stay zero); garbage input returns an error and never panics.
func ParseProbe(data []byte) (Info, error) {
	if len(data) > maxProbeBytes {
		return Info{}, fmt.Errorf("ffprobe output is too large (%d bytes)", len(data))
	}
	var p struct {
		Streams  []map[string]any `json:"streams"`
		Chapters []any            `json:"chapters"`
		Format   struct {
			FormatName string `json:"format_name"`
			Duration   string `json:"duration"`
			Size       string `json:"size"`
			BitRate    string `json:"bit_rate"`
		} `json:"format"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return Info{}, fmt.Errorf("parse ffprobe output: %w", err)
	}
	info := Info{Probe: ProbeVersion, Container: p.Format.FormatName, Chapters: len(p.Chapters)}
	for i, raw := range p.Streams {
		info.Streams = append(info.Streams, parseStream(raw, i))
	}
	// The headline codec and bitrate are the main picture's, not cover art's (R5): a cover image is a
	// video stream with attached_pic, and judging a file by it said "mjpeg / no bitrate". A file whose
	// only video stream is a cover keeps the first video stream's reading: there is no main picture.
	main := info.MainVideoIndex()
	if main < 0 {
		for i, st := range info.Streams {
			if st.Type == "video" {
				main = i
				break
			}
		}
	}
	if main < 0 {
		return Info{}, fmt.Errorf("no video stream")
	}
	info.VideoCodec = info.Streams[main].Codec
	streamRate := int(num(p.Streams[main], "bit_rate"))
	info.DurationSec, _ = strconv.ParseFloat(p.Format.Duration, 64)
	info.SizeBytes, _ = strconv.ParseInt(p.Format.Size, 10, 64)
	// Prefer the video stream's own bitrate; fall back to the whole file's.
	rate := streamRate
	if rate == 0 {
		rate, _ = strconv.Atoi(p.Format.BitRate)
	}
	info.BitrateKbps = rate / 1000
	return info, nil
}

// ParseProgress reads one line of ffmpeg -progress output. If the line reports
// the encoded position, it returns the percent done (0-100) and true.
func ParseProgress(line string, durationSec float64) (float64, bool) {
	us, ok := strings.CutPrefix(line, "out_time_us=")
	if !ok || durationSec <= 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(us, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	pct := v / 1e6 / durationSec * 100
	if pct > 100 {
		pct = 100
	}
	return pct, true
}

// Progress is where a running ffmpeg is, from its -progress report (S5's Files view): the percent of
// the source's length done, and the latest fps, bitrate and speed (0 when ffmpeg says N/A or has not
// said yet). ETASeconds is the time left at that speed, -1 while unknown.
type Progress struct {
	OutTimeUS   float64 // where ffmpeg is in the source (out_time_us); it can freeze while frames and bytes go on
	Frame       int64   // frames ffmpeg has taken through its decoder and filters (frame=); -1 until it says
	Percent     float64
	FPS         float64
	BitrateKbps float64
	Speed       float64
	ETASeconds  float64
	TotalSize   int64 // bytes of the new file written so far (total_size=); 0 when ffmpeg says N/A
}

// Transcode runs ffmpeg and reports each progress point through onProgress (on every out_time_us line,
// with the block's other values as last seen; with durationSec 0, a length we don't know, the position
// only, so a stall can still be told from a slow encode). It returns what ffmpeg printed on stderr (at the error
// level the args ask for), which includes decode errors in the source.
func Transcode(ctx context.Context, x Exec, ffmpeg string, args []string, durationSec float64, onProgress func(Progress)) (string, error) {
	return TranscodeLines(ctx, x, ffmpeg, args, durationSec, onProgress, nil)
}

// TranscodeLines is Transcode with onLine called for every line ffmpeg prints, progress line or not (the
// job log's quiet warning counts them). onLine may be nil.
func TranscodeLines(ctx context.Context, x Exec, ffmpeg string, args []string, durationSec float64, onProgress func(Progress), onLine func()) (string, error) {
	p := Progress{ETASeconds: -1, Frame: -1}
	var etas []float64 // the last 5 raw times left: the raw one jumps with every change of speed (research 3.4)
	return x.Stream(ctx, func(line string) {
		if onLine != nil {
			onLine()
		}
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "frame":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				p.Frame = n
			}
		case "fps":
			p.FPS, _ = strconv.ParseFloat(v, 64)
		case "bitrate": // "4074.8kbits/s", or "N/A"
			p.BitrateKbps, _ = strconv.ParseFloat(strings.TrimSuffix(v, "kbits/s"), 64)
		case "speed": // "1.25x", or "N/A"
			p.Speed, _ = strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(v, "x")), 64)
		case "total_size": // "123456", or "N/A": the bytes of the new file ffmpeg has written so far
			p.TotalSize, _ = strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		}
		if k != "out_time_us" {
			return
		}
		us, err := strconv.ParseFloat(v, 64)
		if err != nil || us < 0 { // "N/A" before the first frame
			return
		}
		p.OutTimeUS = us
		if pct, ok := ParseProgress(line, durationSec); ok {
			p.Percent, p.ETASeconds = pct, -1
			if p.Speed > 0 {
				etas = append(etas, math.Max(0, durationSec*(1-pct/100)/p.Speed))
				if len(etas) > 5 {
					etas = etas[1:]
				}
				var sum float64
				for _, e := range etas {
					sum += e
				}
				p.ETASeconds = sum / float64(len(etas))
			}
		}
		onProgress(p) // a length we don't know: the position only (Percent 0, ETASeconds -1)
	}, ffmpeg, args...)
}

// Encoders is the set of encoders ffmpeg lists (ffmpeg -encoders), e.g. libx264; empty if it can't say.
func Encoders(ctx context.Context, x Exec, ffmpeg string) map[string]bool {
	out, _ := x.Output(ctx, ffmpeg, "-hide_banner", "-encoders")
	set := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && len(f[0]) == 6 && (f[0][0] == 'V' || f[0][0] == 'A' || f[0][0] == 'S') && f[1] != "=" { // not the legend
			set[f[1]] = true
		}
	}
	return set
}
