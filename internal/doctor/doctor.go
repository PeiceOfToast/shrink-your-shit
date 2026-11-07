// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package doctor runs the setup checks (FEATURE-PLAN FR-3): one set of checks, used by first-run's
// folder check (POST /api/libraries/check), Settings > System (GET /api/system/health) and the
// `shrink-your-shit doctor` command. Every message says in plain words what is wrong and what to do.
package doctor

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/scanner"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// Issue is one finding: a blocker (the folder cannot be used) or a note (worth knowing).
type Issue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// FolderReport is POST /api/libraries/check's answer (UI-API-CONTRACT.md line 21).
type FolderReport struct {
	OK         bool    `json:"ok"`
	Blockers   []Issue `json:"blockers"`
	Notes      []Issue `json:"notes"`
	VideoCount int     `json:"videoCount"`
	TotalBytes int64   `json:"totalBytes"`
}

// Tools is what was found of the converter.
type Tools struct {
	FFmpeg, FFprobe               string // paths; "" = not found
	FFmpegVersion, FFprobeVersion string
	X265, SSIM                    bool     // the HEVC encoder, the picture comparison filter
	Hardware                      []string // hardware HEVC encoders that encoded a test frame
	HardwareListed                []string // listed by ffmpeg but did not work here
}

// Converts reports whether conversions can run: ffmpeg, ffprobe, libx265 and the SSIM filter.
func (t Tools) Converts() bool { return t.FFmpeg != "" && t.FFprobe != "" && t.X265 && t.SSIM }

// Checker runs the checks. FFmpeg and FFprobe are the resolved paths ("" when not found).
type Checker struct {
	Exec            media.Exec
	FFmpeg, FFprobe string
	// FreeSpace reports the writable bytes on the volume holding dir; nil = the real thing.
	FreeSpace func(dir string) (uint64, error)

	mu      sync.Mutex
	tools   *Tools // the converter, without the hardware test
	toolsAt time.Time
	hw      *[2][]string // working and listed-but-failing hardware encoders
	hwAt    time.Time
}

const toolsTTL = 10 * time.Minute

