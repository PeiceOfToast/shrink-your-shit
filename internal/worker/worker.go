// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package worker runs transcode jobs. It only talks to the Queue interface,
// so a remote worker (HTTP pull) can implement Queue later; the built-in
// worker is just one instance using the local store.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Queue is everything a worker needs from the job store.
type Queue interface {
	Claim(worker string) (jobs.Job, error)
	Progress(id int64, pct float64) error
	Complete(id int64, outputPath string, sizeAfter int64) error
	Fail(id int64, msg string) error
	// Release puts a running job back in the queue (used on shutdown).
	Release(id int64) error
	// Drop deletes a job that was stopped because its library was removed.
	Drop(id int64) error
	// Keep marks a job "kept original": a safety check refused the result (reason says why).
	Keep(id int64, reason string) error
	// RecordPlan stores the Plan a running job converts with (JSON) and its fingerprint.
	RecordPlan(id int64, plan, fingerprint string) error
}

// OutputMode says what happens to the result.
type OutputMode string

const (
	// Replace swaps the original for the new file, only after the new file is complete.
	Replace OutputMode = "replace"
	// Beside keeps the original and writes name.hevc.ext next to it.
	Beside OutputMode = "beside"
)

// Worker pulls jobs from a Queue and runs ffmpeg.
type Worker struct {
	ID     string
	Queue  Queue
	Exec   media.Exec
	FFmpeg string
	// FFprobe checks the result before it replaces anything.
	FFprobe string
	// Template returns the template of a job's library's flow, compiled per file into the job's Plan.
	// Nil: a legacy template from Rule and Mode for every job.
	Template func(jobs.Job) (rules.Template, error)
	Rule     rules.Rule
	Mode     OutputMode
	Poll     time.Duration
	// OnDone is called after a job succeeds with the final path (optional).
	OnDone func(job jobs.Job, finalPath string)

	// Safety checks (see verify.go). Zero values mean the defaults.
	DecodeCheck string  // "auto" (default), "always" or "off"
	MinSSIM     float64 // 0 = DefaultMinSSIM
	// FullDecodeMaxSec: in "auto" mode, files up to this length are decoded completely, longer ones at
	// three sampled places. 0 = 3 hours.
	FullDecodeMaxSec float64
	// FreeSpace reports the writable bytes on the volume holding dir; nil = the real thing.
	FreeSpace func(dir string) (uint64, error)
	// KeepDays is how many days a replaced original is kept in .syp-trash (keep.go); 0 or nil: it is
	// deleted by the rename, as before slice 17. Journal records those swaps (needed when keeping).
	KeepDays func() int
	Journal  Journal
	Now      func() time.Time // nil: time.Now

	mu         sync.Mutex
	filterMemo map[string]bool
	current    int64              // the running job's id (0: none)
	cancelJob  context.CancelFunc // stops only the running job
	stopped    bool               // StopJob was called for the running job
}

// StopJob stops job id if this worker is running it: the original is left untouched, the temp files
// removed, and the job record dropped (its library was removed). It does not wait.
func (w *Worker) StopJob(id int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.current == id && w.cancelJob != nil {
		w.stopped = true
		w.cancelJob()
	}
}

func (w *Worker) templateFor(job jobs.Job) (rules.Template, error) {
	if w.Template == nil {
		return rules.LegacyTemplate(w.Rule, string(w.Mode)), nil
	}
	return w.Template(job)
}

// Run processes jobs until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		ran, err := w.RunOne(ctx)
		if err != nil {
			log.Printf("worker %s: %v", w.ID, err)
		}
		if !ran {
			select {
			case <-ctx.Done():
			case <-time.After(w.Poll):
			}
		}
	}
}

