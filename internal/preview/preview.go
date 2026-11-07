// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package preview is the dry run (SAF-3, DESIGN/FIRST-RUN.md "Preview"): for a library and a setting,
// how many files would be converted, how many skipped and why, and roughly how much space it could
// free, as a range. It reads only: files are listed and probed (ffprobe, through the store's probe
// cache), never written, and no job is made.
package preview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/scanner"
)

// Result is a library's Preview (UI-API-CONTRACT.md "Preview", addenda 3, 5 and 12).
type Result struct {
	Stale             bool           `json:"stale"` // the files changed since this was computed; a new one is computing
	EstimateBytesLow  int64          `json:"estimateBytesLow"`
	EstimateBytesHigh int64          `json:"estimateBytesHigh"`
	Files             int            `json:"files"`
	TotalBytes        int64          `json:"totalBytes"`
	CanShrink         int            `json:"canShrink"`
	Skipped           map[string]int `json:"skipped"`
	// SharedNoSaving is always 0 since the file guards (slice 23): files shared with another folder are
	// skipped (skipped.shared_file), not converted. Kept for the contract (addendum 12).
	SharedNoSaving int    `json:"sharedNoSaving"`
	Status         string `json:"status"` // ready | computing | failed
	// Progress, while computing: how many of the videos have been looked at, so a page can show it
	// moving instead of giving up on a big library.
	Progress *Progress `json:"progress,omitempty"`
}

// Progress is how far a computing Preview is.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// Previewer computes Previews and keeps the latest one per library and setting.
type Previewer struct {
	Store   *jobs.Store
	Exec    media.Exec
	FFprobe string
	// Probes is how many files are probed at once (default: the number of CPUs, at least 2).
	Probes int

	mu    sync.Mutex
	cache map[string]*run
	slots chan struct{}
}

// run is one computation of a Preview over one listing of the folder.
type run struct {
	sig      string // the listing it was computed over
	finished chan struct{}
	res      Result
	prev     *run // the last finished run for the same setting, shown as stale while this one computes

	total int
	done  atomic.Int64 // files looked at so far
}

func (r *run) progress() *Progress { return &Progress{Done: int(r.done.Load()), Total: r.total} }

// file is a video in the library, as listed.
type file struct {
	path string
	st   fs.FileInfo
}

// list walks the library like the scanner does (same folders skipped, same videos counted) and returns
// its videos with a signature of the listing: it changes when a file is added, removed or changed, or
// gains or loses a link elsewhere.
func list(root string) ([]file, string, error) {
	var out []file
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil // a folder inside that cannot be read: its files are not converted either
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
		if st, err := os.Lstat(p); err == nil { // as the scanner: a link is seen as a link, the file itself read
			if _, ignore := scanner.Guard(root, p, st); !ignore {
				out = append(out, file{p, st})
			}
		}
		return nil
	})
	h := sha256.New()
	for _, f := range out {
		// The link count too: a link made or removed in another folder changes no size or time here,
		// but changes which files free no space (sharedNoSaving).
		b, _ := json.Marshal([]any{f.path, f.st.Size(), f.st.ModTime().UnixNano(), media.LinkCount(f.path, f.st)})
		h.Write(b)
	}
	return out, hex.EncodeToString(h.Sum(nil)[:8]), err
}

// settingKey identifies a setting: equal templates compile every file to the same Plan.
func settingKey(libID int64, t rules.Template) string {
	b, _ := json.Marshal(t)
	h := sha256.Sum256(b)
	return fmt.Sprintf("%d/%x", libID, h[:8])
}

// Library returns the Preview of the library at root under template t. A Preview already computed for
// the same setting over the same files is returned at once. Otherwise one is started and waited for up
// to wait; if it takes longer, the answer is "computing" (with the previous one, marked stale, when the
// files changed since) and a later call picks it up.
func (p *Previewer) Library(libID int64, root string, t rules.Template, wait time.Duration) Result {
	files, sig, err := list(root)
	if err != nil {
		return Result{Status: "failed", Skipped: map[string]int{}}
	}
	kept := files[:0:0] // what the flow's Ignore list leaves out is not counted, as the scanner does not look at it
	for _, f := range files {
		if rel, err := filepath.Rel(root, f.path); err != nil || !t.Ignored(rel) {
			kept = append(kept, f)
		}
	}
	files = kept
	key := settingKey(libID, t)
	p.mu.Lock()
	if p.cache == nil {
		p.cache = map[string]*run{}
	}
	r := p.cache[key]
	if r == nil || r.sig != sig {
		var prev *run
		if r != nil {
			select {
			case <-r.finished:
				prev = r
			default:
				prev = r.prev
			}
		}
		r = &run{sig: sig, finished: make(chan struct{}), prev: prev, total: len(files)}
		p.cache[key] = r
		go func(r *run) {
			r.res = p.compute(root, files, t, &r.done)
			close(r.finished)
		}(r)
	}
	p.mu.Unlock()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-r.finished:
		return r.res
	case <-timer.C:
	}
	select { // finished just as the wait ran out
	case <-r.finished:
		return r.res
	default:
	}
	if r.prev != nil {
		old := r.prev.res
		old.Stale, old.Status, old.Progress = true, "computing", r.progress()
		return old
	}
	return Result{Status: "computing", Skipped: map[string]int{}, Progress: r.progress()}
}

