// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package worker runs transcode jobs. It only talks to the Queue interface,
// so a remote worker (HTTP pull) can implement Queue later; the built-in
// worker is just one instance using the local store.
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/shrink-your-shit/shrink-your-shit/internal/runlog"
)

// Queue is everything a worker needs from the job store.
type Queue interface {
	Claim(worker string) (jobs.Job, error)
	Progress(id int64, pct float64) error
	Complete(id int64, outputPath string, sizeAfter int64) error
	Fail(id int64, code, msg string) error
	// Release puts a running job back in the queue (used on shutdown).
	Release(id int64) error
	// UserStopped turns a job the person stopped into a file skipped as user_stopped.
	UserStopped(id int64) error
	// StopRequested reports whether "Stop this file" was asked for a running job (checked before the swap).
	StopRequested(id int64) (bool, error)
	// Keep marks a job "kept original": a safety check refused the result (reason says why).
	Keep(id int64, code, reason string) error
	// RetryLater puts a job whose failure may pass back in the queue, not to be taken before at (DD-2).
	RetryLater(id int64, at time.Time, code, reason string) error
	// OutputFolder is a library's folder for new files with "Next to each library" (V5a), or why it has
	// none.
	OutputFolder(libraryID int64, libPath string) (string, *jobs.FolderProblem, error)
	// IsOwnOutput reports whether the file at path (this size and time) is one a done job wrote.
	IsOwnOutput(path string, size, mtime int64) (bool, error)
	// RecordPlan stores the Plan a running job converts with (JSON) and its fingerprint.
	RecordPlan(id int64, plan, fingerprint string) error
	// Skip ends a job as a skipped file with words and a reason code (S3: Dolby Vision left alone).
	Skip(id int64, words, code string) error
	// AddStep adds a line to a job's step summary (S5): converted, verified, kept, replaced.
	AddStep(id int64, step string, ok bool, words string) error
	// PutProbe stores a file's probe (JSON) for its size and time: the new file's, for the Files view.
	PutProbe(path string, size, mtime int64, info string) error
}

// OutputMode says what happens to the result.
type OutputMode string

const (
	// Replace swaps the original for the new file, only after the new file is complete.
	Replace OutputMode = "replace"
	// Beside keeps the original and writes name.hevc.ext next to it.
	Beside OutputMode = "beside"
	// Folder (S6) keeps the original where it is and writes the new file to another folder, in the same
	// sub-folders as in its library, under the same name (its new type's extension).
	Folder OutputMode = "folder"
)

