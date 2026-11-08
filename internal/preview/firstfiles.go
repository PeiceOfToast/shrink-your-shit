// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package preview

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/scanner"
)

// First files ("Is this the right folder?", slice A, DESIGN/REDESIGN A3): the first 5 videos of a folder
// A to Z by path, with their probed codec, resolution and length, and how many videos there are. A walk
// runs in the background per folder; each call answers what it has found so far, and the page calls
// again until stillLooking is false (the walk and the 5 probes done). The same rules as the scanner:
// video names only, hidden and system folders left out, links never followed or listed (scanner.Guard),
// a library's Ignore list applied. A finished answer is kept for firstFilesTTL.

// FirstFile is one of the first 5 videos.
type FirstFile struct {
	Path        string  `json:"path"`
	Folder      string  `json:"folder"` // inside the folder, "." for the top
	Name        string  `json:"name"`
	Bytes       int64   `json:"bytes"`
	State       string  `json:"state"` // checking | ready | unreadable
	Codec       *string `json:"codec"`
	Resolution  *string `json:"resolution"`
	DurationSec *int    `json:"durationSec"`
}

// FirstFiles is the answer.
type FirstFiles struct {
	Items        []FirstFile `json:"items"`
	Total        int         `json:"total"`
	StillLooking bool        `json:"stillLooking"`
}

const (
	firstFilesN     = 5
	firstFilesTTL   = 10 * time.Minute
	firstProbeLimit = 20 * time.Second
)

// FirstLister runs and keeps the walks.
type FirstLister struct {
	Exec    media.Exec
	FFprobe string

	mu   sync.Mutex
	runs map[string]*firstRun
}

type firstRun struct {
	mu      sync.Mutex
	items   []FirstFile
	total   int
	looking bool
	done    time.Time
}

func (r *firstRun) answer() FirstFiles {
	r.mu.Lock()
	defer r.mu.Unlock()
	return FirstFiles{Items: append([]FirstFile{}, r.items...), Total: r.total, StillLooking: r.looking}
}

// Known reports whether key has a walk running or a recent answer (so a caller need not check the
// folder again before Get).
func (f *FirstLister) Known(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.runs[key]
	return r != nil && r.fresh()
}

func (r *firstRun) fresh() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.looking || time.Since(r.done) < firstFilesTTL
}

// Get answers for root (a resolved folder), starting a walk if none is running or recent. key names the
// walk: the folder, plus the Ignore list when ignored is a library's (a path relative to root), so a
// library's answer never comes from a walk without its Ignore list or with an older one.
func (f *FirstLister) Get(key, root string, ignored func(rel string) bool) FirstFiles {
	f.mu.Lock()
	if f.runs == nil {
		f.runs = map[string]*firstRun{}
	}
	r := f.runs[key]
	if r == nil || !r.fresh() {
		r = &firstRun{looking: true, items: []FirstFile{}}
		f.runs[key] = r
		go f.run(r, root, ignored)
	}
	f.mu.Unlock()
	return r.answer()
}

func (f *FirstLister) run(r *firstRun, root string, ignored func(rel string) bool) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if rel != "." && ignored != nil && ignored(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != root && scanner.SkipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !scanner.IsVideo(p) {
			return nil
		}
		st, err := os.Lstat(p)
		if err != nil {
			return nil
		}
		// the file guards: a link is never listed or probed (one inside is ignored, one leading outside is
		// the symlink skip: probing it would read outside the folder, review 34c56df); only regular files
		// are; a shared file is listed (it is read, never changed)
		if code, ignore := scanner.Guard(root, p, st); ignore || code == rules.SkipSymlink || !st.Mode().IsRegular() {
			return nil
		}
		r.add(root, p, st.Size())
		return nil
	})
	// the 5, probed one by one (each answer shows as soon as it is read)
	r.mu.Lock()
	paths := make([]string, len(r.items))
	for i, it := range r.items {
		paths[i] = it.Path
	}
	r.mu.Unlock()
	for _, p := range paths {
		ctx, cancel := context.WithTimeout(context.Background(), firstProbeLimit)
		info, err := media.Probe(ctx, f.Exec, f.FFprobe, p)
		cancel()
		r.mu.Lock()
		for i := range r.items {
			if r.items[i].Path != p {
				continue
			}
			if v := info.Video(); err != nil || v == nil {
				r.items[i].State = "unreadable"
			} else {
				c, res, d := CodecWord(v.Codec), ResolutionWord(v.Width, v.Height), int(info.DurationSec+0.5)
				r.items[i].State, r.items[i].Codec, r.items[i].Resolution, r.items[i].DurationSec = "ready", &c, &res, &d
			}
		}
		r.mu.Unlock()
	}
	r.mu.Lock()
	r.looking, r.done = false, time.Now()
	r.mu.Unlock()
}

// add counts one video and keeps it if it is among the first 5 by path.
func (r *firstRun) add(root, p string, size int64) {
	rel, _ := filepath.Rel(root, p)
	folder := filepath.ToSlash(filepath.Dir(rel))
	r.mu.Lock()
	defer r.mu.Unlock()
	r.total++
	if len(r.items) == firstFilesN && rel >= relOf(root, r.items[firstFilesN-1].Path) {
		return
	}
	r.items = append(r.items, FirstFile{Path: p, Folder: folder, Name: filepath.Base(p), Bytes: size, State: "checking"})
	sort.Slice(r.items, func(i, j int) bool { return relOf(root, r.items[i].Path) < relOf(root, r.items[j].Path) })
	if len(r.items) > firstFilesN {
		r.items = r.items[:firstFilesN]
	}
}

func relOf(root, p string) string { rel, _ := filepath.Rel(root, p); return rel }

// codecWords are the codec names people use (DESIGN/REDESIGN A3: "H.264").
var codecWords = map[string]string{"h264": "H.264", "hevc": "HEVC", "av1": "AV1", "vp9": "VP9", "vp8": "VP8",
	"mpeg2video": "MPEG-2", "mpeg1video": "MPEG-1", "mpeg4": "MPEG-4", "msmpeg4v3": "MPEG-4", "vc1": "VC-1",
	"wmv1": "WMV", "wmv2": "WMV", "wmv3": "WMV", "prores": "ProRes"}

// CodecWord is a video codec as people name it.
func CodecWord(codec string) string {
	if w, ok := codecWords[codec]; ok {
		return w
	}
	return strings.ToUpper(codec)
}

// ResolutionWord is a picture size as people name it: 480p ... 1080p, 1440p, 4K, 8K by its height, when
// within 10% of one; else width×height.
func ResolutionWord(w, h int) string {
	for _, r := range []struct {
		h    int
		name string
	}{{480, "480p"}, {576, "576p"}, {720, "720p"}, {1080, "1080p"}, {1440, "1440p"}, {2160, "4K"}, {4320, "8K"}} {
		if d := h - r.h; d*10 <= r.h && -d*10 <= r.h {
			return r.name
		}
	}
	return strconv.Itoa(w) + "×" + strconv.Itoa(h)
}
