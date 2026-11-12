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

var videoExt = map[string]bool{".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".ts": true, ".wmv": true, ".webm": true}

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
	reset     chan struct{} // SetEvery: the wait is timed again
	every     time.Duration // how often a pass starts (Run's, or SetEvery's)
	mu        sync.Mutex
	nextScan  time.Time
	firstLook map[string]look // each file as first seen unchanged (the two-look rule)
	scanning  map[int64]int   // library id -> videos found so far, while its walk runs (slice A)
}

// Scanning is the libraries being walked now, with the videos found so far in each.
func (s *Scanner) Scanning() map[int64]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[int64]int, len(s.scanning))
	for id, n := range s.scanning {
		out[id] = n
	}
	return out
}

func (s *Scanner) setScanning(id int64, n int, done bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scanning == nil {
		s.scanning = map[int64]int{}
	}
	if done {
		delete(s.scanning, id)
	} else {
		s.scanning[id] = n
	}
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
	sizes := map[string]int64{} // each seen file's size: an original left in place that changed (V5c)
	var walked []int64          // libraries walked whole in this pass
	for _, t := range targets {
		lib := t.Path
		videos := 0
		s.setScanning(t.LibraryID, 0, false)
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
			// Lstat (a link is seen as a link), and from the file itself: on Windows a directory
			// listing's size and time can lag behind the file's
			info, err := os.Lstat(path)
			if err != nil {
				return nil
			}
			seen[path], sizes[path] = true, info.Size()
			videos++
			s.setScanning(t.LibraryID, videos, false)
			if code, ignore := Guard(lib, path, info); ignore {
				return nil
			} else if code != "" {
				s.recordSkip(path, info, code)
				// its first look counts from here if a guard later lets it go; never for a link, so the
				// target outside the library is never opened (review aa9cd46). A link that becomes a
				// real file gets a new identity, so a fresh first look anyway.
				if code != rules.SkipSymlink {
					s.observe(path, info, now())
				}
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
		s.setScanning(t.LibraryID, 0, true)
		if ctx.Err() == nil && t.LibraryID > 0 { // a whole walk: the card's count and time
			if err := s.Store.SetLibraryScanned(t.LibraryID, videos); err != nil {
				log.Printf("scan: %s: %v", lib, err)
			}
			walked = append(walked, t.LibraryID)
		}
	}
	if ctx.Err() == nil {
		s.forgetUnseen(seen)
		s.markOriginalsGone(walked, sizes)
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

// fileState is what tells one state of a file from another: media.State (identity, size, modification
// time and, on Windows, creation and change times) and, elsewhere, the change time from the Lstat. A file made again under
// the same name with the same size and times is a different one. unknown: the file could not be read
// (FileState failed), so size and time are the listing's and its identity is not known; such a state
// is never the same as any other, so the file waits until it can be read twice alike (review 9cf6a03).
type fileState struct {
	media.State
	ctime    time.Time
	hasCtime bool
	unknown  bool
}

// readFileState is media.FileState; tests watch which files it opens and make it fail.
var readFileState = media.FileState

func stateOf(path string, st os.FileInfo) fileState {
	c, hasC := media.ChangeTime(st)
	ms, err := readFileState(path, st)
	if err != nil {
		return fileState{State: media.State{Size: st.Size(), MTime: st.ModTime()}, ctime: c, hasCtime: hasC, unknown: true}
	}
	return fileState{State: ms, ctime: c, hasCtime: hasC}
}

func (a fileState) same(b fileState) bool {
	return !a.unknown && !b.unknown && a.State.Same(b.State) && a.hasCtime == b.hasCtime && a.ctime.Equal(b.ctime)
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
	// No floor beyond MinAge: a first look is never decided in its own pass (observe says it is first),
	// so a coarse clock (Windows) cannot hold back a file that was seen unchanged twice.
	if !ok || !l.state.same(stateOf(path, st)) || now.Sub(l.at) < s.MinAge {
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
	// A file already waiting or converting is decided: it is re-checked after its job ends. Before the
	// two-look rule, or a file whose decision was forgotten (its library's setting was saved) would be
	// recorded as still changing for as long as its job waits (ui-shop's S4 run).
	if open, _ := s.Store.HasOpenJob(path); open {
		return false
	}
	// our own output (replace with a new file type, a folder for the new files): never converted again
	if own, err := s.Store.IsOwnOutput(path, st.Size(), st.ModTime().UnixNano()); err != nil {
		log.Printf("scan %s: %v", path, err)
		return false
	} else if own {
		return false
	}
	if s.observe(path, st, now()) || !s.ready(path, st, now()) {
		s.recordSkip(path, st, rules.SkipStillChanging)
		return false
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
	if s.Store.IntakePaused() { // V3c: nothing new is queued; not recorded, so it's looked at again on resume
		return false
	}
	why := plan.Reason
	rec.Decision = "queued: " + why
	id, err := s.Store.Enqueue(jobs.Job{Library: t.Path, LibraryID: t.LibraryID, Path: path, Reason: why, DurationSec: pi.DurationSec, SizeBefore: st.Size()})
	if err != nil {
		if !errors.Is(err, jobs.ErrLibraryGone) { // removed or stopped while this pass ran: nothing to do
			log.Printf("enqueue %s: %v", path, err)
		}
		return false
	}
	s.Store.AddStep(id, "found", true, "Found in "+t.Path+".") // the Files view's step summary (S5)
	for _, d := range plan.Decisions {                         // a workflow with checks (V6a): each answer, on the job's timeline
		s.Store.AddDecision(id, d.NodeID, d.Port, d.Words)
	}
	if len(plan.Path) > 0 {
		s.Store.SetRoute(id, plan.Path)
	}
	s.Store.AddStep(id, "checked", true, "To be converted: "+why+".")
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
	if b, ok := store.GetProbe(path, size, mtime); ok && json.Unmarshal([]byte(b), &info) == nil && info.Probe >= media.ProbeVersion {
		return info, nil // (an older probe misses what this version reads, e.g. S3's HDR side data)
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
	if s.reset == nil {
		s.reset = make(chan struct{}, 1)
	}
	if s.every == 0 { // SetEvery before Run (a saved setting) wins over the flag
		s.every = every
	}
	wake, reset := s.wake, s.reset
	s.mu.Unlock()
	for {
		if n := s.ScanOnce(ctx); n > 0 {
			log.Printf("scan: queued %d job(s)", n)
		}
		done := time.Now()
	wait:
		for {
			s.mu.Lock()
			next := done.Add(s.every) // a new interval counts from the last pass (SetEvery)
			s.nextScan = next
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Until(next)):
				break wait
			case <-wake:
				break wait
			case <-reset:
			}
		}
	}
}

// SetEvery changes how often Run starts a pass, at once: the next one is due that long after the last
// (Settings' scanEvery, applied without a restart).
func (s *Scanner) SetEvery(every time.Duration) {
	s.mu.Lock()
	s.every = every
	if s.reset == nil {
		s.reset = make(chan struct{}, 1)
	}
	r := s.reset
	s.mu.Unlock()
	select {
	case r <- struct{}{}:
	default:
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

// markOriginalsGone records the originals left where they were (beside, or with a folder for new files)
// that this pass no longer found, so their saving counts as freed, and those that changed (another
// size: it is a new file, converted on its own), whose saving no longer counts (V5c, design call 3).
// Only in the libraries walked whole (a paused one isn't), and gone only when the file is really not
// there: a folder that couldn't be read hides its files from a walk without deleting them.
func (s *Scanner) markOriginalsGone(walked []int64, sizes map[string]int64) {
	left, err := s.Store.LeftOriginals(walked)
	if err != nil {
		log.Printf("scan: originals left in place: %v", err)
		return
	}
	for _, o := range left {
		if size, seen := sizes[o.Path]; seen {
			if size != o.Size {
				if err := s.Store.SetOriginalChanged(o.JobID); err != nil {
					log.Printf("scan: %s: %v", o.Path, err)
				}
			}
			continue
		}
		if _, err := os.Lstat(o.Path); errors.Is(err, fs.ErrNotExist) {
			if err := s.Store.SetOriginalGone(o.JobID); err != nil {
				log.Printf("scan: %s: %v", o.Path, err)
			}
		}
	}
}