// Worker pulls jobs from a Queue and runs ffmpeg.
type Worker struct {
	// Logs keeps each job's run log (S5); nil: none.
	Logs *runlog.Logs
	// verified is the probe of the new file the last check passed (S5: stored for its final path).
	verified media.Info
	ID       string
	Queue    Queue
	Exec     media.Exec
	FFmpeg   string
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
	// Sidecars records .srt files before they are put beside a new file (slice 25b); nil: not recorded
	// (they are still only removed while they are ours).
	Sidecars SidecarJournal
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
	w.jobf(job.ID, ": started")
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
		w.jobf(job.ID, ": stopped by the person, original untouched")
		return true, w.Queue.UserStopped(job.ID)
	}
	// A stop that came after the new file was in place changes nothing: the job is done (below).
	if err != nil && stopped != stopNone && ctx.Err() == nil {
		switch stopped {
		case stopPause:
			w.jobf(job.ID, ": paused, back in the queue")
			return true, w.Queue.Release(job.ID)
		case stopUser:
			return true, w.Queue.UserStopped(job.ID)
		case stopStall:
			return true, w.Queue.Fail(job.ID, jobs.ReasonStalled, fmt.Sprintf("Stopped: no progress for %s. The original is untouched; press Retry to try again.", w.stallAfter()))
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			// Shutting down: not the file's fault, so run it again next start.
			return true, w.Queue.Release(job.ID)
		}
		var se *SkippedError
		if errors.As(err, &se) { // left alone by its flow (S3): Skipped, not failed, never retried
			w.jobf(job.ID, " (%s): skipped: %s", job.Path, se.Words)
			return true, w.Queue.Skip(job.ID, se.Words, se.Code)
		}
		// A failure that may pass, with a try left: back in the queue for later, from the start (DD-2).
		if wait, ok := retryIn(err, job.Attempts); ok {
			w.jobf(job.ID, " (%s): try %d of %d failed, trying again in %s: %v", job.Path, job.Attempts+1, maxTries, wait, err)
			return true, w.Queue.RetryLater(job.ID, w.now().Add(wait), reasonCode(err), plainReason(err))
		}
		var ke *KeptError
		if errors.As(err, &ke) {
			// A safety check said no: the original is untouched, the temp file is gone.
			w.jobf(job.ID, ": a check said no, the original is kept: %s", ke.Reason)
			w.Queue.AddStep(job.ID, "verified", false, ke.Reason)
			if kerr := w.Queue.Keep(job.ID, reasonCode(err), ke.Reason); kerr != nil {
				return true, kerr
			}
			return true, err
		}
		// The person sees what happened in plain words; the technical error goes to the log
		// (Settings > System shows its last lines).
		w.jobf(job.ID, " (%s) failed: %v", job.Path, err)
		w.Queue.AddStep(job.ID, "converted", false, plainFailure(err))
		if ferr := w.Queue.Fail(job.ID, reasonCode(err), plainFailure(err)); ferr != nil {
			return true, ferr
		}
		return true, err
	}
	st, _ := os.Stat(final)
	var size int64
	if st != nil {
		size = st.Size()
	}
	if st != nil && w.verified.Video() != nil { // the new file's codec, size and length for the Files view (S5)
		if b, err := json.Marshal(w.verified); err == nil {
			w.Queue.PutProbe(final, st.Size(), st.ModTime().UnixNano(), string(b))
		}
	}
	if err := w.Queue.Complete(job.ID, final, size); err != nil {
		return true, err
	}
	// before and after, also when the new file is not smaller (it is kept: always standardize)
	w.jobf(job.ID, ": done, %s: %s -> %s", final, rules.HumanBytes(job.SizeBefore), rules.HumanBytes(size))
	w.Queue.AddStep(job.ID, "verified", true, "Every check passed: the new file is complete, plays cleanly and looks like the original.")
	if filepath.Dir(final) != filepath.Dir(job.Path) { // "Save to another folder" (S6)
		w.Queue.AddStep(job.ID, "kept", true, "The new file was written to "+filepath.Dir(final)+"; the original stays where it is.")
	} else if w.Mode == Beside {
		w.Queue.AddStep(job.ID, "kept", true, "The new file was written next to the original, which stays as it is.")
	} else {
		w.Queue.AddStep(job.ID, "replaced", true, fmt.Sprintf("The new file is in place: %s to %s.", rules.HumanBytes(job.SizeBefore), rules.HumanBytes(size)))
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

// jobf logs a line about job id, to the program's log and to the job's own run log (S5).
func (w *Worker) jobf(id int64, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	log.Printf("worker %s: job %d%s", w.ID, id, msg)
	if w.Logs != nil {
		w.Logs.Append(id, w.now().UTC().Format("15:04:05")+" "+strings.TrimPrefix(strings.TrimPrefix(msg, ":"), " "))
	}
}

// SkippedError: the file is not converted because its flow leaves it alone (S3's Dolby Vision skips);
// the job ends as a skipped file with these words and code.
type SkippedError struct{ Words, Code string }

func (e *SkippedError) Error() string { return "skipped: " + e.Words }

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

// reasonCode is the code of a failed or refused conversion (one of jobs.JobReasonCodes), beside its
// plain words (plainReason): the same cases, in the same order.
func reasonCode(err error) string {
	var ke *KeptError
	var fe *FailedError
	switch {
	case errors.As(err, &ke):
		return ke.Code
	case errors.As(err, &fe) && fe.Plain == diskFull:
		return jobs.ReasonDiskFull
	case errors.As(err, &fe) && fe.Plain == cannotRead:
		return jobs.ReasonCannotRead
	case errors.As(err, &fe):
		return jobs.ReasonConvertFailed
	case errors.Is(err, syscall.ENOSPC):
		return jobs.ReasonDiskFull
	case errors.Is(err, fs.ErrPermission):
		return jobs.ReasonCannotWrite
	}
	return jobs.ReasonConvertFailed
}

// plainReason is the plain words of a failed or refused conversion.
func plainReason(err error) string {
	var ke *KeptError
	if errors.As(err, &ke) {
		return ke.Reason
	}
	return plainFailure(err)
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
					w.jobf(id, ": no progress for %s, stopping it", after)
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
	// An earlier try of this job may have left .srt files it could not remove (they are on record): they
	// go first, on every try and whatever the setting is now, each only if it is still exactly ours, so a
	// try never takes its own file for someone else's and none is left behind. One that still cannot be
	// removed makes this try one to repeat later (DD-2, reviews 222be8f, 13fb51f).
	if err := w.clearOwnSidecars(job.ID); err != nil {
		return "", retryable(err, retryGeneral)
	}
	tmpl, err := w.templateFor(job)
	if err != nil {
		ferr := failed("This library's setting could not be read, so converting did not start. Your original was not touched. Open the library and save its setting again, then press Retry.", err)
		if errors.Is(err, jobs.ErrLibraryGone) || errors.Is(err, jobs.ErrNotFound) { // gone: trying again changes nothing
			return "", ferr
		}
		return "", retryable(ferr, retryGeneral) // the database was busy
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
	// Dolby Vision the flow leaves alone is never encoded, whatever was decided when the job was queued
	// (review a09f6f5: a job queued before migration 17 or a setting change). Only these skips: the
	// others stay the scanner's decision, as before Plans.
	if words, code := tmpl.DolbyVisionHold(src); code != "" { // from the probe, not the Plan's skip (review 7ab8f72)
		return "", &SkippedError{Words: words, Code: code}
	}
	// Any skip of the Plan made now (the flow changed since the job was queued: an HDR file with a
	// non-HEVC target, a choice that can't run...) ends the job as a skipped file with that reason,
	// never an encode (review 6cafc6f: the second such case after Dolby Vision). The scanner's rules
	// for what is queued are unchanged.
	if plan.Skip != "" {
		code := plan.SkipCode
		if code == "" {
			code = rules.SkipSettingCannotRun
		}
		return "", &SkippedError{Words: plan.Skip, Code: code}
	}
	if plan.Video.Codec == "" {
		return "", failed("This library's setting can't convert this file, so it was left as it is.", fmt.Errorf("its library's flow cannot convert it: %s", plan.Skip))
	}
	// What the conversion will do, logged only now that it runs (review 48f07eb: a skipped Plan
	// converts nothing, so its notes would be untrue).
	if plan.MainAudioUsed { // Basic's stereo AAC found no track in the person's languages (S4)
		w.jobf(job.ID, " (%s): %s", job.Path, rules.NoAudioInMyLanguageMain)
	}
	if plan.TypeNote != "" { // "keep" made this MP4 an MKV (review 4ac9c0d)
		w.jobf(job.ID, " (%s): %s", job.Path, plan.TypeNote)
	}
	if plan.SmallKept { // design, 02:26Z: once per file
		w.jobf(job.ID, " (%s): %s", job.Path, rules.SmallKeptNote)
	}
	if plan.AudioFolded { // design, 01:52Z: once per file
		w.jobf(job.ID, " (%s): %s", job.Path, rules.FoldedNote)
	}
	if plan.StyledSubsPlain { // design, 02:49Z: once per file
		w.jobf(job.ID, " (%s): %s", job.Path, rules.StyledSubsNote)
	}
	if plan.HDRNote != "" { // what the HDR conversion does not keep (S3)
		w.jobf(job.ID, " (%s): %s", job.Path, plan.HDRNote)
	}
	exp := plan.Expect(src, packets)
	if exp.DurationSec <= 0 {
		exp.DurationSec = job.DurationSec
	}
	// The job keeps what it is about to do and how the result will be judged. If that cannot be
	// recorded, it does not start: nothing has been written yet.
	snap := rules.Snapshot{Plan: plan, Expect: exp, Verify: w.verification(exp), Source: &src}
	b, err := json.Marshal(snap)
	if err == nil {
		err = w.Queue.RecordPlan(job.ID, string(b), snap.Fingerprint())
	}
	if err != nil {
		return "", retryable(failed("What this conversion will do could not be recorded, so it did not start. Your original was not touched. Press Retry.", err), retryGeneral)
	}

	ext := filepath.Ext(job.Path)
	if plan.Container != "" {
		ext = "." + plan.Container
	}
	// Where the new file goes: beside the original, at its name, or, in replace mode with another file
	// type, at its name with the new extension (25a-2).
	newName := rules.ReplaceName(job.Path, ext)
	switch OutputMode(plan.Output) {
	case Beside:
		newName = BesidePathAs(job.Path, ext)
	case Folder:
		dir := plan.OutputDir
		if plan.FolderMode == rules.NextToLibrary { // V5a: the library's own folder beside it, or its override
			d, problem, err := w.Queue.OutputFolder(job.LibraryID, job.Library)
			if err != nil {
				return "", retryable(failed("This library's folder for new files could not be read, so converting did not start. Your original was not touched. Press Retry.", err), retryGeneral)
			}
			if problem != nil {
				return "", failed(problem.Message+" Your original was not touched.", errors.New(problem.Code))
			}
			dir = d
		}
		newName = FolderPath(dir, job.Library, job.Path, ext)
		if err := os.MkdirAll(filepath.Dir(newName), 0o755); err != nil {
			return "", failed("The folder for new files can't be written to, so nothing was converted. Your original was not touched. Check the flow's folder, then press Retry.", err)
		}
		if err := removeLeftoverTemps(newName); err != nil { // an earlier run's, staged there (review-697332b P2)
			return "", err
		}
		if err := w.preflight(newName, before.Size()); err != nil { // its own disk: the new file is made there (review 6cafc6f)
			return "", err
		}
	}
	if keepsOriginal(plan) { // the name is taken already: no point converting (checked again at the end)
		if OutputMode(plan.Output) == Folder {
			// our own earlier output may be there (the original changed and is converted again): it is
			// replaced once the new file passes its checks; a file we didn't make never is (design 01:04Z)
			if lst, err := os.Lstat(newName); err == nil && !w.ownOutput(newName, lst) {
				return "", failed(notOurs(newName), fs.ErrExist)
			}
		} else if _, err := os.Lstat(newName); err == nil {
			return "", failed(fmt.Sprintf("There is already a file named %s next to the original, so the new file was not made. Nothing was changed. Move or rename that file, then press Retry.", filepath.Base(newName)), fs.ErrExist)
		}
	} else if newName != job.Path && exists(newName) { // a renaming replace never replaces another file
		return "", failed(rules.NameTaken(newName), fs.ErrExist)
	}
	// Subtitles saved as .srt files go beside the new file and never over another file: a name taken
	// already stops the job before anything is converted (checked again when they are put in place).
	for _, o := range plan.SubtitleFiles() {
		if name := rules.SrtName(newName, o.Extract); exists(name) {
			return "", failed(srtTaken(name), fs.ErrExist)
		}
	}
	stageNear := job.Path
	if OutputMode(plan.Output) == Folder {
		stageNear = newName // staged in the folder for new files: the last step is a rename on its disk
	}
	stage, err = newStaged(stageNear, ext)
	if err != nil {
		return "", err
	}
	tmp := stage.path
	args := plan.FFmpegArgs(job.Path, tmp)
	stopWatch := w.watchStall(job.ID)
	setLive(job.ID, LiveJob{Worker: w.ID, Started: w.now(), ConvertingTo: convertingTo(plan, src), Encoder: encoderWords(plan),
		Decoder: decoderWords(src), Progress: media.Progress{ETASeconds: -1}})
	defer dropLive(job.ID)
	var tenths progressLines
	encErr, terr := media.Transcode(ctx, w.Exec, w.FFmpeg, args, job.DurationSec, func(p media.Progress) {
		w.progressAt.Store(time.Now().UnixNano())
		w.Queue.Progress(job.ID, p.Percent)
		updateLive(job.ID, p)
		if line := tenths.next(p); line != "" {
			w.jobf(job.ID, ": %s", line)
		}
	})
	stopWatch()
	if s := strings.TrimSpace(encErr); s != "" && w.Logs != nil {
		w.Logs.Append(job.ID, "ffmpeg said:\n"+s) // errors only (the encode runs at -v error)
	}
	if terr == nil {
		w.Queue.AddStep(job.ID, "converted", true, "Converted: "+convertingTo(plan, src)+".")
	}
	if terr != nil {
		if errors.Is(terr, context.Canceled) || ctx.Err() != nil {
			return "", terr // stopped or shutting down: RunOne decides, not a failure of the file
		}
		if errors.Is(terr, syscall.ENOSPC) || strings.Contains(encErr+terr.Error(), "No space left on device") {
			return "", retryable(failed(diskFull, terr), retrySpace)
		}
		ferr := failed("Converting stopped with an error. Your original was not touched. Press Retry; if it keeps failing, the file may be damaged.", fmt.Errorf("%w: %s", terr, firstLines(encErr, 3)))
		if killedBySignal(terr) { // stopped by the system (out of memory...), not a problem of the file
			return "", retryable(ferr, retryGeneral)
		}
		return "", ferr
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
	for k := range plan.SubtitleFiles() { // each subtitle that was to be saved as a file was saved
		if st, err := os.Lstat(rules.StagedSrt(tmp, k)); err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
			return "", kept("a subtitle could not be saved as a separate file")
		}
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
		w.jobf(job.ID, ": could not keep the modification time: %v", err)
	}
	if err := keepOwner(tmp, before); err != nil {
		w.jobf(job.ID, ": could not keep the owner: %v", err)
	}
	final = newName
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
		return "", retryable(fmt.Errorf("checking for a stop: %w", err), retryGeneral) // nothing replaced yet
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
	placed, err := w.installSubtitles(plan, job.ID, tmp, final)
	if err != nil {
		return "", err
	}
	if len(placed) > 0 {
		if err := hook("subtitles placed"); err != nil {
			return "", err // a crash here: RecoverSidecars removes them at the next start
		}
	}
	removePlaced := func() { // the swap did not happen: the .srt files we put beside it go again
		w.removeSidecars(job.ID, placed)
	}
	swapped := func() { // the swap is done: the .srt files stay; their record goes
		if w.Sidecars != nil && len(placed) > 0 {
			w.Sidecars.DropSidecars(job.ID)
		}
	}
	days := 0
	if w.KeepDays != nil {
		days = w.KeepDays()
	}
	renaming := !keepsOriginal(plan) && final != job.Path
	if renaming && w.Journal == nil {
		return "", failed("This setting changes the file type, which needs the record of kept originals, and it is not available here. Your original was not touched.", errors.New("no journal"))
	}
	if (final == job.Path && days > 0 || renaming) && w.Journal != nil {
		// The original is kept: moved into .syp-trash, journaled (keep.go). A change of name always is
		// (also with 0 days: then it is deleted right after), so no step of it is ever unrecorded.
		if err := w.keepAndReplace(ctx, job, stage, before, final, days, stillOK); err != nil {
			removePlaced()
			return "", err
		}
		swapped()
		return final, nil
	}
	move := moveIntoPlace
	switch {
	case OutputMode(plan.Output) == Folder:
		move = w.moveIntoFolder // never over a file we didn't make
	case keepsOriginal(plan):
		move = moveBeside // never over another file
	}
	if err := move(ctx, tmp, final, stillOK); err != nil {
		removePlaced()
		var f *FailedError
		if errors.As(err, &f) {
			return "", err
		}
		var k *KeptError
		if errors.As(err, &k) {
			return "", err
		}
		return "", retryable(failed("The new file could not be put in place, so your original was kept as it was. Press Retry.", fmt.Errorf("move result into place: %w", err)), retryGeneral) // busy or locked
	}
	swapped()
	return final, nil
}

// SidecarJournal records subtitles saved as .srt files before they are put beside a new file
// (jobs.Store), so a crash in between leaves nothing of ours behind (RecoverSidecars).
type SidecarJournal interface {
	AddSidecars([]jobs.Sidecar) error
	DropSidecars(jobID int64) error
	DropSidecar(jobID int64, path string) error
	Sidecars() ([]jobs.Sidecar, error)
}

// placeSidecar is renameNoReplace; tests make it fail after placing the file.
var placeSidecar = renameNoReplace

// installSubtitles puts the Plan's extracted subtitles (written next to tmp) beside final, never over
// another file (renameNoReplace). Each is recorded first, with what it is and what the new video is, so
// only our own file is ever removed again (here if a name is taken or the swap fails, or at the next
// start after a crash). It returns the records of those placed.
func (w *Worker) installSubtitles(plan rules.Plan, jobID int64, tmp, final string) ([]jobs.Sidecar, error) {
	files := plan.SubtitleFiles()
	if len(files) == 0 {
		return nil, nil
	}
	vst, err := os.Stat(tmp)
	if err != nil {
		return nil, err
	}
	vhash, err := SampleHash(tmp)
	if err != nil {
		return nil, err
	}
	var recs []jobs.Sidecar
	for k, o := range files {
		staged := rules.StagedSrt(tmp, k)
		st, err := os.Stat(staged)
		if err != nil {
			return nil, err
		}
		h, err := fullHash(staged) // .srt files are small: their whole content identifies them
		if err != nil {
			return nil, err
		}
		recs = append(recs, jobs.Sidecar{JobID: jobID, Path: rules.SrtName(final, o.Extract), Bytes: st.Size(),
			MTime: st.ModTime().UnixNano(), Hash: h, VideoPath: final, VideoBytes: vst.Size(), VideoHash: vhash})
	}
	if w.Sidecars != nil {
		if err := w.Sidecars.AddSidecars(recs); err != nil {
			return nil, fmt.Errorf("record the subtitle files: %w", err)
		}
	}
	var placed []jobs.Sidecar
	for k, c := range recs {
		err := placeSidecar(rules.StagedSrt(tmp, k), c.Path)
		if errors.Is(err, errPlacedSourceLeft) {
			// linked into place, the staged name not removed (the link fallback): the file is ours and
			// placed; the staging folder's cleanup removes the leftover name (review 25b-939e790)
			w.jobf(jobID, ": %s placed; %v", c.Path, err)
			err = nil
		}
		if err != nil {
			if w.Sidecars != nil { // this one and the rest were never put there: their names are not ours
				for _, u := range recs[k:] {
					w.Sidecars.DropSidecar(jobID, u.Path)
				}
			}
			w.removeSidecars(jobID, placed)
			if errors.Is(err, fs.ErrExist) {
				return nil, failed(srtTaken(c.Path), err)
			}
			return nil, retryable(failed("A subtitle file could not be put next to the new file, so nothing was changed. Press Retry.", err), retryGeneral)
		}
		placed = append(placed, c)
	}
	return placed, nil
}

// removeSidecars removes the .srt files we placed, each only if it is still exactly the one we wrote
// (size, time and full SHA-256): one replaced meanwhile is someone else's and stays. A record goes once
// its file is gone or not ours; one that could not be removed keeps its record, so the next start
// tries again and a Retry knows the file is ours (review 25b-1e31bdb).
func (w *Worker) removeSidecars(jobID int64, list []jobs.Sidecar) {
	for _, c := range list {
		if _, err := removeIfOurs(c); err != nil {
			w.jobf(jobID, ": could not remove %s (it is kept on record and tried again): %v", c.Path, err)
			continue
		}
		if w.Sidecars != nil {
			w.Sidecars.DropSidecar(jobID, c.Path)
		}
	}
}

// clearOwnSidecars removes the .srt files an earlier try of job left on record (RecoverSidecars' check,
// for this job only): a record goes once its file is gone, not ours, or removed; an error if one of ours
// is still there.
func (w *Worker) clearOwnSidecars(jobID int64) error {
	if w.Sidecars == nil {
		return nil
	}
	list, err := w.Sidecars.Sidecars()
	if err != nil {
		return fmt.Errorf("subtitle files on record: %w", err)
	}
	for _, c := range list {
		if c.JobID != jobID {
			continue
		}
		if _, err := removeIfOurs(c); err != nil {
			return fmt.Errorf("a subtitle file an earlier try left (%s) could not be removed: %w", c.Path, err)
		}
		w.Sidecars.DropSidecar(jobID, c.Path)
	}
	return nil
}

// removeIfOurs removes c's file if it is still exactly ours. removed says it was; nil error means the
// record can go (the file is gone, someone else's, or removed). When we could not tell whether the file
// is ours (Lstat or reading it failed), it is left alone and the error keeps the record, so a later
// start checks again (review 25b-28abb71).
func removeIfOurs(c jobs.Sidecar) (removed bool, err error) {
	switch o, err := sidecarIsOurs(c); o {
	case sidecarUnknown:
		return false, fmt.Errorf("could not tell whether it is ours: %w", err)
	case sidecarNotOurs:
		return false, nil
	}
	if err := os.Remove(c.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	return true, nil
}

// sidecarOwner is what sidecarIsOurs found out about a recorded .srt file.
type sidecarOwner int

const (
	sidecarNotOurs sidecarOwner = iota // gone, or not exactly the file we wrote: its record can go
	sidecarOurs                        // still exactly the file we wrote
	sidecarUnknown                     // could not check (Lstat or reading failed): keep the record
)

// sidecarLstat and sidecarHash are os.Lstat and fullHash; tests make them fail.
var (
	sidecarLstat = os.Lstat
	sidecarHash  = fullHash
)

// sidecarIsOurs compares c's file with what we wrote: regular file, size, time and full SHA-256.
func sidecarIsOurs(c jobs.Sidecar) (sidecarOwner, error) {
	st, err := sidecarLstat(c.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return sidecarNotOurs, nil
	case err != nil:
		return sidecarUnknown, err
	case !st.Mode().IsRegular() || st.Size() != c.Bytes || st.ModTime().UnixNano() != c.MTime:
		return sidecarNotOurs, nil
	}
	h, err := sidecarHash(c.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return sidecarNotOurs, nil
	case err != nil:
		return sidecarUnknown, err
	case h != c.Hash:
		return sidecarNotOurs, nil
	}
	return sidecarOurs, nil
}

// fullHash is the SHA-256 of the whole file.
func fullHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// RecoverSidecars ends what a crash left between putting .srt files beside a new file and the swap (run
// at start, after RecoverSwaps): where the new video is in place (its recorded size and sample hash at
// VideoPath), the swap happened and the files stay; otherwise each file that is still exactly ours is
// removed, so a Retry finds no name taken by our own file. A record stays only when its file could not
// be checked or removed; the next start tries again.
func RecoverSidecars(sj SidecarJournal) []string {
	list, err := sj.Sidecars()
	if err != nil {
		return []string{fmt.Sprintf("subtitle files: %v", err)}
	}
	var said []string
	for _, c := range list {
		swapped := false
		if st, err := os.Stat(c.VideoPath); err == nil && st.Size() == c.VideoBytes {
			if h, err := SampleHash(c.VideoPath); err == nil && h == c.VideoHash {
				swapped = true // the swap happened: the subtitles belong to the new file
			}
		}
		if !swapped {
			removed, err := removeIfOurs(c)
			if err != nil {
				said = append(said, fmt.Sprintf("job %d: could not remove %s, kept on record to try again: %v", c.JobID, c.Path, err))
				continue // its record stays
			}
			if removed {
				said = append(said, fmt.Sprintf("job %d: removed %s (put there before a swap that did not happen)", c.JobID, c.Path))
			}
		}
		sj.DropSidecar(c.JobID, c.Path)
	}
	return said
}

// srtTaken is design's sentence for an .srt name already there.
func srtTaken(name string) string {
	return fmt.Sprintf("A subtitle file named %s is already there. Nothing was changed. Move or rename that file, then press Retry.", filepath.Base(name))
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

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
		return retryable(kept("the original file changed while it was being converted, so the new file is out of date"), retryGeneral) // still being written
	}
	// media.FileState too: on Windows it carries the change time, which the Stat above does not (an edit
	// that keeps the size and puts the modification time back); unreadable now counts as changed
	if st, err := readSourceState(path, now); err != nil || !st.Same(beforeState) {
		return retryable(kept("the original file changed while it was being converted, so the new file is out of date"), retryGeneral) // still being written
	}
	if b, ok := media.ChangeTime(before); ok {
		if n, ok := media.ChangeTime(now); !ok || !n.Equal(b) {
			return retryable(kept("the original file changed while it was being converted, so the new file is out of date"), retryGeneral) // still being written
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
	return moveNoClobber(ctx, tmp, final, stillOK, func(name string) string {
		return fmt.Sprintf("There is already a file named %s next to the original, so the new file was not put there. Nothing was changed. Move or rename that file, then press Retry.", filepath.Base(name))
	})
}

// moveIntoFolder puts the new file in the folder for new files (it passed its checks): over our own
// earlier output of the same file, which goes now, but never over a file we didn't make (design 01:04Z).
func (w *Worker) moveIntoFolder(ctx context.Context, tmp, final string, stillOK func() error) error {
	return moveNoClobber(ctx, tmp, final, func() error {
		if err := stillOK(); err != nil {
			return err
		}
		if lst, err := os.Lstat(final); err == nil && w.ownOutput(final, lst) {
			return os.Remove(final) // the older generation: the original is still in the library
		}
		return nil
	}, notOurs)
}

// ownOutput reports whether the regular file at p (lst its Lstat) is one a done job of ours wrote.
func (w *Worker) ownOutput(p string, lst fs.FileInfo) bool {
	if !lst.Mode().IsRegular() {
		return false
	}
	own, err := w.Queue.IsOwnOutput(p, lst.Size(), lst.ModTime().UnixNano())
	return err == nil && own
}

// notOurs is the failure for a file we didn't make at a new file's name in the folder (design 01:04Z).
func notOurs(p string) string {
	return fmt.Sprintf("A file named %s is already in %s and wasn't made by Shrink Your Shit. Nothing was changed. Move or rename it, then press Retry.", filepath.Base(p), filepath.Dir(p))
}

// moveNoClobber renames tmp to final only if nothing is at final (renameNoReplace); a name taken fails
// the job with taken(final).
func moveNoClobber(ctx context.Context, tmp, final string, stillOK func() error, taken func(string) string) error {
	tries := 1
	if runtime.GOOS == "windows" {
		tries = 10
	}
	return renameRetry(ctx, func(a, b string) error {
		if err := stillOK(); err != nil {
			return err
		}
		if err := renameNoReplace(a, b); errors.Is(err, fs.ErrExist) {
			return failed(taken(b), err)
		} else {
			return err
		}
	}, tmp, final, tries, time.Second)
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

// keepsOriginal is whether the plan leaves the original where it is (beside, or another folder, S6).
func keepsOriginal(plan rules.Plan) bool {
	return OutputMode(plan.Output) == Beside || OutputMode(plan.Output) == Folder
}

// FolderPath is where "Save to another folder" (S6) puts the new file of src (in library lib): the same
// sub-folders under dir, the same name with ext.
func FolderPath(dir, lib, src, ext string) string {
	name := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src)) + ext
	if rel, err := filepath.Rel(lib, filepath.Dir(src)); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Join(dir, rel, name)
	}
	return filepath.Join(dir, name)
}
