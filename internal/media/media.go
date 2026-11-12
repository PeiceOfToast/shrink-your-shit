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

func (OSExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) (string, error) {
	cmd := command(ctx, name, args...)
	// lines go through a writer, not a read loop: Wait (with WaitDelay) then ends a stopped run even when
	// something it started still holds its output open (QA finding B)
	lines := &lineWriter{onLine: onLine}
	var last strings.Builder
	head := headBuffer{max: maxStderrBytes}
	cmd.Stdout, cmd.Stderr = lines, io.MultiWriter(&tail{b: &last, max: 2000}, &head)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	err := cmd.Wait()
	lines.flush()
	if err != nil {
		return string(head.buf), fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(last.String()))
	}
	return string(head.buf), nil
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
const ProbeVersion = 4

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
	found := false
	streamRate := 0
	// The headline codec/bitrate come from the first video stream in the file, exactly as before
	// the per-stream model existed (so nothing that is converted today changes).
	for i, st := range info.Streams {
		if st.Type == "video" {
			info.VideoCodec = st.Codec
			streamRate = int(num(p.Streams[i], "bit_rate"))
			found = true
			break
		}
	}
	if !found {
		return Info{}, fmt.Errorf("no video stream")
	}
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
	Percent     float64
	FPS         float64
	BitrateKbps float64
	Speed       float64
	ETASeconds  float64
}

// Transcode runs ffmpeg and reports each progress point through onProgress (on every out_time_us line,
// with the block's other values as last seen). It returns what ffmpeg printed on stderr (at the error
// level the args ask for), which includes decode errors in the source.
func Transcode(ctx context.Context, x Exec, ffmpeg string, args []string, durationSec float64, onProgress func(Progress)) (string, error) {
	p := Progress{ETASeconds: -1}
	var etas []float64 // the last 5 raw times left: the raw one jumps with every change of speed (research 3.4)
	return x.Stream(ctx, func(line string) {
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "fps":
			p.FPS, _ = strconv.ParseFloat(v, 64)
		case "bitrate": // "4074.8kbits/s", or "N/A"
			p.BitrateKbps, _ = strconv.ParseFloat(strings.TrimSuffix(v, "kbits/s"), 64)
		case "speed": // "1.25x", or "N/A"
			p.Speed, _ = strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(v, "x")), 64)
		}
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
			onProgress(p)
		}
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
