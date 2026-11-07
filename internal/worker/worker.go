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
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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
	// UserStopped turns a job the person stopped into a file skipped as user_stopped.
	UserStopped(id int64) error
	// StopRequested reports whether "Stop this file" was asked for a running job (checked before the swap).
	StopRequested(id int64) (bool, error)
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
	// Paused, when it says so, makes the worker take no new job (Pause all, slice 24).
	Paused func() bool
	// StallAfter: an encode with no progress for this long is stopped and fails (0: DefaultStallAfter).
	StallAfter time.Duration
	// KeepDays is how many days a replaced original is kept in .syp-trash (keep.go); 0 or nil: it is
	// deleted by the rename, as before slice 17. Journal records those swaps (needed when keeping).
	KeepDays func() int
	Journal  Journal
	Now      func() time.Time // nil: time.Now

	mu         sync.Mutex
	filterMemo map[string]bool
	current    int64              // the running job's id (0: none)
	cancelJob  context.CancelFunc // stops only the running job
	stopped    stopKind           // why the running job was stopped (stopNone: it was not)
	progressAt atomic.Int64       // UnixNano of the running encode's last progress (stall watchdog)
}

// stopKind is why a running job was stopped, which decides what becomes of it.
type stopKind int

const (
	stopNone  stopKind = iota
	stopPause          // Pause all, or its library being removed: back to the queue
	stopUser           // Stop this file: skipped, reason user_stopped
	stopStall          // no progress for StallAfter: failed
)

// DefaultStallAfter is how long an encode may go without progress before the watchdog stops it.
const DefaultStallAfter = 10 * time.Minute

// RequeueJob stops job id if this worker is running it and puts it back in the queue, as a pause does:
// the original is left untouched and the temp files removed (its library is being removed, which then
// deletes the waiting job). One already inside its swap finishes. It does not wait.
func (w *Worker) RequeueJob(id int64) { w.stop(id, stopPause) }

// StopFile stops job id if this worker is running it ("Stop this file"): the original is untouched,
// and the file is recorded as stopped by the person. It reports whether this worker was running it.
func (w *Worker) StopFile(id int64) bool { return w.stop(id, stopUser) }

// PauseNow stops this worker's running job, if any, and puts it back in the queue (Pause all; the
// worker takes no new job while Paused says so).
func (w *Worker) PauseNow() {
	w.mu.Lock()
	id := w.current
	w.mu.Unlock()
	if id != 0 {
		w.stop(id, stopPause)
	}
}

func (w *Worker) busy() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.current != 0
}

