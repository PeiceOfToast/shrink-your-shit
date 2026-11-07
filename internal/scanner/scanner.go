// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package scanner walks library folders, probes new or changed video files
// and queues jobs for the ones the rule matches.
package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

var videoExt = map[string]bool{".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".ts": true, ".wmv": true}

// IsVideo reports whether the path looks like a video we handle.
// Our own temp and output files are ignored.
func IsVideo(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if strings.HasPrefix(base, ".") {
		return false // hidden files, e.g. macOS "._name.mp4" metadata stubs
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if strings.HasSuffix(stem, ".syp-tmp") || strings.HasSuffix(stem, ".hevc") {
		return false
	}
	return videoExt[ext]
}

// SkipDir reports whether a folder should not be scanned: hidden folders
// (.Trash, .AppleDouble, ...), Synology's @eaDir and Windows' recycle bin.
func SkipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch strings.ToLower(name) {
	case "@eadir", "$recycle.bin", "system volume information":
		return true
	}
	return false
}

// Scanner finds work.
type Scanner struct {
	Store   *jobs.Store
	Exec    media.Exec
	FFprobe string
	// Targets lists what to scan on each pass: the started libraries, each with its flow's template.
	Targets func() ([]Target, error)
	// MinAge skips files modified more recently than this (still being copied).
	MinAge time.Duration
	Now    func() time.Time

	wake     chan struct{}
	mu       sync.Mutex
	nextScan time.Time
}

// Target is one library to scan.
type Target struct {
	LibraryID int64
	Path      string
	Template  rules.Template // compiled per file into a Plan, which decides whether to convert it
}

// ScanOnce walks every library once and returns how many jobs it queued.
func (s *Scanner) ScanOnce(ctx context.Context) int {
	queued := 0
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	targets, err := s.Targets()
	if err != nil {
		log.Printf("scan: cannot read the libraries: %v", err)
		return 0
	}
	for _, t := range targets {
		lib := t.Path
		filepath.WalkDir(lib, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return nil
			}
			if rel, err := filepath.Rel(lib, path); err == nil && rel != "." && t.Template.Ignored(rel) {
				if d.IsDir() {
					return fs.SkipDir // the flow's Ignore list: not looked at
				}
				return nil
			}
			if d.IsDir() {
				if path != lib && SkipDir(d.Name()) {
					return fs.SkipDir
				}
				return nil
			}
			if !IsVideo(path) {
				return nil
			}
			info, err := d.Info()
			if err != nil || now().Sub(info.ModTime()) < s.MinAge {
				return nil
			}
			if s.scanFile(ctx, t, path, info) {
				queued++
			}
			return nil
		})
	}
	return queued
}

func (s *Scanner) scanFile(ctx context.Context, t Target, path string, st os.FileInfo) bool {
	prev, err := s.Store.GetFile(path)
	if err != nil {
		log.Printf("scan %s: %v", path, err)
		return false
	}
	if prev != nil && prev.Decision == jobs.DecisionRestored {
		return false // its original was put back: not converted again until "Convert again"
	}
	if prev != nil && prev.Size == st.Size() && prev.ModTime == st.ModTime().UnixNano() {
		return false // unchanged since last look
	}
	if open, _ := s.Store.HasOpenJob(path); open {
		return false // already waiting or running; it will be re-checked after it finishes
	}
	pi, err := Probe(ctx, s.Store, s.Exec, s.FFprobe, path, st)
	rec := jobs.File{Path: path, Size: st.Size(), ModTime: st.ModTime().UnixNano()}
	if err != nil {
		log.Printf("probe %s: %v", path, err)
		rec.Decision, rec.Reason = "unreadable", rules.SkipUnreadable
		s.Store.PutFile(rec)
		return false
	}
	rec.Codec = pi.VideoCodec
	plan := t.Template.Compile(pi)
	if plan.Skip != "" {
		rec.Decision, rec.Reason = "skip: "+plan.Skip, plan.SkipCode
		s.Store.PutFile(rec)
		return false
	}
	why := plan.Reason
	rec.Decision = "queued: " + why
	if _, err := s.Store.Enqueue(jobs.Job{Library: t.Path, LibraryID: t.LibraryID, Path: path, Reason: why, DurationSec: pi.DurationSec, SizeBefore: st.Size()}); err != nil {
		if !errors.Is(err, jobs.ErrLibraryGone) { // removed or stopped while this pass ran: nothing to do
			log.Printf("enqueue %s: %v", path, err)
		}
		return false
	}
	s.Store.PutFile(rec)
	return true
}

// Probe is ffprobe's view of path, from the store's probe cache when the file has the same size and
// modification time as when it was last probed. Only reads the file.
func Probe(ctx context.Context, store *jobs.Store, x media.Exec, ffprobe, path string, st os.FileInfo) (media.Info, error) {
	size, mtime := st.Size(), st.ModTime().UnixNano()
	var info media.Info
	if b, ok := store.GetProbe(path, size, mtime); ok && json.Unmarshal([]byte(b), &info) == nil {
		return info, nil
	}
	info, err := media.Probe(ctx, x, ffprobe, path)
	if err != nil {
		return info, err
	}
	if b, err := json.Marshal(info); err == nil {
		if err := store.PutProbe(path, size, mtime, string(b)); err != nil {
			log.Printf("probe cache %s: %v", path, err) // the probe is still good; it is just taken again next time
		}
	}
	return info, nil
}

// Run scans every interval until ctx is cancelled (polling works on WSL2 where file events don't),
// and at once when ScanNow is called.
func (s *Scanner) Run(ctx context.Context, every time.Duration) {
	s.mu.Lock()
	if s.wake == nil {
		s.wake = make(chan struct{}, 1)
	}
	wake := s.wake
	s.mu.Unlock()
	for {
		if n := s.ScanOnce(ctx); n > 0 {
			log.Printf("scan: queued %d job(s)", n)
		}
		s.mu.Lock()
		s.nextScan = time.Now().Add(every)
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		case <-wake:
		}
	}
}

// ScanNow asks Run for a pass now (it does not wait for it). A pass already running finishes first.
func (s *Scanner) ScanNow() {
	s.mu.Lock()
	if s.wake == nil {
		s.wake = make(chan struct{}, 1)
	}
	w := s.wake
	s.mu.Unlock()
	select {
	case w <- struct{}{}:
	default: // one is already waiting
	}
}

// NextScan is when the next regular pass starts (zero before the first pass has finished).
func (s *Scanner) NextScan() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextScan
}
