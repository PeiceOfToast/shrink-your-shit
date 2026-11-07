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
	"math"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
	// SharedNoSaving: of CanShrink, the files shared with another folder (link count > 1). Converting
	// them frees nothing, so the estimate leaves them out. Slice 12 moves them to skipped.shared_file.
	SharedNoSaving int    `json:"sharedNoSaving"`
	Status         string `json:"status"` // ready | computing | failed
}

// Previewer computes Previews and keeps the latest one per library and setting.
type Previewer struct {
	Store   *jobs.Store
	Exec    media.Exec
	FFprobe string
	// Probes is how many files are probed at once (default 4).
	Probes int

	mu    sync.Mutex
	cache map[string]*run
	slots chan struct{}
}

// run is one computation of a Preview over one listing of the folder.
type run struct {
	sig  string // the listing it was computed over
	done chan struct{}
	res  Result
	prev *run // the last finished run for the same setting, shown as stale while this one computes
}

// file is a video in the library, as listed.
type file struct {
	path string
	st   fs.FileInfo
}

// list walks the library like the scanner does (same folders skipped, same videos counted) and returns
// its videos with a signature of the listing: it changes when a file is added, removed or changed.
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
		if st, err := d.Info(); err == nil {
			out = append(out, file{p, st})
		}
		return nil
	})
	h := sha256.New()
	for _, f := range out {
		b, _ := json.Marshal([]any{f.path, f.st.Size(), f.st.ModTime().UnixNano()})
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
			case <-r.done:
				prev = r
			default:
				prev = r.prev
			}
		}
		r = &run{sig: sig, done: make(chan struct{}), prev: prev}
		p.cache[key] = r
		go func(r *run) {
			r.res = p.compute(files, t)
			close(r.done)
		}(r)
	}
	p.mu.Unlock()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-r.done:
		return r.res
	case <-timer.C:
	}
	select { // finished just as the wait ran out
	case <-r.done:
		return r.res
	default:
	}
	if r.prev != nil {
		old := r.prev.res
		old.Stale, old.Status = true, "computing"
		return old
	}
	return Result{Status: "computing", Skipped: map[string]int{}}
}

// compute probes every file (a few at a time) and adds up their Plans.
func (p *Previewer) compute(files []file, t rules.Template) Result {
	p.mu.Lock()
	if p.slots == nil {
		n := p.Probes
		if n <= 0 {
			n = 4
		}
		p.slots = make(chan struct{}, n)
	}
	slots := p.slots
	p.mu.Unlock()

	type one struct {
		plan    rules.Plan
		save    [2]int64
		shared  bool
		skipped string
	}
	results := make([]one, len(files))
	var wg sync.WaitGroup
	for i, f := range files {
		wg.Add(1)
		slots <- struct{}{} // shared by every computation, so several settings at once do not overload the disk
		go func(i int, f file) {
			defer func() { <-slots; wg.Done() }()
			// Each probe gets its own time limit; a computation is not tied to the request that started it.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
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
			results[i].shared = media.LinkCount(f.path, f.st) > 1
			if !results[i].shared {
				lo, hi := Saving(plan, info, f.st.Size())
				results[i].save = [2]int64{lo, hi}
			}
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
		if r.shared {
			res.SharedNoSaving++
		}
		res.EstimateBytesLow += r.save[0]
		res.EstimateBytesHigh += r.save[1]
	}
	return res
}

// The estimate (v1, TIERS-PLAN.md 2.1 and FIRST-RUN.md): from bits per pixel. The new video gets
// between OutBPPLow and OutBPPHigh bits per pixel per frame at CRF 23, halving every 6 CRF up (x265's
// rule of thumb), never more than the source has. Everything other than the video is copied, so it
// saves nothing. The band is deliberately wide: these are untuned starting values (D8), to be
// calibrated on real files.
const (
	OutBPPLow  = 0.03
	OutBPPHigh = 0.075
	// UnknownRatio is the band of new-to-old video size used when the source's bits per pixel are
	// unknown (a legacy flow converts such files).
	UnknownRatioLow, UnknownRatioHigh = 0.4, 0.8
)

// Saving is the range of bytes converting one file could free under its Plan. A file whose best case
// does not reach the Plan's minimum saving is kept, so it frees nothing; one that may miss it counts
// from zero.
func Saving(plan rules.Plan, info media.Info, size int64) (low, high int64) {
	if plan.Skip != "" || size <= 0 {
		return 0, 0
	}
	kbps, bpp := rules.SourceVideo(info)
	video := size
	if kbps > 0 && info.DurationSec > 0 {
		video = min(size, int64(float64(kbps)*1000/8*info.DurationSec))
	}
	ratioLow, ratioHigh := UnknownRatioLow, UnknownRatioHigh // new video size / old, best and worst case
	if bpp > 0 {
		scale := math.Pow(2, float64(23-plan.Video.CRF)/6)
		ratioLow, ratioHigh = math.Min(1, OutBPPLow*scale/bpp), math.Min(1, OutBPPHigh*scale/bpp)
	}
	high = int64(math.Round(float64(video) * (1 - ratioLow)))
	low = int64(math.Round(float64(video) * (1 - ratioHigh)))
	floor := int64(float64(size) * float64(plan.MinSavingPct) / 100)
	if high < floor || high <= 0 {
		return 0, 0
	}
	if low < floor {
		low = 0
	}
	return low, high
}

// Files lists the library's videos whose path (inside the library) contains q, case-insensitively,
// sorted by that path, at most limit of them: the choices for a sample file.
func Files(root, q string, limit int) ([]Candidate, error) {
	files, _, err := list(root)
	if err != nil {
		return nil, err
	}
	out := []Candidate{}
	q = strings.ToLower(q)
	for _, f := range files {
		rel, _ := filepath.Rel(root, f.path)
		if q == "" || strings.Contains(strings.ToLower(rel), q) {
			out = append(out, Candidate{Path: f.path, Name: filepath.ToSlash(rel), SizeBytes: f.st.Size()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Candidate is a file that can be chosen as the sample.
type Candidate struct {
	Path      string `json:"path"`
	Name      string `json:"name"` // its path inside the library
	SizeBytes int64  `json:"sizeBytes"`
}