// RunOne claims and processes a single job. It reports whether a job was run.
func (w *Worker) RunOne(ctx context.Context) (bool, error) {
	job, err := w.Queue.Claim(w.ID)
	if errors.Is(err, jobs.ErrNoJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	jobCtx, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	w.current, w.cancelJob, w.stopped = job.ID, cancel, false
	w.mu.Unlock()
	final, err := w.process(jobCtx, job)
	cancel()
	w.mu.Lock()
	stopped := w.stopped
	w.current, w.cancelJob, w.stopped = 0, nil, false
	w.mu.Unlock()
	if err != nil && stopped && ctx.Err() == nil {
		return true, w.Queue.Drop(job.ID)
	}
	if err != nil {
		if ctx.Err() != nil {
			// Shutting down: not the file's fault, so run it again next start.
			return true, w.Queue.Release(job.ID)
		}
		var ke *KeptError
		if errors.As(err, &ke) {
			// A safety check said no: the original is untouched, the temp file is gone.
			if kerr := w.Queue.Keep(job.ID, ke.Reason); kerr != nil {
				return true, kerr
			}
			return true, err
		}
		if ferr := w.Queue.Fail(job.ID, err.Error()); ferr != nil {
			return true, ferr
		}
		return true, err
	}
	st, _ := os.Stat(final)
	var size int64
	if st != nil {
		size = st.Size()
	}
	if err := w.Queue.Complete(job.ID, final, size); err != nil {
		return true, err
	}
	if w.OnDone != nil {
		w.OnDone(job, final)
	}
	return true, nil
}

// BesidePath is the output name in Beside mode.
func BesidePath(src string) string {
	ext := filepath.Ext(src)
	return strings.TrimSuffix(src, ext) + ".hevc" + ext
}

// process converts one file and replaces the original only if every safety check passes. The order is
// the safety story: nothing is written until the pre-checks pass; the new file is only ever a temp
// file next to the original; every check runs on that temp file; the rename is the very last step, and
// any error or "no" before it removes the temp file and leaves the original exactly as it was.
func (w *Worker) process(ctx context.Context, job jobs.Job) (final string, err error) {
	tmpl, err := w.templateFor(job)
	if err != nil {
		return "", fmt.Errorf("cannot read what to do with it: %w", err)
	}
	before, serr := statOpen(job.Path)
	if serr != nil {
		return "", fmt.Errorf("source file: %w", serr)
	}
	if err := removeLeftoverTemps(job.Path); err != nil {
		return "", err
	}
	var stage *staged
	defer func() {
		if stage != nil {
			stage.remove() // after a successful move only the empty folder is left
		}
	}()

	// What the source looks like (and so what the new file must look like).
	pctx, pcancel := context.WithTimeout(ctx, quickTimeout)
	src, perr := media.Probe(pctx, w.Exec, w.FFprobe, job.Path)
	pcancel()
	if perr != nil {
		if errors.Is(pctx.Err(), context.DeadlineExceeded) {
			return "", checkFailed(pctx, "reading the original", perr)
		}
		return "", kept("the original cannot be read any more (%v)", perr)
	}
	if err := w.preflight(job.Path, before.Size()); err != nil {
		return "", err
	}
	var packets int64
	if ord := src.MainVideoOrdinal(); ord >= 0 {
		cctx, ccancel := context.WithTimeout(ctx, countTimeout)
		n, cerr := media.CountVideoPackets(cctx, w.Exec, w.FFprobe, job.Path, ord)
		ccancel()
		if cerr != nil {
			return "", checkFailed(cctx, "counting the original's video frames", cerr)
		}
		packets = n
	}
	// The Plan for this file, from its library's flow and the probe just taken: what to run, and what
	// the result must look like. Whether to convert it was the scanner's decision (as before Plans);
	// only a flow that cannot say how to convert stops the job here.
	plan := tmpl.Compile(src)
	if plan.Video.Codec == "" {
		return "", fmt.Errorf("its library's flow cannot convert it: %s", plan.Skip)
	}
	exp := plan.Expect(src, packets)
	if exp.DurationSec <= 0 {
		exp.DurationSec = job.DurationSec
	}
	// The job keeps what it is about to do and how the result will be judged. If that cannot be
	// recorded, it does not start: nothing has been written yet.
	snap := rules.Snapshot{Plan: plan, Expect: exp, Verify: w.verification(exp)}
	b, err := json.Marshal(snap)
	if err == nil {
		err = w.Queue.RecordPlan(job.ID, string(b), snap.Fingerprint())
	}
	if err != nil {
		return "", fmt.Errorf("could not record what this conversion will do, so it did not start; the original is untouched (%v)", err)
	}

	stage, err = newStaged(job.Path)
	if err != nil {
		return "", err
	}
	tmp := stage.path
	args := plan.FFmpegArgs(job.Path, tmp)
	encErr, terr := media.Transcode(ctx, w.Exec, w.FFmpeg, args, job.DurationSec, func(p float64) {
		w.Queue.Progress(job.ID, p)
	})
	if terr != nil {
		return "", terr
	}
	// Errors ffmpeg reported while reading the original mean it is damaged: converting it would bake
	// the damage in and delete the only copy that might still be repaired or fetched again.
	if damage := sourceDamage(encErr); damage != "" {
		return "", kept("the original file has errors (%s); not converting it", damage)
	}

	// If the source changed while we encoded, the new file is stale: say so first (otherwise the
	// checks would report a confusing mismatch), and check again right before replacing.
	if err := sourceUnchanged(job.Path, before); err != nil {
		return "", err
	}
	if err := stage.intact(); err != nil {
		return "", err
	}
	if err := w.verify(ctx, src, exp, job.Path, tmp, before.Size()); err != nil {
		return "", err
	}
	// The original's permissions, only now that the file has been checked.
	if err := stage.intact(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, before.Mode().Perm()); err != nil {
		return "", fmt.Errorf("temp file permissions: %w", err)
	}
	final = job.Path
	if OutputMode(plan.Output) == Beside {
		final = BesidePath(job.Path)
	}
	// Rename is atomic on one filesystem: the original is only replaced by a complete file. Right
	// before each try, the original must still be the file we read and the temp file still the one
	// we created and checked. A plain rename cannot make that last check and the move one step, so a
	// change in the instant between them is not caught (see docs/STATUS.md).
	stillOK := func() error {
		if err := sourceUnchanged(job.Path, before); err != nil {
			return err
		}
		return stage.intact()
	}
	days := 0
	if w.KeepDays != nil {
		days = w.KeepDays()
	}
	if final == job.Path && days > 0 && w.Journal != nil {
		// The original is kept: moved into .syp-trash, journaled (keep.go).
		if err := w.keepAndReplace(ctx, job, stage, before, days, stillOK); err != nil {
			return "", err
		}
		return final, nil
	}
	if err := moveIntoPlace(ctx, tmp, final, stillOK); err != nil {
		var k *KeptError
		if errors.As(err, &k) {
			return "", err
		}
		return "", fmt.Errorf("move result into place: %w", err)
	}
	return final, nil
}

// statOpen is os.Stat through an open file, so the file's identity (device and inode, or the Windows
// file ID) is read now. On Windows, os.Stat by path reads that ID only when os.SameFile first needs it,
// from whatever the path names then: after the original was replaced, it would describe the new file
// and the check below would wrongly find them the same.
func statOpen(path string) (os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

// sourceUnchanged refuses the new file if the original is no longer the file that was read: a
// different file now (another device or inode, or Windows file ID), or the same file with another
// size, modification time or (where the system records one) change time. The change time catches
// an edit that keeps the size and puts the modification time back, as some tag editors do.
func sourceUnchanged(path string, before os.FileInfo) error {
	now, err := os.Stat(path)
	if err != nil || !os.SameFile(now, before) || now.Size() != before.Size() || !now.ModTime().Equal(before.ModTime()) {
		return kept("the original file changed while it was being converted, so the new file is out of date")
	}
	if b, ok := changeTime(before); ok {
		if n, ok := changeTime(now); !ok || !n.Equal(b) {
			return kept("the original file changed while it was being converted, so the new file is out of date")
		}
	}
	return nil
}

// moveIntoPlace renames tmp over final. On Windows the rename fails while
// another program (a media player, a virus scanner) holds the target open, so
// it is retried for a while there. Elsewhere one try is enough.
func moveIntoPlace(ctx context.Context, tmp, final string, stillOK func() error) error {
	tries := 1
	if runtime.GOOS == "windows" {
		tries = 10
	}
	return renameRetry(ctx, func(a, b string) error {
		if err := stillOK(); err != nil {
			return err
		}
		return os.Rename(a, b)
	}, tmp, final, tries, time.Second)
}

func renameRetry(ctx context.Context, rename func(a, b string) error, a, b string, tries int, wait time.Duration) error {
	var err error
	for i := 0; i < tries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err() // shutting down: stop waiting, the job goes back in the queue
			case <-time.After(wait):
			}
		}
		if err = rename(a, b); err == nil {
			return nil
		}
		var k *KeptError
		if errors.As(err, &k) {
			return err // a refusal, not a locked file: trying again will not help
		}
	}
	return err
}