func (w *Worker) stop(id int64, why stopKind) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.current == id && w.cancelJob != nil {
		if w.stopped == stopNone {
			w.stopped = why
		}
		w.cancelJob()
		return true
	}
	return false
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
	// The claim and this worker's "current" are set under its lock, so a pause or a stop that looks for
	// the job waits for the claim and finds it: there is no moment when a job is claimed but not
	// stoppable. (The claim itself takes no job while paused or once its stop was requested.)
	w.mu.Lock()
	if w.Paused != nil && w.Paused() {
		w.mu.Unlock()
		return false, nil
	}
	job, err := w.Queue.Claim(w.ID)
	if err != nil {
		w.mu.Unlock()
		if errors.Is(err, jobs.ErrNoJob) {
			return false, nil
		}
		return false, err
	}
	jobCtx, cancel := context.WithCancel(ctx)
	w.current, w.cancelJob, w.stopped = job.ID, cancel, stopNone
	w.mu.Unlock()
	log.Printf("worker %s: job %d: started", w.ID, job.ID)
	if testHook != nil {
		testHook("registered")
	}
	var final string
	if w.Paused != nil && w.Paused() { // a pause that landed after the check above: nothing has started
		w.stop(job.ID, stopPause)
		err = context.Canceled
	} else {
		final, err = w.process(jobCtx, job)
	}
	cancel()
	w.mu.Lock()
	stopped := w.stopped
	w.mu.Unlock()
	// The job stays this worker's "current" until its outcome is recorded (deferred), so a stop in the
	// meantime is still answered by this worker.
	defer func() {
		w.mu.Lock()
		w.current, w.cancelJob, w.stopped = 0, nil, stopNone
		w.mu.Unlock()
	}()
	// A stop asked for on the job's record wins over a pause or a shutdown, so it never waits in the
	// queue as a job no claim takes. Once the new file is in place (err == nil) the job is done.
	if err != nil {
		if req, rerr := w.Queue.StopRequested(job.ID); rerr == nil && req {
			stopped = stopUser
		}
	}
	if err != nil && stopped == stopUser {
		log.Printf("worker %s: job %d: stopped by the person, original untouched", w.ID, job.ID)
		return true, w.Queue.UserStopped(job.ID)
	}
	// A stop that came after the new file was in place changes nothing: the job is done (below).
	if err != nil && stopped != stopNone && ctx.Err() == nil {
		switch stopped {
		case stopPause:
			log.Printf("worker %s: job %d: paused, back in the queue", w.ID, job.ID)
			return true, w.Queue.Release(job.ID)
		case stopUser:
			return true, w.Queue.UserStopped(job.ID)
		case stopStall:
			return true, w.Queue.Fail(job.ID, fmt.Sprintf("Stopped: no progress for %s. The original is untouched; press Retry to try again.", w.stallAfter()))
		}
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
		// The person sees what happened in plain words; the technical error goes to the log
		// (Settings > System shows its last lines).
		log.Printf("worker %s: job %d (%s) failed: %v", w.ID, job.ID, job.Path, err)
		if ferr := w.Queue.Fail(job.ID, plainFailure(err)); ferr != nil {
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

// FailedError is a failed conversion: Plain is what the person sees (COPY-1/COPY-2: what happened,
// what was not touched, what to do), Err the technical cause, for the log.
type FailedError struct {
	Plain string
	Err   error
}

func (e *FailedError) Error() string { return e.Plain + " (" + e.Err.Error() + ")" }
func (e *FailedError) Unwrap() error { return e.Err }

func failed(plain string, err error) error { return &FailedError{plain, err} }

const cannotRead = "shrink-your-shit is not allowed to read this file, so nothing was converted. Let it read the file (Settings > System says how), then press Retry."

const diskFull = "The disk is full, so converting stopped. Your original was not touched. Free some space, then press Retry."

// plainFailure is the plain words for a failed conversion: a full disk and a folder it may not write
// in are said as such; a FailedError says its own; anything else gets the general sentence.
func plainFailure(err error) string {
	var fe *FailedError
	switch {
	case errors.As(err, &fe): // a stage that says its own, more precisely
		return fe.Plain
	case errors.Is(err, syscall.ENOSPC):
		return diskFull
	case errors.Is(err, fs.ErrPermission):
		return "shrink-your-shit is not allowed to write in this folder, so converting stopped. Your original was not touched. Let it write there (Settings > System says how), then press Retry."
	}
	return "Converting did not finish. Your original was not touched. Press Retry; if it keeps failing, Settings > System shows what went wrong."
}

// errIsLink: the file is a link (or another reparse point), which is never followed (slice 23).
var errIsLink = errors.New("the file is a link")

func errorIs(err, target error) bool { return errors.Is(err, target) }

// errStopRequested: "Stop this file" was recorded on the job before its swap; nothing was replaced.
var errStopRequested = errors.New("stopped by the person before the new file was put in place")

// testHook, if set, is called at named points of RunOne ("registered"), so tests can land a pause or a
// stop exactly there.
var testHook func(at string)

func (w *Worker) stallAfter() time.Duration {
	if w.StallAfter > 0 {
		return w.StallAfter
	}
	return DefaultStallAfter
}

// watchStall stops job id (stopStall) if its encode reports no progress for stallAfter. The returned
// func ends the watch. Checks after the encode have their own time limits (slice 10).
func (w *Worker) watchStall(id int64) func() {
	w.progressAt.Store(time.Now().UnixNano())
	after := w.stallAfter()
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(max(after/20, 10*time.Millisecond))
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if time.Since(time.Unix(0, w.progressAt.Load())) >= after {
					log.Printf("worker %s: job %d: no progress for %s, stopping it", w.ID, id, after)
					w.stop(id, stopStall)
					return
				}
			}
		}
	}()
	return func() { close(done) }
}

// BesidePath is the output name in Beside mode.
func BesidePath(src string) string { return BesidePathAs(src, filepath.Ext(src)) }

// BesidePathAs is the output name in Beside mode for a new file of type ext (".mkv"): name.hevc.mkv.
func BesidePathAs(src, ext string) string {
	return strings.TrimSuffix(src, filepath.Ext(src)) + ".hevc" + ext
}