// compute probes every file (a few at a time) and adds up their Plans.
func (p *Previewer) compute(root string, files []file, t rules.Template, done *atomic.Int64) Result {
	p.mu.Lock()
	if p.slots == nil {
		n := p.Probes
		if n <= 0 {
			n = max(2, runtime.NumCPU()) // ffprobe is short and partly waits on the disk
		}
		p.slots = make(chan struct{}, n)
	}
	slots := p.slots
	p.mu.Unlock()

	type one struct {
		plan    rules.Plan
		save    [2]int64
		skipped string
	}
	results := make([]one, len(files))
	var wg sync.WaitGroup
	for i, f := range files {
		wg.Add(1)
		slots <- struct{}{} // shared by every computation, so several settings at once do not overload the disk
		go func(i int, f file) {
			defer func() { done.Add(1); <-slots; wg.Done() }()
			// Each probe gets its own time limit; a computation is not tied to the request that started it.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if code, _ := scanner.Guard(root, f.path, f.st); code != "" {
				results[i].skipped = code // the file guards, exactly as the scanner decides
				return
			}
			if rec, _ := p.Store.GetFile(f.path); rec != nil && rec.Decision == jobs.DecisionRestored {
				results[i].skipped = jobs.DecisionRestored // the scanner skips it too
				return
			}
			info, err := scanner.Probe(ctx, p.Store, p.Exec, p.FFprobe, f.path, f.st)
			if err != nil {
				results[i].skipped = rules.SkipUnreadable
				return
			}
			plan := t.Compile(info)
			if plan.Skip != "" {
				results[i].skipped = plan.SkipCode
				if plan.SkipCode == "" {
					results[i].skipped = "other"
				}
				return
			}
			results[i].plan = plan
			lo, hi := Saving(t, plan, info, f.st.Size())
			results[i].save = [2]int64{lo, hi}
		}(i, f)
	}
	wg.Wait()

	res := Result{Status: "ready", Skipped: map[string]int{}, Files: len(files)}
	for i, r := range results {
		res.TotalBytes += files[i].st.Size()
		if r.skipped != "" {
			res.Skipped[r.skipped]++
			continue
		}
		res.CanShrink++
		res.EstimateBytesLow += r.save[0]
		res.EstimateBytesHigh += r.save[1]
	}
	return res
}

// Saving is the range of bytes converting one file could free under template t (v1, orch-lead's ruling
// on 2026-11-07): the file's video bytes times its position's SaveLowPct..SaveHighPct (rules.Slider).
// Audio, subtitles and the container are copied, so they save nothing. A file whose source is within
// rules.NearMinBPP of its position's threshold counts from 0 at the low end (the minimum saving keeps
// many of those), and one whose best case misses the minimum saving counts as kept (0). The low end is
// the position's minimum saving, so it is not cut to 0 just for sitting at that line.
func Saving(t rules.Template, plan rules.Plan, info media.Info, size int64) (low, high int64) {
	if plan.Skip != "" || size <= 0 {
		return 0, 0
	}
	pos := t.EstimatePosition()
	kbps, bpp := rules.SourceVideo(info)
	video := size // bitrate unknown (only a legacy flow converts such files): the whole file
	if kbps > 0 && info.DurationSec > 0 {
		video = min(size, int64(float64(kbps)*1000/8*info.DurationSec))
	}
	low = video * int64(pos.SaveLowPct) / 100
	high = video * int64(pos.SaveHighPct) / 100
	floor := size * int64(plan.MinSavingPct) / 100
	if high < floor || high <= 0 {
		return 0, 0
	}
	near := bpp > 0 && pos.MinBPP > 0 && bpp <= pos.MinBPP*rules.NearMinBPP
	if t.Kind == rules.KindLegacy {
		near = t.Params.MinBitrateKbps > 0 && kbps > 0 && float64(kbps) <= float64(t.Params.MinBitrateKbps)*rules.NearMinBPP
	}
	if near {
		low = 0
	}
	return low, high
}

// Files lists the library's videos whose path (inside the library) contains q, case-insensitively,
// sorted by that path: the choices for a sample file.
func Files(root, q string) ([]Candidate, error) {
	files, _, err := list(root)
	if err != nil {
		return nil, err
	}
	out := []Candidate{}
	q = strings.ToLower(q)
	for _, f := range files {
		rel, _ := filepath.Rel(root, f.path)
		if q == "" || strings.Contains(strings.ToLower(rel), q) {
			out = append(out, Candidate{Path: f.path, Name: filepath.ToSlash(rel), Bytes: f.st.Size()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Candidate is a file that can be chosen as the sample.
type Candidate struct {
	Path  string `json:"path"`
	Name  string `json:"name"` // its path inside the library
	Bytes int64  `json:"bytes"`
}
