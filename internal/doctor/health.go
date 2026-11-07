// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package doctor

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Check is one line of the health report (GET /api/system/health, `shrink-your-shit doctor`).
type Check struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"` // ok | warn | bad
	Detail string `json:"detail"`
}

// Health is the whole report.
type Health struct {
	Checks   []Check  `json:"checks"`
	Version  string   `json:"version"`
	LogsTail []string `json:"logsTail"`
}

// Bad reports whether any check failed.
func (h Health) Bad() bool {
	for _, c := range h.Checks {
		if c.Status == "bad" {
			return true
		}
	}
	return false
}

// Library is what the health report needs of a library.
type Library struct {
	ID         int64
	Name, Path string
}

// Health checks the converter, the data folder and every library, with the same checks as Folder.
func (c *Checker) Health(ctx context.Context, dataDir string, libs []Library) []Check {
	t := c.Tools(ctx)
	var out []Check
	add := func(id, label string, ok bool, good, bad string) {
		s, d := "ok", good
		if !ok {
			s, d = "bad", bad
		}
		out = append(out, Check{id, label, s, d})
	}
	add("ffmpeg", "Converter (ffmpeg)", t.FFmpeg != "", "Version "+t.FFmpegVersion+" at "+t.FFmpeg,
		"Not found. Install ffmpeg (Linux: sudo apt install ffmpeg; macOS: brew install ffmpeg; Windows: run the installer again), then restart shrink-your-shit.")
	add("ffprobe", "File reader (ffprobe)", t.FFprobe != "", "Version "+t.FFprobeVersion+" at "+t.FFprobe,
		"Not found. It comes with ffmpeg: install ffmpeg, then restart shrink-your-shit.")
	add("x265", "HEVC encoder (libx265)", t.X265, "Available",
		"This ffmpeg cannot make HEVC files (no libx265). Install a complete ffmpeg build, then restart shrink-your-shit.")
	add("ssim", "Picture check (ssim)", t.SSIM, "Available",
		"This ffmpeg has no 'ssim' filter, which the safety check needs. Install a complete ffmpeg build, then restart shrink-your-shit.")
	hw := "Not found. That is fine: converting uses the processor."
	if len(t.Hardware) > 0 {
		hw = "Found " + strings.Join(t.Hardware, ", ") + ". Not used yet: converting uses the processor."
	} else if len(t.HardwareListed) > 0 {
		hw = "This ffmpeg lists " + strings.Join(t.HardwareListed, ", ") + ", but none worked on this machine. That is fine: converting uses the processor."
	}
	out = append(out, Check{"hardware", "Graphics-card encoder", "ok", hw})

	if err := writeProbe(dataDir); err != nil {
		out = append(out, Check{"data", "Data folder", "bad", "Cannot write to " + dataDir + ". " + ReadOnlyMessage(dataDir)})
	} else {
		out = append(out, Check{"data", "Data folder", "ok", dataDir})
	}

	if len(libs) == 0 {
		out = append(out, Check{"libraries", "Libraries", "warn", "No libraries yet. Add one on the Libraries page."})
	}
	for _, l := range libs {
		r := c.Folder(ctx, l.Path)
		ch := Check{ID: fmt.Sprintf("library:%d", l.ID), Label: "Library: " + l.Name, Status: "ok",
			Detail: fmt.Sprintf("%s: %s, %s", l.Path, plural(r.VideoCount, "video", "videos"), gbInt(r.TotalBytes))}
		var said []string
		for _, b := range r.Blockers {
			if b.Code != "no_converter" { // reported once above
				said = append(said, b.Message)
			}
		}
		if len(said) > 0 {
			ch.Status, ch.Detail = "bad", l.Path+": "+strings.Join(said, " ")
		} else {
			for _, n := range r.Notes {
				if n.Code == "hardlinks" {
					ch.Status, ch.Detail = "warn", ch.Detail+". "+n.Message
				}
			}
		}
		out = append(out, ch)
	}
	return out
}

func gbInt(n int64) string {
	if n < 0 {
		n = 0
	}
	return gb(uint64(n))
}

// LogTail keeps the last lines the program logged, for the health report. Use it as a log writer.
type LogTail struct {
	mu    sync.Mutex
	lines []string
	part  string
}

const tailLines = 50

func (t *LogTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.part += string(p)
	for {
		i := strings.IndexByte(t.part, '\n')
		if i < 0 {
			break
		}
		t.lines = append(t.lines, t.part[:i])
		t.part = t.part[i+1:]
	}
	if len(t.lines) > tailLines {
		t.lines = append([]string(nil), t.lines[len(t.lines)-tailLines:]...)
	}
	return len(p), nil
}

// Lines returns a copy of the kept lines, oldest first.
func (t *LogTail) Lines() []string {
	if t == nil {
		return []string{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string{}, t.lines...)
}
