// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package media wraps ffprobe and ffmpeg. All process execution goes through
// the Exec interface so tests can fake it without real ffmpeg installed.
package media

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// Exec runs external programs.
type Exec interface {
	// Output runs a program and returns its stdout.
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
	// Stream runs a program and calls onLine for each stdout line.
	Stream(ctx context.Context, onLine func(string), name string, args ...string) error
	// Capture runs a program and returns its stdout and (the first part of) its stderr, which is
	// where ffmpeg reports decode errors and filter results. err is non-nil if it exited non-zero;
	// the output is returned either way.
	Capture(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

// OSExec is the real Exec.
type OSExec struct{}

func (OSExec) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
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
	cmd := exec.CommandContext(ctx, name, args...)
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

func (OSExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &tail{b: &stderr, max: 2000}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		onLine(sc.Text())
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return nil
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
	Container   string  `json:"container"`
	VideoCodec  string  `json:"videoCodec"`
	BitrateKbps int     `json:"bitrateKbps"`
	DurationSec float64 `json:"durationSec"`
	SizeBytes   int64   `json:"sizeBytes"`
	// Streams lists every stream in file order; Chapters is the chapter count.
	Streams  []Stream `json:"streams,omitempty"`
	Chapters int      `json:"chapters,omitempty"`
}

// Probe runs ffprobe on path.
func Probe(ctx context.Context, x Exec, ffprobe, path string) (Info, error) {
	out, err := x.Output(ctx, ffprobe, "-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", "-show_chapters", path)
	if err != nil {
		return Info{}, err
	}
	return ParseProbe(out)
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
	c, perr := strconv.ParseInt(strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]), 10, 64)
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
	info := Info{Container: p.Format.FormatName, Chapters: len(p.Chapters)}
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

// HEVCArgs builds the ffmpeg arguments to convert in to out: the first video
// stream becomes HEVC, everything else (audio, subtitles, attachments, extra
// streams) is copied. Output uses the input's container, so copies always fit.
// Progress is written to stdout as key=value lines.
func HEVCArgs(in, out string, crf int, preset string) []string {
	return []string{
		"-y", "-nostdin", "-v", "error", "-progress", "pipe:1", "-nostats",
		"-i", in,
		"-map", "0", "-map", "-0:d",
		"-c", "copy",
		"-c:v:0", "libx265", "-crf", strconv.Itoa(crf), "-preset", preset,
		"-tag:v:0", "hvc1",
		out,
	}
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

// Transcode runs ffmpeg and reports progress percent through onProgress.
func Transcode(ctx context.Context, x Exec, ffmpeg string, args []string, durationSec float64, onProgress func(float64)) error {
	return x.Stream(ctx, func(line string) {
		if pct, ok := ParseProgress(line, durationSec); ok {
			onProgress(pct)
		}
	}, ffmpeg, args...)
}