// process converts one file and replaces the original only if every safety check passes. The order is
// the safety story: nothing is written until the pre-checks pass; the new file is only ever a temp
// file next to the original; every check runs on that temp file; the rename is the very last step, and
// any error or "no" before it removes the temp file and leaves the original exactly as it was.
func (w *Worker) process(ctx context.Context, job jobs.Job) (final string, err error) {
	tmpl, err := w.templateFor(job)
	if err != nil {
		return "", failed("This library's setting could not be read, so converting did not start. Your original was not touched. Open the library and save its setting again, then press Retry.", err)
	}
	// A link is never followed: checked before opening, and the open itself does not follow one (slice 23).
	if lst, err := os.Lstat(job.Path); err == nil && lst.Mode()&fs.ModeSymlink != 0 {
		return "", kept("%s", rules.SkipWords[rules.SkipSymlink])
	}
	before, serr := statOpen(job.Path)
	if errors.Is(serr, errIsLink) {
		return "", kept("%s", rules.SkipWords[rules.SkipSymlink])
	}
	if serr != nil {
		if errors.Is(serr, fs.ErrNotExist) {
			return "", failed("The file is not there any more, so nothing was converted.", serr)
		}
		if errors.Is(serr, fs.ErrPermission) { // not a write: the generic permission words would point the wrong way
			return "", failed(cannotRead, serr)
		}
		return "", fmt.Errorf("source file: %w", serr)
	}
	// The file as the system keeps it (on Windows read through its handle, with its change time, which
	// os.FileInfo does not carry there): compared again before the swap (review 86970d8).
	beforeState, err := readSourceState(job.Path, before)
	if err != nil {
		return "", failed(cannotRead, err)
	}
	// The file guards again (slice 23; the scanner decided earlier, and the file may have gained another
	// name since): a shared file is left alone. sourceUnchanged checks it again right before the swap.
	if media.LinkCount(job.Path, before) > 1 {
		return "", kept("%s", rules.SkipWords[rules.SkipSharedFile])
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
		log.Printf("worker %s: %s: %v", w.ID, job.Path, perr)
		return "", kept("the original can't be read any more, so it was left as it is")
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
		return "", failed("This library's setting can't convert this file, so it was left as it is.", fmt.Errorf("its library's flow cannot convert it: %s", plan.Skip))
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
		return "", failed("What this conversion will do could not be recorded, so it did not start. Your original was not touched. Press Retry.", err)
	}

	ext := filepath.Ext(job.Path)
	if plan.Container != "" {
		if OutputMode(plan.Output) != Beside { // refused when saved (25a); a flow made otherwise stops here
			return "", failed("This library's setting changes the file type while replacing the original, which this version can't do yet. Your original was not touched. Choose \"Put the new file next to the original\" or keep the file type, then press Retry.",
				fmt.Errorf("container %s with output %s", plan.Container, plan.Output))
		}
		ext = "." + plan.Container
	}
	if OutputMode(plan.Output) == Beside { // the name is taken already: no point converting (checked again at the end)
		if _, err := os.Lstat(BesidePathAs(job.Path, ext)); err == nil {
			return "", failed(fmt.Sprintf("There is already a file named %s next to the original, so the new file was not made. Nothing was changed. Move or rename that file, then press Retry.", filepath.Base(BesidePathAs(job.Path, ext))), fs.ErrExist)
		}
	}
	stage, err = newStaged(job.Path, ext)
	if err != nil {
		return "", err
	}
	tmp := stage.path
	args := plan.FFmpegArgs(job.Path, tmp)
	stopWatch := w.watchStall(job.ID)
	encErr, terr := media.Transcode(ctx, w.Exec, w.FFmpeg, args, job.DurationSec, func(p float64) {
		w.progressAt.Store(time.Now().UnixNano())
		w.Queue.Progress(job.ID, p)
	})
	stopWatch()
	if terr != nil {
		if errors.Is(terr, context.Canceled) || ctx.Err() != nil {
			return "", terr // stopped or shutting down: RunOne decides, not a failure of the file
		}
		if errors.Is(terr, syscall.ENOSPC) || strings.Contains(encErr+terr.Error(), "No space left on device") {
			return "", failed(diskFull, terr)
		}
		return "", failed("Converting stopped with an error. Your original was not touched. Press Retry; if it keeps failing, the file may be damaged.", fmt.Errorf("%w: %s", terr, firstLines(encErr, 3)))
	}
	// Errors ffmpeg reported while reading the original mean it is damaged: converting it would bake
	// the damage in and delete the only copy that might still be repaired or fetched again.
	if damage := sourceDamage(encErr); damage != "" {
		log.Printf("worker %s: %s: the original has errors: %s", w.ID, job.Path, damage)
		return "", kept("the original file has errors, so it was not converted (converting would keep the damage)")
	}

	// If the source changed while we encoded, the new file is stale: say so first (otherwise the
	// checks would report a confusing mismatch), and check again right before replacing.
	if err := sourceUnchanged(job.Path, before, beforeState); err != nil {
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
	// Its modification time and owner too (slice 23), where the system allows: media servers and
	// backups key on the time, and the file stays the user's. Set before the swap, so the identity the
	// swap records is the one the file keeps.
	if err := os.Chtimes(tmp, time.Now(), before.ModTime()); err != nil {
		log.Printf("worker %s: job %d: could not keep the modification time: %v", w.ID, job.ID, err)
	}
	if err := keepOwner(tmp, before); err != nil {
		log.Printf("worker %s: job %d: could not keep the owner: %v", w.ID, job.ID, err)
	}
	final = job.Path
	if OutputMode(plan.Output) == Beside {
		final = BesidePathAs(job.Path, ext)
	}
	// Rename is atomic on one filesystem: the original is only replaced by a complete file. Right
	// before each try, the original must still be the file we read and the temp file still the one
	// we created and checked. A plain rename cannot make that last check and the move one step, so a
	// change in the instant between them is not caught (see docs/STATUS.md).
	// From here the swap runs to its end even if the job is stopped meanwhile (it takes milliseconds,
	// or a few seconds of retries on Windows): a stop never leaves half a swap, and the job is done.
	// The last moment a stop can still prevent the swap: one that cancelled the job, or one recorded on
	// the job (it may have reached no worker).
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if req, err := w.Queue.StopRequested(job.ID); err != nil {
		return "", fmt.Errorf("checking for a stop: %w", err)
	} else if req {
		return "", errStopRequested
	}
	ctx = context.WithoutCancel(ctx)
	if err := hook("before swap"); err != nil {
		return "", err
	}
	stillOK := func() error {
		if err := sourceUnchanged(job.Path, before, beforeState); err != nil {
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
	move := moveIntoPlace
	if OutputMode(plan.Output) == Beside {
		move = moveBeside // "Put the new file next to the original": never over another file
	}
	if err := move(ctx, tmp, final, stillOK); err != nil {
		var f *FailedError
		if errors.As(err, &f) {
			return "", err
		}
		var k *KeptError
		if errors.As(err, &k) {
			return "", err
		}
		return "", failed("The new file could not be put in place, so your original was kept as it was. Press Retry.", fmt.Errorf("move result into place: %w", err))
	}
	return final, nil
}

// statOpen is os.Stat through an open file, so the file's identity (device and inode, or the Windows
// file ID) is read now. On Windows, os.Stat by path reads that ID only when os.SameFile first needs it,
// from whatever the path names then: after the original was replaced, it would describe the new file
// and the check below would wrongly find them the same.
func statOpen(path string) (os.FileInfo, error) {
	f, err := openNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

// readSourceState is media.FileState; tests change what it reports.
var readSourceState = media.FileState

// sourceUnchanged refuses the new file if the original is no longer the file that was read: a
// different file now (another device or inode, or Windows file ID), or the same file with another
// size, modification time or (where the system records one) change time. The change time catches
// an edit that keeps the size and puts the modification time back, as some tag editors do.
func sourceUnchanged(path string, before os.FileInfo, beforeState media.State) error {
	now, err := os.Stat(path)
	if err != nil || !os.SameFile(now, before) || now.Size() != before.Size() || !now.ModTime().Equal(before.ModTime()) {
		return kept("the original file changed while it was being converted, so the new file is out of date")
	}
	// media.FileState too: on Windows it carries the change time, which the Stat above does not (an edit
	// that keeps the size and puts the modification time back); unreadable now counts as changed
	if st, err := readSourceState(path, now); err != nil || !st.Same(beforeState) {
		return kept("the original file changed while it was being converted, so the new file is out of date")
	}
	if b, ok := media.ChangeTime(before); ok {
		if n, ok := media.ChangeTime(now); !ok || !n.Equal(b) {
			return kept("the original file changed while it was being converted, so the new file is out of date")
		}
	}
	if media.LinkCount(path, now) > 1 { // another name appeared while converting (Windows has no ctime to see it)
		return kept("%s", rules.SkipWords[rules.SkipSharedFile])
	}
	return nil
}

// moveIntoPlace renames tmp over final. On Windows the rename fails while
// another program (a media player, a virus scanner) holds the target open, so
// it is retried for a while there. Elsewhere one try is enough.
// moveBeside is moveIntoPlace for the new file next to the original: it never replaces a file already
// at that name, one that was there before or one that appeared while converting (renameNoReplace, slice
// 18). Then nothing is moved, and the job fails with a plain reason.
func moveBeside(ctx context.Context, tmp, final string, stillOK func() error) error {
	tries := 1
	if runtime.GOOS == "windows" {
		tries = 10
	}
	err := renameRetry(ctx, func(a, b string) error {
		if err := stillOK(); err != nil {
			return err
		}
		if err := renameNoReplace(a, b); errors.Is(err, fs.ErrExist) {
			return failed(fmt.Sprintf("There is already a file named %s next to the original, so the new file was not put there. Nothing was changed. Move or rename that file, then press Retry.", filepath.Base(b)), err)
		} else {
			return err
		}
	}, tmp, final, tries, time.Second)
	return err
}

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
		var f *FailedError
		if errors.As(err, &k) || errors.As(err, &f) {
			return err // a refusal (or the name is taken), not a locked file: trying again will not help
		}
	}
	return err
}
