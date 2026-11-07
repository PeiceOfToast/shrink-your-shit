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

	wake      chan struct{}
	mu        sync.Mutex
	nextScan  time.Time
	firstLook map[string]look // each file as first seen unchanged (the two-look rule)
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
	seen := map[string]bool{}
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
			info, err := d.Info() // Lstat: a link is seen as a link
			if err != nil {
				return nil
			}
			seen[path] = true
			if code, ignore := Guard(lib, path, info); ignore {
				return nil
			} else if code != "" {
				s.recordSkip(path, info, code)
				s.observe(path, info, now()) // its first look counts from here if a guard later lets it go
				return nil
			}
			if now().Sub(info.ModTime()) < s.MinAge {
				return nil
			}
			if s.scanFile(ctx, t, path, info) {
				queued++
			}
			return nil
		})
	}
	if ctx.Err() == nil {
		s.forgetUnseen(seen)
	}
	return queued
}

// recordSkip records a file the guards leave alone (unless it is recorded so already).
func (s *Scanner) recordSkip(path string, st os.FileInfo, code string) {
	if prev, _ := s.Store.GetFile(path); prev != nil && prev.Reason == code && prev.Size == st.Size() && prev.ModTime == st.ModTime().UnixNano() {
		return
	}
	s.Store.PutFile(jobs.File{Path: path, Size: st.Size(), ModTime: st.ModTime().UnixNano(), Decision: "skip: " + rules.SkipWords[code], Reason: code})
}

// look is a file as it was first seen unchanged: when, and what it was.
type look struct {
	at    time.Time
	state fileState
}

// fileState is what tells one state of a file from another: which file it is (device and inode, or the
// Windows volume and file index; empty where the system does not say), its size, and its modification
// and change times (no change time on Windows). A file made again under the same name with the same
// size and times is a different one.
type fileState struct {
	key          string
	size         int64
	mtime, ctime time.Time
	hasCtime     bool
}

func stateOf(path string, st os.FileInfo) fileState {
	key, _ := media.FileKey(path, st)
	c, hasC := media.ChangeTime(st)
	return fileState{key: key, size: st.Size(), mtime: st.ModTime(), ctime: c, hasCtime: hasC}
}

func (a fileState) same(b fileState) bool {
	return a.key == b.key && a.size == b.size && a.mtime.Equal(b.mtime) && a.hasCtime == b.hasCtime && a.ctime.Equal(b.ctime)
}

// observe notes path as seen now in state st, and reports whether this is its first look in that
// state (a new file, a changed one, or one made again): then it is never decided in the same pass.
func (s *Scanner) observe(path string, st os.FileInfo, now time.Time) (first bool) {
	cur := stateOf(path, st)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.firstLook == nil {
		s.firstLook = map[string]look{}
	}
	if l, ok := s.firstLook[path]; ok && l.state.same(cur) {
		return false
	}
	s.firstLook[path] = look{now, cur}
	return true
}

// ready reports whether path has been seen in state st since at least MinAge ago (then it is decided,
// and its look forgotten).
func (s *Scanner) ready(path string, st os.FileInfo, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.firstLook[path]
	if !ok || !l.state.same(stateOf(path, st)) || now.Sub(l.at) < max(s.MinAge, time.Nanosecond) {
		return false
	}
	delete(s.firstLook, path)
	return true
}

// forgetUnseen drops the looks of files not seen in a whole pass (deleted, moved, or no longer in a
// library), so a file made again later at that path starts its wait afresh.
func (s *Scanner) forgetUnseen(seen map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for p := range s.firstLook {
		if !seen[p] {
			delete(s.firstLook, p)
		}
	}
}

func (s *Scanner) scanFile(ctx context.Context, t Target, path string, st os.FileInfo) bool {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	prev, err := s.Store.GetFile(path)
	if err != nil {
		log.Printf("scan %s: %v", path, err)
		return false
	}
	if prev != nil && (prev.Decision == jobs.DecisionRestored || prev.Reason == jobs.UserStoppedReason) {
		return false // its original was put back, or the person stopped it: not again until "Convert again"
	}
	same := prev != nil && prev.Size == st.Size() && prev.ModTime == st.ModTime().UnixNano()
	guarded := prev != nil && (prev.Reason == rules.SkipSharedFile || prev.Reason == rules.SkipSymlink)
	if same && prev.Reason != rules.SkipStillChanging && !guarded {
		return false // unchanged since it was decided
	}
	// A file is decided only when it has not changed since the previous look, at least MinAge earlier
	// (a download in progress, an import being copied): the first look, or a change, records it as
	// still changing, and the next scan decides. The look's time is kept in memory; after a restart the
	// wait starts again.
	// The two-look rule, also for a file a guard no longer holds back (its other name was removed): its
	// wait starts when it is first seen eligible, so -settle always passes before it is read (review
	// 7ff706c).
	if s.observe(path, st, now()) || !s.ready(path, st, now()) {
		s.recordSkip(path, st, rules.SkipStillChanging)
		return false
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

// Guard is what the file guards (slice 23, SAF-4) decide about the file at path in the library at root,
// from the file itself (lst is its Lstat), before anything reads it:
//   - a link is never followed or converted: one leading outside the library is skipped (symlink); one
//     inside it is not looked at (Ignore), as its real file is converted under its own name;
//   - a file shared with another folder (more than one name) is skipped (shared_file): converting it
//     frees no space, and the other name (a seeding torrent, a media manager's copy) keeps the old data.
//
// The scanner and the Preview both use it, so their decisions match.
func Guard(root, path string, lst fs.FileInfo) (code string, ignore bool) {
	if lst.Mode()&fs.ModeSymlink != 0 {
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", true // a broken link: nothing to convert
		}
		// the root resolved too: on macOS /var is a link to /private/var, so a library reached through a
		// linked folder would otherwise see every link inside it as leading outside
		if r, err := filepath.EvalSymlinks(root); err == nil {
			root = r
		}
		if rel, err := filepath.Rel(root, real); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", true
		}
		return rules.SkipSymlink, false
	}
	if media.LinkCount(path, lst) > 1 {
		return rules.SkipSharedFile, false
	}
	return "", false
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
