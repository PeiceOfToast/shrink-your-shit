// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package scanner walks library folders, probes new or changed video files
// and queues jobs for the ones the rule matches.
package scanner

import (
	"context"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
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
// (.Trash, .AppleDouble, ...) and Synology's @eaDir.
func SkipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "@eaDir"
}

// Scanner finds work.
type Scanner struct {
	Store     *jobs.Store
	Exec      media.Exec
	FFprobe   string
	Rule      rules.Rule
	Libraries []string
	// MinAge skips files modified more recently than this (still being copied).
	MinAge time.Duration
	Now    func() time.Time
}

// ScanOnce walks every library once and returns how many jobs it queued.
func (s *Scanner) ScanOnce(ctx context.Context) int {
	queued := 0
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	for _, lib := range s.Libraries {
		filepath.WalkDir(lib, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
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
			if s.scanFile(ctx, lib, path, info) {
				queued++
			}
			return nil
		})
	}
	return queued
}

func (s *Scanner) scanFile(ctx context.Context, lib, path string, st os.FileInfo) bool {
	prev, err := s.Store.GetFile(path)
	if err != nil {
		log.Printf("scan %s: %v", path, err)
		return false
	}
	if prev != nil && prev.Size == st.Size() && prev.ModTime == st.ModTime().UnixNano() {
		return false // unchanged since last look
	}
	if open, _ := s.Store.HasOpenJob(path); open {
		return false // already waiting or running; it will be re-checked after it finishes
	}
	pi, err := media.Probe(ctx, s.Exec, s.FFprobe, path)
	rec := jobs.File{Path: path, Size: st.Size(), ModTime: st.ModTime().UnixNano()}
	if err != nil {
		log.Printf("probe %s: %v", path, err)
		rec.Decision = "unreadable"
		s.Store.PutFile(rec)
		return false
	}
	rec.Codec = pi.VideoCodec
	ok, why := s.Rule.Match(pi)
	if !ok {
		rec.Decision = "skip: " + why
		s.Store.PutFile(rec)
		return false
	}
	rec.Decision = "queued: " + why
	if _, err := s.Store.Enqueue(jobs.Job{Library: lib, Path: path, Reason: why, DurationSec: pi.DurationSec, SizeBefore: st.Size()}); err != nil {
		log.Printf("enqueue %s: %v", path, err)
		return false
	}
	s.Store.PutFile(rec)
	return true
}

// Run scans every interval until ctx is cancelled (polling works on WSL2 where file events don't).
func (s *Scanner) Run(ctx context.Context, every time.Duration) {
	for {
		if n := s.ScanOnce(ctx); n > 0 {
			log.Printf("scan: queued %d job(s)", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