// Tools checks the converter, hardware encoders included (cached for ten minutes: the hardware test
// encodes take a moment).
func (c *Checker) Tools(ctx context.Context) Tools {
	t := c.Converter(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hw == nil || time.Since(c.hwAt) >= toolsTTL {
		var hw [2][]string
		if t.FFmpeg != "" {
			if out, _ := c.Exec.Output(ctx, t.FFmpeg, "-hide_banner", "-encoders"); len(out) > 0 {
				for _, enc := range []string{"hevc_nvenc", "hevc_qsv", "hevc_vaapi", "hevc_videotoolbox", "hevc_amf", "hevc_mf", "hevc_v4l2m2m"} {
					if strings.Contains(string(out), " "+enc+" ") {
						if c.encodes(ctx, enc) {
							hw[0] = append(hw[0], enc)
						} else {
							hw[1] = append(hw[1], enc)
						}
					}
				}
			}
		}
		c.hw, c.hwAt = &hw, time.Now()
	}
	t.Hardware, t.HardwareListed = c.hw[0], c.hw[1]
	return t
}

// Converter checks ffmpeg, ffprobe, libx265 and the SSIM filter, without the hardware test: quick
// enough for startup. Cached for ten minutes.
func (c *Checker) Converter(ctx context.Context) Tools {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tools != nil && time.Since(c.toolsAt) < toolsTTL {
		return *c.tools
	}
	t := Tools{FFmpeg: c.FFmpeg, FFprobe: c.FFprobe}
	version := regexp.MustCompile(`version (\S+)`)
	if t.FFmpeg != "" {
		if out, err := c.Exec.Output(ctx, t.FFmpeg, "-hide_banner", "-version"); err == nil || len(out) > 0 {
			if m := version.FindSubmatch(out); m != nil {
				t.FFmpegVersion = string(m[1])
			}
		}
		if out, _ := c.Exec.Output(ctx, t.FFmpeg, "-hide_banner", "-encoders"); len(out) > 0 {
			t.X265 = strings.Contains(string(out), " libx265 ")
		}
		if out, _ := c.Exec.Output(ctx, t.FFmpeg, "-hide_banner", "-filters"); len(out) > 0 {
			t.SSIM = regexp.MustCompile(`(?m)^\s*\S+\s+ssim\s`).Match(out)
		}
	}
	if t.FFprobe != "" {
		if out, err := c.Exec.Output(ctx, t.FFprobe, "-hide_banner", "-version"); err == nil || len(out) > 0 {
			if m := version.FindSubmatch(out); m != nil {
				t.FFprobeVersion = string(m[1])
			}
		}
	}
	c.tools, c.toolsAt = &t, time.Now()
	return t
}

// encodes tries one tiny frame with a hardware encoder: listed in ffmpeg is not the same as usable here.
func (c *Checker) encodes(ctx context.Context, enc string) bool {
	tctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := c.Exec.Output(tctx, c.FFmpeg, "-hide_banner", "-v", "error", "-f", "lavfi", "-i", "color=c=black:s=256x256:d=0.1",
		"-frames:v", "1", "-c:v", enc, "-f", "null", "-")
	return err == nil
}

func (c *Checker) freeSpace(dir string) (uint64, error) {
	if c.FreeSpace != nil {
		return c.FreeSpace(dir)
	}
	return worker.DiskFree(dir)
}

// Folder checks a folder for use as a library: it can be opened and written by the user the program
// runs as, it holds videos, its disk has room for converting the largest one, and the converter works.
// It only reads, except the write test's temp file, which is always removed.
func (c *Checker) Folder(ctx context.Context, path string) (r FolderReport) {
	r = FolderReport{Blockers: []Issue{}, Notes: []Issue{}}
	defer func() { r.OK = len(r.Blockers) == 0 }() // r is the named result, so this reaches the caller
	st, err := os.Stat(path)
	if err == nil && !st.IsDir() {
		err = fmt.Errorf("not a folder")
	}
	if err == nil {
		_, err = os.ReadDir(path)
	}
	if err != nil {
		r.Blockers = append(r.Blockers, Issue{"unreadable", "We can't open this folder. Check the path, and that shrink-your-shit is allowed to read it."})
		return r
	}
	if err := writeProbe(path); err != nil {
		r.Blockers = append(r.Blockers, Issue{"read_only", ReadOnlyMessage(path)})
	}

	var largest int64
	hardlinked := 0
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != path && scanner.SkipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !scanner.IsVideo(p) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		r.VideoCount++
		r.TotalBytes += info.Size()
		largest = max(largest, info.Size())
		if linkCount(info) > 1 {
			hardlinked++
		}
		return nil
	})

	if r.VideoCount == 0 {
		r.Blockers = append(r.Blockers, Issue{"no_videos", "We found no videos here. Pick the folder that holds them."})
	} else if free, err := c.freeSpace(path); err == nil {
		if need := worker.SpaceNeeded(largest); free < need {
			r.Blockers = append(r.Blockers, Issue{"low_space", fmt.Sprintf(
				"Only %s free on this disk. Free up space (about %s is enough to start), then press Check again.", gb(free), gb(need))})
		}
	}
	tools := c.Tools(ctx)
	if !tools.Converts() {
		r.Blockers = append(r.Blockers, Issue{"no_converter", "The converter is not installed correctly."})
	}
	// Messages here are shown as they are in first-run: plain words only (DESIGN/ACCEPTANCE.md COPY-1,
	// FIRST-RUN.md "States and errors"). The health report's details may be technical.
	if hardlinked == 1 {
		r.Notes = append(r.Notes, Issue{"hardlinks", "1 file is shared with another folder (for example by Radarr). This version still converts it, but that frees no space, because the other folder keeps its copy. Leaving it alone comes in a later update."})
	} else if hardlinked > 1 {
		r.Notes = append(r.Notes, Issue{"hardlinks", fmt.Sprintf("%d files are shared with another folder (for example by Radarr). This version still converts them, but that frees no space, because the other folder keeps its copy. Leaving them alone comes in a later update.", hardlinked)})
	}
	if len(tools.Hardware) == 0 {
		r.Notes = append(r.Notes, Issue{"no_hw_encoder", "Not found. That is fine."})
	}
	return r
}

// writeProbe creates and removes a file in dir, as the user this program runs as.
func writeProbe(dir string) error {
	f, err := os.CreateTemp(dir, ".syp-write-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// ReadOnlyMessage is design's read-only copy (FIRST-RUN.md) followed by how to make the folder
// writable for the user the program runs as, on this system: the Linux service runs as "syp", the
// container as a numeric user, macOS and Windows as the person's own account.
func ReadOnlyMessage(path string) string {
	const lead = "This folder is read-only. Make it writable, then press Check again. "
	name := "this user"
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	switch {
	case runtime.GOOS == "windows":
		return lead + fmt.Sprintf("Right-click the folder, choose Properties > Security, and give your account (%s) the Modify permission; also check the folder is not marked Read-only.", name)
	case runtime.GOOS == "darwin":
		return lead + fmt.Sprintf("In Finder, select the folder, choose File > Get Info, and under Sharing & Permissions give your account (%s) Read & Write.", name)
	case inContainer():
		ids := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
		return lead + fmt.Sprintf("shrink-your-shit runs inside its container as user %s. On the host, make the folder you mounted writable for that user, for example: sudo chown -R %s <the host folder>", ids, ids)
	case name == "syp":
		return lead + fmt.Sprintf("shrink-your-shit runs as the user syp. On this server: sudo chown -R syp:syp %s (or add syp to the folder's group and give the group write access).", path)
	default:
		return lead + fmt.Sprintf("shrink-your-shit runs as the user %s. For example: sudo chown -R %s %s", name, name, path)
	}
}

func inContainer() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// gb writes a byte count in GB, with one decimal under 10 GB.
func gb(n uint64) string {
	g := float64(n) / (1 << 30)
	if g < 10 {
		return fmt.Sprintf("%.1f GB", g)
	}
	return fmt.Sprintf("%.0f GB", g)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
