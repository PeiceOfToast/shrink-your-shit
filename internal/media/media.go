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
}

// OSExec is the real Exec.
type OSExec struct{}

func (OSExec) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return out, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(ee.Stderr)))
		}
		return out, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
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
}

// Probe runs ffprobe on path.
func Probe(ctx context.Context, x Exec, ffprobe, path string) (Info, error) {
	out, err := x.Output(ctx, ffprobe, "-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", path)
	if err != nil {
		return Info{}, err
	}
	return ParseProbe(out)
}

// ParseProbe turns ffprobe JSON into Info. It errors if there is no video stream.
func ParseProbe(data []byte) (Info, error) {
	var p struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			BitRate   string `json:"bit_rate"`
		} `json:"streams"`
		Format struct {
			FormatName string `json:"format_name"`
			Duration   string `json:"duration"`
			Size       string `json:"size"`
			BitRate    string `json:"bit_rate"`
		} `json:"format"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return Info{}, fmt.Errorf("parse ffprobe output: %w", err)
	}
	info := Info{Container: p.Format.FormatName}
	found := false
	streamRate := 0
	for _, s := range p.Streams {
		if s.CodecType == "video" {
			info.VideoCodec = s.CodecName
			streamRate, _ = strconv.Atoi(s.BitRate)
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
