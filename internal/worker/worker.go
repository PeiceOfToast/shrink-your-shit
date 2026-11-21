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
	"os/exec"
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
	"github.com/shrink-your-shit/shrink-your-shit/internal/scanner"
	"github.com/shrink-your-shit/shrink-your-shit/internal/vars"
)

// Queue is everything a worker needs from the job store.
type Queue interface {
	Claim(worker string) (jobs.Job, error)
	Progress(id int64, pct float64) error
	Complete(id int64, outputPath string, sizeAfter int64) error
	Fail(id int64, code, msg string) error
	// Release puts a running job back in the queue (used on shutdown).
	Release(id int64) error
	// The job's log lines, steps and failure summary in the database (user review 11-13 item E).
	AppendLogLine(id int64, step int, section, level, stream, text string) error
	// Get is the job as the store has it now (its final status and sizes for the summary line).
	Get(id int64) (jobs.Job, error)
	RecordRunStep(id int64, st jobs.RunStep) error
	SetFailureSummary(id int64, f jobs.FailureSummary) error
	ClearFailureSummary(id int64) error
	// UserStopped turns a job the person stopped into a file skipped as user_stopped.
	UserStopped(id int64) error
	// StopRequested reports whether "Stop this file" was asked for a running job (checked before the swap).
	StopRequested(id int64) (bool, error)
	// Keep marks a job "kept original": a safety check refused the result (reason says why).
	Keep(id int64, code, reason string) error
	// RetryLater puts a job whose failure may pass back in the queue, not to be taken before at (DD-2).
	RetryLater(id int64, at time.Time, code, reason string) error
	// EncoderFell records a graphics encoder's failure on worker (the job converts on the processor); paused says
	// whether it stops the worker's graphics encoder for a while. EncoderWorked ends a run of failures.
	EncoderFell(id int64, worker, encoder, reason string) (paused bool, err error)
	EncoderWorked(worker string) error
	// EncoderPaused: whether worker's graphics encoder is paused, and until when.
	EncoderPaused(worker string) (until time.Time, paused bool, err error)
	// ShareDropped puts a job back in the queue because its share stopped answering (D4c): not a failure.
	ShareDropped(id, shareID int64, mountPath, words string) error
	// OutputFolder is a library's folder for new files with "Next to each library" (V5a), or why it has
	// none.
	OutputFolder(libraryID int64, libPath string) (string, *jobs.FolderProblem, error)
	// LeaseOf, RenewLease and HoldsLease: a running job's lease (V7a).
	LeaseOf(id int64) (string, error)
	RenewLease(id int64, lease string) (bool, error)
	HoldsLease(id int64, lease string) (bool, error)
	// FallbackOf is the path a job falls back to (a rules.Branch, JSON; "" none) (V6b).
	FallbackOf(id int64) (string, error)
	// FallBack puts a job back in the queue along another path; AddFailureStep records a failure (V6b).
	FallBack(id int64, branch string, path []string, nodeID, words string) error
	AddFailureStep(id int64, nodeID, words string) error
	// IsOwnOutputExact reports whether the file at path (this size and time) is one a done job wrote.
	IsOwnOutputExact(path string, size, mtime int64) (bool, error)
	// OutputOwner is the newest job whose output is path (ok false when none).
	OutputOwner(path string) (id int64, ok bool, err error)
	// RecordPlan stores the Plan a running job converts with (JSON) and its fingerprint.
	RecordPlan(id int64, plan, fingerprint string) error
	// Skip ends a job as a skipped file with words and a reason code (S3: Dolby Vision left alone).
	Skip(id int64, words, code string) error
	// AddStep adds a line to a job's step summary (S5): converted, verified, kept, replaced.
	AddStep(id int64, step string, ok bool, words string) error
	// PutProbe stores a file's probe (JSON) for its size and time: the new file's, for the Files view.
	PutProbe(path string, size, mtime int64, info string) error
	// GetProbe returns the stored probe (JSON) of path if it was taken at this size and modification time.
	GetProbe(path string, size, mtime int64) (string, bool)
	// RecordJobStats stores the running job's run counters (the L2 summary's passes and bytesWritten).
	RecordJobStats(id int64, passes int, bytesWritten int64) error
	// RecordJobMove adds one file action the replace step took, in order (the replace step's moves[]).
	RecordJobMove(id int64, m jobs.Move) error
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
// ShareDown is the saved share a library's folder is in, when that share does not answer (D4c). Answering
// is set when the share answers now but had an outage or reconnect while the job ran: the job is still put
// back, and nothing is paused.
type ShareDown struct {
	ShareID   int64
	Name      string
	MountPath string
	Answering bool
}

type Worker struct {
	// ShareDown says whether a library's folder is on a share that does not answer now (nil: no shares
	// are checked). A convert that fails while it does is put back in the queue, not failed (D4c).
	ShareDown func(folder string, jobID int64) (ShareDown, bool)
	// Encoder is the graphics encoder the local worker uses for a video codec (ffmpeg's name, e.g. libx264), or ""
	// for the processor (or nil: the processor). need10 is set when the new file is 10-bit (an HDR plan, or a
	// 10-bit source, SDR or not), so that encoder's 10-bit test must have passed too. A plan it can't do, and a
	// graphics encode that fails, use the processor. note, when set, is the job-log line for a processor the
	// worker fell back to (not set for a processor by choice).
	Encoder func(codec string, need10 bool, hdr *rules.HDR) (enc, note string)
	// GraphicsDecodes says whether a graphics family (qsv, vaapi) decodes codec at that bit depth here: the
	// full GPU pipeline needs it (nil: the processor decodes). Set from the doctor's probe.
	GraphicsDecodes func(ctx context.Context, fam, codec string, tenBit bool) bool
	// EncoderCandidates, when set, returns the job log's lines for the encoder choice (log-spec R5): the
	// hardware found and one line per encoder considered, with why it was or was not used, for the plan's
	// codec (ffmpeg's name), whether the file is 10-bit (HDR, or a 10-bit source) and its HDR data (R4's gate:
	// a 10-bit test, and a family proven to keep HDR10's mastering data). The worker writes them and then the
	// chosen encoder's line, in section encode.
	EncoderCandidates func(codec string, need10 bool, hdr *rules.HDR) []string
	// GPUSlots is how many of this computer's graphics card's slots the person let it have (Workers page),
	// read each time a job asks for a place, so a changed limit applies to the next job that asks; a job already
	// waiting keeps the limit it read when it started waiting. 0: the processor does the encoding. nil
	// (a test's worker): no limit, every graphics job runs. All of the program's workers share the one card.
	GPUSlots func() int
	// verified is the probe of the new file the last check passed (S5: stored for its final path).
	verified media.Info
	// movedAside is the trash sentence for our earlier output this job moved aside (Convert again). The job's kept
	// step says it too, since the Job page Timeline shows steps, not the log.
	movedAside string
	// planOutput is the current job's plan output (replace, beside or folder), set by process: the job's last
	// Timeline step follows it, not the worker's Mode.
	planOutput OutputMode
	check      *CheckScore // the picture check's score for the job in hand (nil: none ran); set by the comparison
	// remote is the job a worker on another computer converts, run here (V7b2); nil for a local one.
	remote *RemoteJob
	// failures: the running job's path's choices for when it doesn't work (V6b; under mu)
	failures *rules.Failures
	// lease: the running job's lease (V7a; under mu)
	lease string
	ID    string
	Queue Queue
	// phase is the job log section the running job is in (joblog.go); the worker's own lines go there. It is read
	// by the stall and lease watchers on other goroutines, so it is an atomic value (setPhase, section).
	phase  atomic.Value
	Exec   media.Exec
	FFmpeg string
	// FFprobe checks the result before it replaces anything.
	FFprobe string
	// Template returns the template of a job's library's flow, compiled per file into the job's Plan.
	// Nil: a legacy template from Rule and Mode for every job.
	Template func(jobs.Job) (rules.Template, error)
	// RunInfo returns what a job's workflow says about it (its library's name, its name and revision and
	// its own variables) for the {{name}} values (W4b). Nil: the names have no value from it.
	RunInfo func(jobs.Job) (jobs.RunInfo, error)
	Rule    rules.Rule
	Mode    OutputMode
	Poll    time.Duration
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
	// FinishAfter: the same, once the position is at the file's length (the finishing step; 0: DefaultFinishAfter).
	FinishAfter time.Duration
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
	finishing  atomic.Bool        // the position is at the file's length: the stall watchdog gives the finishing allowance
	stalledFor atomic.Int64       // the limit (ns) the watchdog last stopped an encode at: the message says it
	// the running job's run counters (log-spec L2 summary): the ffmpeg runs it needed and the bytes it wrote.
	passes, bytesWritten atomic.Int64
	statsCounting        atomic.Bool // false: this job's counters are not recorded (a remote job, whose runs are not all here)
	// the running local encode's silence (log-spec R9's quiet warning): when ffmpeg last printed anything, read
	// by the stall watcher; quietOn is false outside a local encode, quietWarned says the warning is written.
	lastOutput  atomic.Int64
	quietOn     atomic.Bool
	quietWarned atomic.Bool
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

// DefaultFinishAfter is the allowance once an encode's position is at the file's length. ffmpeg then writes the
// end of the file with no progress line: an MP4 is rewritten to move its index to the front (+faststart), which
// copies the whole file, minutes for a large one on a NAS. An hour is generous for a 60 GB remux; past it the finish
// is stopped like any stall, so a hung write still ends.
const DefaultFinishAfter = time.Hour

// finishTolerance is how many seconds either side of the file's length a position may be and still count as the
// end: the last progress report can stop a little short of it, and out_time can run a little past it.
const finishTolerance = 2.0

// stallWarnAfter is how long an encode may go without moving (position, frames or bytes) before the job log warns (R9);
// the job itself is stopped only at StallAfter (ten minutes by default).
const stallWarnAfter = 30 * time.Second

// quietAfter is how long ffmpeg may print nothing before the job log says the conversion is still running
// (log-spec R9's quiet warning). Unlike the stall warning it says nothing about the media position.
const quietAfter = 60 * time.Second

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
	w.failures = nil // this job's own, once its plan is made (V6b)
	w.lease, _ = w.Queue.LeaseOf(job.ID)
	lease := w.lease
	w.mu.Unlock()
	return w.runClaimed(ctx, job, jobCtx, cancel, lease, true)
}

// runClaimed runs job, claimed for this worker (current, cancelJob and lease set), and records its
// outcome. renew: this worker renews its lease (a local job; a remote worker renews its own, V7b2).
func (w *Worker) runClaimed(ctx context.Context, job jobs.Job, jobCtx context.Context, cancel context.CancelFunc, lease string, renew bool) (bool, error) {
	var err error
	if renew {
		renewDone := make(chan struct{})
		defer close(renewDone)
		go w.renewLease(job.ID, lease, renewDone) // V7a: kept while it works, the probe and the checks too
	}
	w.startLines(job)
	w.resetStats(w.remote != nil) // this job's run counters (log-spec L2 summary)
	defer w.recordStats(job.ID)   // and stored once its outcome is recorded
	defer w.summaryLine(job.ID)   // the job's last line: its verdict, once its outcome is recorded
	w.jobf(job.ID, ": started")
	// a new attempt: an earlier attempt's failure summary is not this one's (a kept attempt would show it)
	if err := w.Queue.ClearFailureSummary(job.ID); err != nil {
		log.Printf("clear failure summary of job %d: %v", job.ID, err)
	}
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
	// The job was given back to the queue (its lease ended: V7a, V7b2): another worker has it now, so
	// there is nothing to record here.
	if errors.Is(err, errLeaseLost) {
		w.jobf(job.ID, ": given back to the queue (this worker no longer holds it)")
		return true, nil
	}
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
			return true, w.Queue.Fail(job.ID, jobs.ReasonStalled, fmt.Sprintf("Stopped: no progress for %s. The original is untouched; press Retry to try again.", w.stalledLimit()))
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			// Shutting down: not the file's fault, so run it again next start.
			return true, w.Queue.Release(job.ID)
		}
		// D4c: a share that stopped answering is not this file's failure: back in the queue, its libraries
		// paused by the share until it answers, and no try counted.
		if w.ShareDown != nil {
			if sd, down := w.ShareDown(job.Library, job.ID); down {
				if sd.Answering {
					words := "Put back in the queue: " + sd.Name + " lost its connection during the convert."
					w.jobf(job.ID, " (%s): %s", job.Path, words)
					return true, w.Queue.ShareDropped(job.ID, 0, "", words)
				}
				words := "Put back in the queue: " + sd.Name + " stopped answering."
				w.jobf(job.ID, " (%s): %s", job.Path, words)
				return true, w.Queue.ShareDropped(job.ID, sd.ShareID, sd.MountPath, words)
			}
		}
		var se *SkippedError
		if errors.As(err, &se) { // left alone by its flow (S3): Skipped, not failed, never retried
			w.jobf(job.ID, " (%s): skipped: %s", job.Path, se.Words)
			return true, w.Queue.Skip(job.ID, se.Words, se.Code)
		}
		// V6b: the path's own choice for when its conversion doesn't work comes first.
		w.mu.Lock()
		fs := w.failures
		w.mu.Unlock()
		var kept *KeptError
		noRetry := false
		if f := fs.For(errors.As(err, &kept)); f != nil {
			done, rerr := w.applyFailure(job, err, f)
			if done {
				return true, rerr
			}
			noRetry = true // stop, or the last try: it fails or is kept as before, without another
		}
		// A failure that may pass, with a try left: back in the queue for later, from the start (DD-2).
		if wait, ok := retryIn(err, job.Attempts); ok && !noRetry {
			w.jobf(job.ID, " (%s): try %d of %d failed, trying again in %s: %v", job.Path, job.Attempts+1, maxTries, wait, err)
			w.Queue.AddFailureStep(job.ID, "", retryNote(job.Attempts+1, maxTries, plainReason(err), wait))
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
		// Mark errors from the place/replace phase (after the check: staging -> final path,
		// the swap into the library, kept-original moves) with a type.
		var pe *placeError
		if errors.As(err, &pe) {
			// The new file was made and checked, but putting it in place was refused (user review 11-13
			// item E, the manager's check): a failed 'replace' step with the OS error as its key line,
			// and a 'replaced' timeline entry instead of a second 'converted'.
			w.jobf(job.ID, " (%s) failed putting the new file in place: %v", job.Path, err)
			w.replaceStep(job.ID, pe.Started, true, pe.keyLine())
			w.Queue.AddStep(job.ID, "replaced", false, plainFailure(err))
			if ferr := w.Queue.Fail(job.ID, reasonCode(err), plainFailure(err)); ferr != nil {
				return true, ferr
			}
			return true, err
		}
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
	// The log line and the steps first, then done: a job's record is complete by the time its state says
	// done, so a stream that ends on done never misses them (review-7484a34). The new file is in place
	// already, so they are true even if recording done fails.
	// before and after, also when the new file is not smaller (it is kept: always standardize)
	w.jobf(job.ID, ": done, %s: %s -> %s", final, rules.HumanBytes(job.SizeBefore), rules.HumanBytes(size))
	w.Queue.AddStep(job.ID, "verified", true, "Every check passed: the new file is complete, plays cleanly and looks like the original.")
	if filepath.Dir(final) != filepath.Dir(job.Path) { // "Save to another folder" (S6)
		w.Queue.AddStep(job.ID, "kept", true, w.withMovedAside("The new file was written to "+filepath.Dir(final)+"; the original stays where it is."))
	} else if w.planOutput == Beside {
		w.Queue.AddStep(job.ID, "kept", true, w.withMovedAside("The new file was written next to the original, which stays as it is."))
	} else {
		w.Queue.AddStep(job.ID, "replaced", true, w.withMovedAside(fmt.Sprintf("The new file is in place: %s to %s.", rules.HumanBytes(job.SizeBefore), rules.HumanBytes(size))))
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

// unreadableSecretWords is what the job says when its workflow's secret was sealed on another computer: the
// secret is named, because entering it again on this one is the fix, and nothing was converted.
func unreadableSecretWords(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "“" + n + "”"
	}
	what, it := "The workflow secret "+strings.Join(quoted, ", "), "it"
	if len(names) > 1 {
		what, it = "The workflow secrets "+strings.Join(quoted, ", "), "them"
	}
	return what + " can't be opened on this computer (it was sealed on another one), so converting did not start. " +
		"Your original was not touched. Enter " + it + " again in the workflow, then press Retry."
}

// jobf logs a line about job id, to the program's log and to the job's own run log (S5).
func (w *Worker) jobf(id int64, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	log.Printf("worker %s: job %d%s", w.ID, id, msg)
	if w.Queue != nil { // the job's log lives in the database (user review 11-13 item E)
		w.Queue.AppendLogLine(id, 0, w.section(), jobs.LevelInfo, "app", strings.TrimPrefix(strings.TrimPrefix(msg, ":"), " "))
	}
}

// recordEncode keeps the encode step in the database: its command, how long it took, the ffmpeg lines it wrote
// and, when it failed, the failure summary (the last error lines of ffmpeg, or the last lines if none report one).
func (w *Worker) recordEncode(id int64, args []string, started time.Time, encErr string, terr error, probe string) {
	if w.Queue == nil {
		return
	}
	lines := strings.Split(strings.TrimSpace(encErr), "\n")
	if encErr == "" {
		lines = nil
	}
	for _, l := range lines {
		stream := "stderr"
		if strings.HasPrefix(strings.ToLower(l), "libva info:") { // the GPU driver's chatter: kept in the log, not shown as ffmpeg's output
			stream = "info"
		}
		w.Queue.AppendLogLine(id, 0, secEncode, jobs.LevelInfo, stream, jobs.BenignText(l)) // R8: a known harmless line says so
	}
	outcome := "ok"
	if terr != nil {
		outcome = "failed"
	}
	w.Queue.RecordRunStep(id, jobs.RunStep{Index: 0, Name: "encode", StartedAt: started.UTC().Format(time.RFC3339),
		TookMs: w.now().Sub(started).Milliseconds(), Outcome: outcome, Command: "ffmpeg " + strings.Join(args, " "), Probe: probe, OutputLines: len(lines)})
	if terr == nil {
		return
	}
	var keys []string
	for _, l := range lines {
		if jobs.InfoLine(l) || jobs.BenignNote(l) != "" { // R8: a harmless line is not a failure's reason
			continue
		}
		if strings.Contains(strings.ToLower(l), "error") || strings.Contains(strings.ToLower(l), "invalid") {
			keys = append(keys, l)
		}
	}
	ran, exit := ffmpegExit(terr)
	w.Queue.SetFailureSummary(id, jobs.FailureSummary{Step: 0, StepCount: 1, AfterMs: w.now().Sub(started).Milliseconds(),
		ExitCode: exit, FfmpegRan: ran, KeyLines: keys})
}

// SkippedError: the file is not converted because its flow leaves it alone (S3's Dolby Vision skips);
// the job ends as a skipped file with these words and code.
type SkippedError struct{ Words, Code string }

// placeError is an error from the place/replace phase (after the encode and check).
// It wraps the underlying error with the path that was being written to.
type placeError struct {
	Path    string    // the file or folder the system refused, exactly as the OS error names it
	Words   string    // the OS error's own words ("permission denied", "read-only file system")
	Started time.Time // when putting the new file in place began, for the replace step's timing
	Err     error
}

func (e *placeError) Error() string { return e.Err.Error() }
func (e *placeError) Unwrap() error { return e.Err }

// replaceStep records a replace step (Index 2) when the file swap/placement
// phase completes or fails. On success Outcome is "ok"; on failure it records
// the failure details so the log shows what went wrong with the placement.
func (w *Worker) replaceStep(id int64, started time.Time, failed bool, result string) {
	if w.Queue == nil {
		return
	}
	outcome := "ok"
	tookMs := w.now().Sub(started).Milliseconds()
	if failed {
		outcome = "failed"
	}
	w.Queue.RecordRunStep(id, jobs.RunStep{Index: 2, Name: "replace",
		StartedAt: started.UTC().Format(time.RFC3339),
		TookMs:    tookMs, Outcome: outcome, Result: result})
	if !failed {
		return
	}
	w.Queue.AppendLogLine(id, 2, secReplace, jobs.LevelError, "app", result)
	w.Queue.SetFailureSummary(id, jobs.FailureSummary{
		Step: 2, StepCount: 3, AfterMs: tookMs,
		ExitCode: -1, FfmpegRan: true, KeyLines: []string{result},
	})
}

// placeError records the place failure steps and returns a wrapped error.
// It extracts the path from *os.PathError / *os.LinkError when present, and
// names a .syp staging folder's parent instead of the staging folder itself.
func (w *Worker) placeError(jobID int64, placeStarted time.Time, final string, err error) error {
	if err == nil {
		return nil
	}
	pe := &placeError{Path: final, Started: placeStarted, Err: err}
	var pathErr *os.PathError
	var linkErr *os.LinkError
	switch {
	case errors.As(err, &linkErr): // rename/link: the destination is where the write was refused
		pe.Path, pe.Words = linkErr.New, linkErr.Err.Error()
	case errors.As(err, &pathErr):
		pe.Path, pe.Words = pathErr.Path, pathErr.Err.Error()
	default:
		pe.Words = err.Error()
	}
	return pe
}

// keyLine is the failure summary's line for a place error, in the form jobs.DeniedFolder reads
// ("Couldn't write <path>: <words>"), so the person's sentence names the real folder.
func (e *placeError) keyLine() string { return "Couldn't write " + e.Path + ": " + e.Words }

func (e *SkippedError) Error() string { return "skipped: " + e.Words }

const cannotRead = "shrink-your-shit is not allowed to read this file, so nothing was converted. Let it read the file (Settings > About says how), then press Retry."

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
		return "shrink-your-shit is not allowed to write in this folder, so converting stopped. Your original was not touched. Let it write there (Settings > About says how), then press Retry."
	}
	return "Converting did not finish. Your original was not touched. Press Retry; if it keeps failing, Settings > About shows what went wrong."
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

func (w *Worker) finishAfter() time.Duration {
	if w.FinishAfter > 0 {
		return w.FinishAfter
	}
	return DefaultFinishAfter
}

// stalledLimit is the limit the watchdog stopped the running encode at (the stall allowance, or the finishing one).
func (w *Worker) stalledLimit() time.Duration {
	if d := time.Duration(w.stalledFor.Load()); d > 0 {
		return d
	}
	return w.stallAfter()
}

// stallLimit is how long an encode may go without progress: the stall allowance, or the finishing one once its
// position is at the file's length. The finishing allowance is never shorter than the stall one.
func (w *Worker) stallLimit(after time.Duration, finishing bool) time.Duration {
	if finishing {
		return max(after, w.finishAfter())
	}
	return after
}

// reachedEnd says a position (out_time, µs) is the file's length: every frame is written and the encode is
// finishing. Only a position near the length is: one well past it is a stuck encode, not a finish. A length we
// don't know (0) never is.
func reachedEnd(outUS, durationSec float64) bool {
	d := outUS - durationSec*1e6
	return durationSec > 0 && d >= -finishTolerance*1e6 && d <= finishTolerance*1e6
}

// watchStall stops job id (stopStall) if its encode reports no progress for stallAfter (stallLimit once it is
// finishing). The returned func ends the watch. Checks after the encode have their own time limits (slice 10).
func (w *Worker) watchStall(id int64) func() {
	w.progressAt.Store(time.Now().UnixNano())
	w.finishing.Store(false)
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
				limit := w.stallLimit(after, w.finishing.Load())
				if time.Since(time.Unix(0, w.progressAt.Load())) >= limit {
					w.stalledFor.Store(int64(limit))
					w.jobf(id, ": no progress for %s, stopping it", limit)
					w.stop(id, stopStall)
					return
				}
				w.quietCheck(id, w.now()) // R9: ffmpeg printing nothing for a minute
			}
		}
	}()
	return func() { close(done) }
}

// watchRemoteStall is watchStall for a remote worker's encode: the worker reports its position, and the
// job is stopped (stopStall, the same words and retry rules) when it hasn't moved on for stallAfter
// while the worker says it is converting.
func (w *Worker) watchRemoteStall(id int64, rj *RemoteJob) func() {
	after := w.stallAfter()
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(max(after/20, 10*time.Millisecond))
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-t.C:
				limit := w.stallLimit(after, rj.finishing())
				if still, judging := rj.stillFor(now); judging && still >= limit {
					w.stalledFor.Store(int64(limit))
					w.jobf(id, ": the remote encode made no progress for %s, stopping it", limit)
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
	w.movedAside = ""
	w.planOutput = ""
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
	// Convert again (design 11:45Z): a file that replaced its original runs from the original kept in
	// .syp-trash, never from our own output; the new file replaces our earlier one (replaceOwnOutput).
	srcPath, again, err := w.againSource(job)
	if err != nil {
		return "", err
	}
	// A link is never followed: checked before opening, and the open itself does not follow one (slice 23).
	if lst, err := os.Lstat(srcPath); err == nil && lst.Mode()&fs.ModeSymlink != 0 {
		return "", kept("%s", rules.SkipWords[rules.SkipSymlink])
	}
	before, serr := statOpen(srcPath)
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
	beforeState, err := readSourceState(srcPath, before)
	if err != nil {
		return "", failed(cannotRead, err)
	}
	// The file guards again (slice 23; the scanner decided earlier, and the file may have gained another
	// name since): a shared file is left alone. sourceUnchanged checks it again right before the swap.
	if media.LinkCount(srcPath, before) > 1 {
		return "", kept("%s", rules.SkipWords[rules.SkipSharedFile])
	}
	if err := removeLeftoverTemps(job.Path); err != nil {
		return "", err
	}
	var stage *staged
	defer func() {
		if stage == nil {
			return
		}
		left, rerr := stage.remove() // after a successful move only the empty folder is left
		w.recordMove(job.ID, "remove", stage.dir, "", rerr)
		if rerr == nil {
			w.logLine(job.ID, secReplace, jobs.LevelInfo, fmt.Sprintf("Removed the temp folder %s (%s bytes left in it).",
				stage.dir, rules.GroupDigits(left)))
			return
		}
		w.logLine(job.ID, secReplace, jobs.LevelWarning, fmt.Sprintf("Could not remove the temp folder %s: %v. It may still hold %s bytes; delete it to free the space.",
			stage.dir, rerr, rules.GroupDigits(left)))
	}()

	// What the source looks like (and so what the new file must look like).
	// The scanner probed this file when it queued the job; the same size and time hit its cache (one probe per version).
	pctx, pcancel := context.WithTimeout(ctx, quickTimeout)
	src, perr := w.probeSource(pctx, srcPath)
	pcancel()
	if perr != nil {
		if errors.Is(pctx.Err(), context.DeadlineExceeded) {
			return "", checkFailed(pctx, "reading the original", perr)
		}
		log.Printf("worker %s: %s: %v", w.ID, job.Path, perr)
		return "", kept("the original can't be read any more, so it was left as it is")
	}
	w.inputLines(job.ID, srcPath, before, src)
	if err := w.preflight(srcPath, before.Size()); err != nil {
		return "", err
	}
	// The original's picture count: the container's own count when it states one exactly (an mp4's nb_frames, an
	// mkv's DURATION tag), which the probe has read already; only when it does not is every packet counted, which
	// reads the whole file (P2: the encode reads it all again anyway, so that second read is only for files that
	// cannot say their count). expectedPackets prefers the exact count, so what the checks expect is unchanged.
	var packets int64
	if frames, exact := sourceFrameCount(src); !exact || frames <= 0 {
		if ord := src.MainVideoOrdinal(); ord >= 0 {
			cctx, ccancel := context.WithTimeout(ctx, countTimeout)
			n, cerr := media.CountVideoPackets(cctx, w.Exec, w.FFprobe, srcPath, ord)
			ccancel()
			if cerr != nil {
				return "", checkFailed(cctx, "counting the original's video frames", cerr)
			}
			packets = n
		}
	}
	// The Plan for this file, from its library's flow and the probe just taken: what to run, and what
	// the result must look like. Whether to convert it was the scanner's decision (as before Plans);
	// only a flow that cannot say how to convert stops the job here.
	plan := tmpl.Compile(src)
	if fb, err := w.Queue.FallbackOf(job.ID); err == nil && fb != "" { // V6b: it falls back to another path
		var b rules.Branch
		if json.Unmarshal([]byte(fb), &b) == nil {
			plan = rules.CompileBranch(b, src, tmpl.Languages)
		}
	}
	w.mu.Lock()
	w.failures = plan.OnFailure // what this path does when it doesn't work (RunOne reads it)
	w.planOutput = OutputMode(plan.Output)
	w.mu.Unlock()
	// Dolby Vision the flow leaves alone is never encoded, whatever was decided when the job was queued
	// (review a09f6f5: a job queued before migration 17 or a setting change). Only these skips: the
	// others stay the scanner's decision, as before Plans.
	if words, code := tmpl.DolbyVisionHold(src); code != "" { // from the probe, not the Plan's skip (review 7ab8f72)
		if again != nil {
			return "", againLeftAlone()
		}
		w.writeStepSections(job.ID, []rules.StepDecision{{Label: rules.StepLeftAlone, Outcome: "skipped", Decision: words}})
		return "", &SkippedError{Words: words, Code: code}
	}
	// Any skip of the Plan made now (the flow changed since the job was queued: an HDR file with a
	// non-HEVC target, a choice that can't run...) ends the job as a skipped file with that reason,
	// never an encode (review 6cafc6f: the second such case after Dolby Vision). The scanner's rules
	// for what is queued are unchanged.
	if plan.Skip != "" && again != nil { // the done job stays done: its file and kept original as they are
		return "", againLeftAlone()
	}
	if plan.Skip != "" {
		w.writeStepSections(job.ID, plan.Steps) // the step that left it alone still says so (L2, R4)
		code := plan.SkipCode
		if code == "" {
			code = rules.SkipSettingCannotRun
		}
		return "", &SkippedError{Words: plan.Skip, Code: code}
	}
	if plan.Video.Codec == "" {
		return "", failed("This library's setting can't convert this file, so it was left as it is.", fmt.Errorf("its library's flow cannot convert it: %s", plan.Skip))
	}
	// The workflow's own decisions, one section per step, before the encode (L2, log-spec R3/R4). A step
	// that did nothing still has its section. The notes below are the steps' further words, so they go
	// there too; a note whose step the flow does not have (a legacy flow has video only) goes to encode.
	stepSec := w.writeStepSections(job.ID, plan.Steps)
	stepNote := func(label, text string) {
		sec := stepSec[label]
		if sec == "" {
			sec = secEncode
		}
		w.jobNote(job.ID, sec, jobs.LevelInfo, "(%s): %s", job.Path, text)
	}
	// What the conversion will do, logged only now that it runs (review 48f07eb: a skipped Plan
	// converts nothing, so its notes would be untrue).
	if plan.MainAudioUsed { // Basic's stereo AAC found no track in the person's languages (S4)
		stepNote(rules.StepAudio, rules.NoAudioInMyLanguageMain)
	}
	if plan.TypeNote != "" { // "keep" made this MP4 an MKV (review 4ac9c0d)
		stepNote(rules.StepFileType, plan.TypeNote)
	}
	if plan.SmallKept { // design, 02:26Z: once per file
		stepNote(rules.StepAudio, rules.SmallKeptNote)
	}
	if plan.AudioFolded { // design, 01:52Z: once per file
		stepNote(rules.StepAudio, rules.FoldedNote)
	}
	if plan.StyledSubsPlain { // design, 02:49Z: once per file
		note := rules.StyledSubsNote
		if plan.Container == "webm" {
			note = rules.StyledSubsWebMNote
		}
		stepNote(rules.StepSubtitles, note)
	}
	if plan.BitmapSubsDropped > 0 { // user bug 11-20: WebM can't hold picture subtitles, so they are left out
		stepNote(rules.StepSubtitles, rules.BitmapSubsNote(plan.BitmapSubsDropped))
	}
	if plan.HDRNote != "" { // what the HDR conversion does not keep (S3)
		stepNote(rules.StepConvertVideo, plan.HDRNote)
	}
	// W4b: the values the Save the result step's {{name}} fields take, and its track title. The title
	// goes on every audio track of the new file, so it is resolved before the snapshot: a remote worker
	// encodes the plan it is given.
	vals, vtable, err := w.runValues(job, src, plan)
	if err != nil {
		// a secret sealed on another computer can't be opened here (a restored backup): the job stops,
		// naming it, rather than run with it silently empty
		var unreadable *jobs.SecretUnreadableError
		if errors.As(err, &unreadable) {
			return "", retryable(failed(unreadableSecretWords(unreadable.Names), err), retryGeneral)
		}
		return "", retryable(failed("This library's setting could not be read, so converting did not start. Your original was not touched. Open the library and save its setting again, then press Retry.", err), retryGeneral)
	}
	var vEmpty []string
	if plan.TrackTitle != "" {
		title, empty := vars.Resolve(plan.TrackTitle, vals)
		plan.Title, vEmpty = strings.TrimSpace(title), append(vEmpty, empty...)
		plan.AudioTracks = plan.AudioTrackCount(src)
	}
	ext := filepath.Ext(job.Path)
	if plan.Container != "" {
		ext = "." + plan.Container
	}
	outName, empty := vars.OutputName(plan.OutputName, ext, vals)
	vEmpty = append(vEmpty, empty...)
	if nameTooLong(outName) { // the final rename would refuse it, after the conversion (review2-504 P3)
		return "", failed(tooLongWords("Output file name", outName), errors.New("the output file name is too long"))
	}
	exp := plan.Expect(src, expectedPackets(src, packets))
	if exp.DurationSec <= 0 {
		exp.DurationSec = job.DurationSec
	}
	// The job keeps what it is about to do and how the result will be judged. If that cannot be
	// recorded, it does not start: nothing has been written yet.
	snap := rules.Snapshot{Plan: plan, Expect: exp, Verify: w.verification(exp), Source: &src, Variables: vtable}
	b, err := json.Marshal(snap)
	if err == nil {
		err = w.Queue.RecordPlan(job.ID, string(b), snap.Fingerprint())
	}
	if err != nil {
		return "", retryable(failed("What this conversion will do could not be recorded, so it did not start. Your original was not touched. Press Retry.", err), retryGeneral)
	}

	// Where the new file goes: beside the original, at its name, or, in replace mode with another file
	// type, at its name with the new extension (25a-2). A name the Save the result step's template made
	// (W4b) takes the place of that name, in the same folder; "" is today's name.
	newName := rules.ReplaceName(job.Path, ext)
	if outName != "" {
		newName = filepath.Join(filepath.Dir(job.Path), outName)
	}
	switch OutputMode(plan.Output) {
	case Beside:
		newName = BesidePathAs(job.Path, ext)
		if outName != "" {
			newName = filepath.Join(filepath.Dir(job.Path), outName)
		}
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
		} else if strings.Contains(dir, "{{") { // W4b: the folder as a template, filled in for this file
			d, empty := vars.Folder(dir, vals)
			vEmpty = append(vEmpty, empty...)
			for _, piece := range strings.Split(filepath.ToSlash(d), "/") {
				if nameTooLong(piece) {
					return "", failed(tooLongWords("Folder", piece), errors.New("the folder for new files has a piece that is too long"))
				}
			}
			if !filepath.IsAbs(d) {
				return "", failed(fmt.Sprintf("The folder for new files came out as %q, which isn't a full path, so nothing was converted. Your original was not touched. Check the folder in the workflow's Save the result step, then press Retry.", d), errors.New("folder for new files: its template did not make a full path"))
			}
			dir = d
		}
		name := strings.TrimSuffix(filepath.Base(job.Path), filepath.Ext(job.Path)) + ext
		if outName != "" {
			name = outName
		}
		newName = FolderPathAs(dir, job.Library, job.Path, name)
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
	// W4b: the .srt files' names, resolved beside the new file (an empty template keeps today's names),
	// and the job log's variables section, now that every field the run uses is filled in.
	plan.SrtNames, empty = vars.SrtNames(newName, plan.SrtName, subtitlesOf(plan), vals)
	vEmpty = append(vEmpty, empty...)
	for _, n := range plan.SrtNames {
		if base := filepath.Base(n); nameTooLong(base) { // as with the new file's name: the write would refuse it
			return "", failed(tooLongWords("Subtitle file name", base), errors.New("a subtitle file name is too long"))
		}
	}
	w.variableLines(job.ID, vtable, distinctNames(vEmpty))
	if keepsOriginal(plan) { // the name is taken already: no point converting (checked again at the end)
		if OutputMode(plan.Output) == Folder || OutputMode(plan.Output) == Beside { // beside too (Convert again, design 11:45Z)
			// our own earlier output may be there (the original changed and is converted again): it is
			// replaced once the new file passes its checks; a file we didn't make never is (design 01:04Z)
			if lst, err := os.Lstat(newName); err == nil && !w.ownOutput(newName, lst) && !w.setAsideable(newName, lst) {
				return "", failed(takenWords(plan)(newName), fs.ErrExist)
			}
		} else if _, err := os.Lstat(newName); err == nil {
			return "", failed(fmt.Sprintf("There is already a file named %s next to the original, so the new file was not made. Nothing was changed. Move or rename that file, then press Retry.", filepath.Base(newName)), fs.ErrExist)
		}
	} else if again != nil && OutputMode(plan.Output) != Replace {
		return "", failed("This library's workflow no longer replaces files, so this file can't be converted again from its kept original. Nothing was changed.", errors.New("convert again: not a replace"))
	} else if newName != job.Path && exists(newName) && (again == nil || newName != again.Dest()) { // a renaming replace never replaces another file (Convert again: but our own earlier one)
		return "", failed(rules.NameTaken(newName), fs.ErrExist)
	}
	// Subtitles saved as .srt files go beside the new file and never over another file: a name taken
	// already stops the job before anything is converted (checked again when they are put in place).
	for k := range plan.SubtitleFiles() {
		if name := plan.SrtNameFor(newName, k); exists(name) {
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
	// The local worker encodes with its graphics encoder for this plan's format when it can do this plan; else,
	// and for a remote worker, the processor (its own arguments are FFmpegArgs, unchanged).
	gpu := ""
	full := false // gpu runs the full GPU pipeline (decode and encode on the graphics card)
	fell := false // the graphics encoder failed and the processor is retrying the file
	fellReason := ""
	var gpuSlot *GPUSlot // the card's place this job holds while its graphics encode runs
	defer func() {
		if gpuSlot != nil {
			gpuSlot.Release()
		}
	}()
	args := plan.FFmpegArgs(srcPath, tmp)
	w.setPhase(secEncode) // the encoder choice and its notes belong to encode (log-spec section 4)
	chosenWhy := ""
	if w.remote == nil && w.Encoder != nil {
		need10 := tenBitSource(plan)
		if w.EncoderCandidates != nil && !plan.Video.Copy { // R5: what hardware is here, and each encoder considered
			for _, line := range w.EncoderCandidates(plan.Video.Codec, need10, plan.Video.HDR) {
				w.logLine(job.ID, secEncode, jobs.LevelInfo, line)
			}
		}
		want, note := w.Encoder(plan.Video.Codec, need10, plan.Video.HDR)
		if want != "" && !plan.Video.Copy {
			until, paused, err := w.Queue.EncoderPaused(w.ID)
			switch {
			case err == nil && paused:
				w.jobf(job.ID, ": %s is paused until %s after three failures in a row; using the processor", want, until.Local().Format("15:04"))
			default:
				if gargs, gfull, ok := w.graphicsArgs(ctx, job, plan, src, srcPath, tmp, want); ok {
					// The card's slots come first (user bug 11-20): a job waits while they are all in
					// use, so no two graphics jobs run on it at once beyond the limit. With none (the
					// limit at 0), the processor does the encoding, as it does for any plan the card
					// can't do.
					slot, serr := w.takeGPUSlot(ctx, job)
					if serr != nil { // stopped while it waited
						return "", serr
					}
					if slot != nil {
						gpuSlot = slot
						args, full = gargs, gfull
						gpu = want
						chosenWhy = want + " passed its test on this computer and can do this file"
					}
				} else {
					w.jobf(job.ID, ": encoder %s can't do this file; using the processor", want)
				}
			}
		} else if note != "" && !plan.Video.Copy {
			w.jobf(job.ID, ": %s", note) // why the processor: the Chosen line below follows it, without repeating it
		}
	}
	if !plan.Video.Copy && w.remote == nil { // R5's last line: the encoder the real encode uses, why, and its settings
		line := "Chosen: " + encoderWordsUsed(plan, gpu)
		if chosenWhy != "" {
			line += " — " + chosenWhy
		}
		w.logLine(job.ID, secEncode, jobs.LevelInfo, line+". Settings: "+encoderDetail(args))
	}
	if note := vbrAverageNote(plan.Video); note != "" {
		w.jobf(job.ID, ": %s", note)
	}
	setLive(job.ID, LiveJob{Worker: w.ID, Started: w.now(), ConvertingTo: convertingTo(plan, src, gpu), Encoder: encoderWordsUsed(plan, gpu),
		Decoder: decoderWords(src, full), Progress: media.Progress{ETASeconds: -1}})
	defer dropLive(job.ID)
	var encErr string
	var terr error
	decoded := int64(-1) // frames ffmpeg took from the original; -1 when a remote worker did not say
	encStart := w.now()
	if w.remote != nil { // V7b2: a worker on another computer converts and checks; its file comes up here
		stopWatch := w.watchRemoteStall(job.ID, w.remote)
		var ran []string
		encErr, ran, terr = w.remote.convert(ctx, w, job, snap, srcPath, tmp)
		stopWatch()
		if ran != nil { // the command the remote ran (its processor's after a graphics encoder fell back), not the server's plan
			args = ran
		}
		var k *KeptError
		if errors.As(terr, &k) || errors.Is(terr, errLeaseLost) {
			return "", terr
		}
	} else {
		encErr, decoded, terr = w.encodeLocal(ctx, job, args)
		// A failed graphics encode steps down a rung at a time: the full GPU pipeline to the processor decoding
		// for the same encoder (no count toward the pause), then to the processor, which counts (EncoderFell).
		for gpu != "" && w.gpuFailed(ctx, terr, encErr, full) {
			w.recordEncode(job.ID, args, encStart, encErr, terr, probeSummary(src))
			reason := firstLines(encErr, 1)
			if reason == "" {
				reason = terr.Error()
			}
			// Truncate, don't remove: the staged file is checked by identity (staged.intact), so the retry must
			// write into the same file, not a new one that may get another inode.
			if err := os.Truncate(tmp, 0); err != nil {
				encErr, terr = "", fmt.Errorf("clearing the partial output: %w", err)
				break
			}
			if full {
				full = false
				args, _ = plan.FFmpegArgsWith(srcPath, tmp, gpu)
				w.jobf(job.ID, ": full GPU pipeline failed (%s); the processor decodes for %s", shortReason(reason), gpu)
				setLive(job.ID, LiveJob{Worker: w.ID, Started: w.now(), ConvertingTo: convertingTo(plan, src, gpu), Encoder: encoderWordsUsed(plan, gpu),
					Decoder: decoderWords(src, false), Progress: media.Progress{ETASeconds: -1}})
				encStart = w.now()
				encErr, decoded, terr = w.encodeLocal(ctx, job, args)
				continue
			}
			if gpuSlot != nil { // the processor converts from here: the card is free for another job
				gpuSlot.Release()
			}
			fell, fellReason = true, shortReason(reason)
			args = plan.FFmpegArgs(srcPath, tmp)
			setLive(job.ID, LiveJob{Worker: w.ID, Started: w.now(), ConvertingTo: convertingTo(plan, src, ""), Encoder: encoderWords(plan),
				Decoder: decoderWords(src, false), Progress: media.Progress{ETASeconds: -1}})
			encStart = w.now()
			encErr, decoded, terr = w.encodeLocal(ctx, job, args)
			break
		}
		if gpuSlot != nil { // the graphics encode has ended: the card's place is free for the next job
			gpuSlot.Release()
		}
	}
	w.recordEncode(job.ID, args, encStart, encErr, terr, probeSummary(src))
	// Moved or deleted while converting: ffmpeg's read errors then say nothing about the file, so it is
	// neither damaged nor the encoder's fault. Checked before the errors are read as damage.
	if ctx.Err() == nil && sourceGone(srcPath) {
		return "", failed(goneWords, errors.Join(terr, errors.New("the original is no longer at its path")))
	}
	if fell && terr == nil { // the processor converted the file, so the graphics encoder was the fault: it counts
		w.jobNote(job.ID, secEncode, jobs.LevelWarning, "%s failed (%s); converted on the processor", gpu, fellReason)
		if paused, err := w.Queue.EncoderFell(job.ID, w.ID, gpu, fellReason); err != nil {
			w.jobf(job.ID, ": could not record the fall-back: %v", err)
		} else if paused {
			w.jobf(job.ID, ": %s failed three times in a row, so it is paused for an hour", gpu)
		}
	}
	if gpu != "" && !fell && terr == nil { // the graphics encoder converted the file: its run of failures ends
		if err := w.Queue.EncoderWorked(w.ID); err != nil {
			w.jobf(job.ID, ": could not record the encoder's success: %v", err)
		}
	}
	if terr == nil {
		ranOn := gpu // the encoder that made the new file
		if fell {
			ranOn = "" // the processor converted it after the card failed
		}
		w.Queue.AddStep(job.ID, "converted", true, "Converted: "+convertingTo(plan, src, ranOn)+".")
	}
	if terr != nil {
		if errors.Is(terr, context.Canceled) || ctx.Err() != nil {
			return "", terr // stopped or shutting down: RunOne decides, not a failure of the file
		}
		if errors.Is(terr, syscall.ENOSPC) || strings.Contains(encErr+terr.Error(), "No space left on device") {
			return "", retryable(failed(diskFull, terr), retrySpace)
		}
		if words := headerWords(encErr); words != "" { // the new file's type can't hold a track: not damage
			return "", failed(words, fmt.Errorf("%w: %s", terr, firstLines(encErr, 3)))
		}
		ferr := failed("Converting stopped with an error. Your original was not touched. Press Retry; if it keeps failing, the file may be damaged.", fmt.Errorf("%w: %s", terr, firstLines(encErr, 3)))
		if killedBySignal(terr) { // stopped by the system (out of memory...), not a problem of the file
			return "", retryable(ferr, retryGeneral)
		}
		return "", ferr
	}
	// Errors ffmpeg reported while reading the original mean it is damaged: converting it would bake
	// the damage in and delete the only copy that might still be repaired or fetched again.
	frames, exact := sourceFrameCount(src)
	lost := frames
	if !exact {
		lost = 0 // a count from the container's duration is not evidence of lost frames: unknown is not damage
	}
	if lost > 0 && decoded >= 0 && decoded < frames && src.MainVideoOrdinal() >= 0 {
		// Pictures before the first keyframe of a stream that starts mid-group-of-pictures cannot be decoded in any
		// file: they are not lost frames. They count as accounted for; damage after the first keyframe still counts.
		lctx, lcancel := context.WithTimeout(ctx, quickTimeout)
		lead, lerr := media.LeadingUndecodable(lctx, w.Exec, w.FFprobe, srcPath, src.MainVideoOrdinal())
		lcancel()
		if lerr != nil {
			log.Printf("worker %s: %s: reading the first keyframe: %v", w.ID, job.Path, lerr) // unknown: counts stay strict
		} else {
			decoded += lead
		}
	}
	if dmg := withLostFrames(sourceDamage(encErr), lost, decoded); dmg.Found() {
		if !damageTolerated(dmg, frames) {
			// The check step records the refusal (its Result is the kept reason), then the lines the UI shows.
			const reason = "the original file has errors, so it was not converted (converting would keep the damage)"
			errorsLine := "Errors: the file as a whole is damaged; that is never tolerated"
			if !dmg.FileLevel {
				errorsLine = fmt.Sprintf("Errors: %s; up to %d%% are tolerated", damagedShare(dmg.Pictures, frames), maxDamagedPercent)
			}
			w.Queue.RecordRunStep(job.ID, jobs.RunStep{Index: 1, Name: "check", StartedAt: w.now().UTC().Format(time.RFC3339),
				Outcome: "failed", Result: reason})
			w.Queue.AppendLogLine(job.ID, 1, secOutput, jobs.LevelInfo, "check", "Check: damage in the original, read while converting")
			w.Queue.AppendLogLine(job.ID, 1, secOutput, jobs.LevelInfo, "check", errorsLine)
			for _, l := range dmg.Lines {
				w.Queue.AppendLogLine(job.ID, 1, secOutput, jobs.LevelInfo, "check", l)
			}
			log.Printf("worker %s: %s: the original has errors: %s", w.ID, job.Path, strings.Join(dmg.Lines, " | "))
			return "", kept(reason)
		}
		w.Queue.AppendLogLine(job.ID, 0, secOutput, jobs.LevelWarning, "app", fmt.Sprintf("The original has a little damage (%s, up to %d%% are tolerated); converted anyway.",
			damagedShare(dmg.Pictures, frames), maxDamagedPercent))
	}

	// If the source changed while we encoded, the new file is stale: say so first (otherwise the
	// checks would report a confusing mismatch), and check again right before replacing.
	if err := sourceUnchanged(srcPath, before, beforeState); err != nil {
		return "", err
	}
	if err := stage.intact(); err != nil {
		return "", err
	}
	w.check = nil
	w.verified = media.Info{} // the new file's probe is only what this check read (a failed probe leaves it empty)
	w.setPhase(secOutput)
	checkStart := w.now()
	verr := w.verify(ctx, src, exp, srcPath, tmp, before.Size())
	w.recordCheck(job.ID, checkStart, verr)
	if err := verr; err != nil {
		var k *KeptError
		if errors.As(err, &k) && plan.Video.PeakLimitKbps > 0 && strings.HasPrefix(k.Reason, pictureTooDifferent) {
			// design (SCREENS V3 step 3): VBR's own words; the measured similarity goes to the run log
			w.jobf(job.ID, ": %s", k.Reason)
			k.Reason = keptVBRWords(plan.Video)
		}
		if errors.As(err, &k) {
			w.recordCheckFailure(job.ID, checkStart, k, args, src)
		}
		return "", err
	}
	for k := range plan.SubtitleFiles() { // each subtitle that was to be saved as a file was saved
		if st, err := os.Lstat(rules.StagedSrt(tmp, k)); err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
			return "", kept("a subtitle could not be saved as a separate file")
		}
	}
	// R8: a new file bigger than its original is kept when the workflow says so, before anything moves.
	if err := w.sizeRule(job.ID, plan, tmp, before.Size()); err != nil {
		return "", err
	}
	// The original's permissions, only now that the file has been checked.
	if err := stage.intact(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, before.Mode().Perm()); err != nil {
		w.recordMove(job.ID, "chmod", tmp, tmp, err)
		w.logLine(job.ID, secReplace, jobs.LevelWarning, fmt.Sprintf("Could not give the new file the source's permissions (%04o): %v. Your original was not touched.",
			before.Mode().Perm(), err))
		return "", fmt.Errorf("temp file permissions: %w", err)
	}
	w.recordMove(job.ID, "chmod", tmp, tmp, nil)
	w.logLine(job.ID, secReplace, jobs.LevelInfo, fmt.Sprintf("Gave the new file the source's permissions (%04o).", before.Mode().Perm()))
	// Its modification time and owner too (slice 23), where the system allows: media servers and
	// backups key on the time, and the file stays the user's. Set before the swap, so the identity the
	// swap records is the one the file keeps.
	if err := os.Chtimes(tmp, time.Now(), before.ModTime()); err != nil {
		w.jobf(job.ID, ": could not keep the modification time: %v", err)
	}
	if outcome, oerr := setOwner(tmp, before); outcome != ownerNothing { // R14: the log says what the owner came to
		level := jobs.LevelInfo
		if outcome != ownerKept {
			level = jobs.LevelWarning
		}
		w.logLine(job.ID, secReplace, level, ownerWords(outcome, oerr))
		w.recordMove(job.ID, "chown", tmp, tmp, oerr)
	}
	// On disk before any rename (R1): after a power cut the name never points at data that wasn't written.
	if err := syncFile(tmp); err != nil {
		return "", retryable(failed("The new file could not be saved to disk, so your original was kept as it was. Press Retry.", fmt.Errorf("sync the new file: %w", err)), retryGeneral)
	}
	for k := range plan.SubtitleFiles() {
		if err := syncFile(rules.StagedSrt(tmp, k)); err != nil {
			return "", retryable(failed("A subtitle file could not be saved to disk, so your original was kept as it was. Press Retry.", fmt.Errorf("sync a subtitle file: %w", err)), retryGeneral)
		}
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
		if err := sourceUnchanged(srcPath, before, beforeState); err != nil {
			return err
		}
		if err := w.stillHolds(job.ID); err != nil { // V7a: a worker that lost its lease never puts a file in place
			return err
		}
		return stage.intact()
	}
	w.setPhase(secReplace)
	placeStarted := w.now()
	placed, err := w.installSubtitles(plan, job.ID, tmp, final)
	if err != nil {
		return "", w.placeError(job.ID, placeStarted, final, err)
	}
	if len(placed) > 0 {
		if err := hook("subtitles placed"); err != nil {
			return "", err // a crash here: RecoverSidecars removes them at the next start
		}
	}
	removePlaced := func() { // the swap did not happen: the .srt files we put beside it go again
		w.removeSidecars(job.ID, placed)
	}
	swapped := func() { // the swap is done: the .srt files stay, the job keeps their names; their record goes
		if w.Sidecars != nil {
			w.Sidecars.FinishSidecars(job.ID)
		}
	}
	days := 0
	if w.KeepDays != nil {
		days = w.KeepDays()
	}
	if again != nil { // Convert again: our earlier file goes, the original stays kept (its days start again)
		if err := w.replaceOwnOutput(ctx, *again, stage, final, days, stillOK); err != nil {
			removePlaced()
			return "", w.placeError(job.ID, placeStarted, final, err)
		}
		swapped()
		w.replaceStep(job.ID, placeStarted, false, "")
		return final, nil
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
			return "", w.placeError(job.ID, placeStarted, final, err)
		}
		swapped()
		w.replaceStep(job.ID, placeStarted, false, "")
		return final, nil
	}
	move := moveIntoPlace
	switch {
	case OutputMode(plan.Output) == Folder:
		move = func(ctx context.Context, tmp, final string, stillOK func() error) error {
			return w.moveIntoFolder(ctx, job.ID, tmp, final, stillOK) // never over a file we didn't make
		}
	case keepsOriginal(plan):
		move = func(ctx context.Context, tmp, final string, stillOK func() error) error {
			return w.moveBeside(ctx, job.ID, tmp, final, stillOK) // never over another file, but our own earlier one (Convert again)
		}
	}
	if err := move(ctx, tmp, final, stillOK); err != nil {
		w.recordMove(job.ID, "move", tmp, final, err)
		removePlaced()
		var f *FailedError
		if errors.As(err, &f) {
			return "", w.placeError(job.ID, placeStarted, final, err)
		}
		var k *KeptError
		if errors.As(err, &k) {
			return "", err
		}
		return "", w.placeError(job.ID, placeStarted, final, retryable(failed("The new file could not be put in place, so your original was kept as it was. Press Retry.", fmt.Errorf("move result into place: %w", err)), retryGeneral)) // busy or locked
	}
	w.recordMove(job.ID, "move", tmp, final, nil)
	w.logLine(job.ID, secReplace, jobs.LevelInfo, "Moved the new file into place: "+final)
	swapped()
	w.replaceStep(job.ID, placeStarted, false, "")
	return final, nil
}

// SidecarJournal records subtitles saved as .srt files before they are put beside a new file
// (jobs.Store), so a crash in between leaves nothing of ours behind (RecoverSidecars).
type SidecarJournal interface {
	AddSidecars([]jobs.Sidecar) error
	FinishSidecars(jobID int64) error
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
	for k := range files {
		staged := rules.StagedSrt(tmp, k)
		st, err := os.Stat(staged)
		if err != nil {
			return nil, err
		}
		w.addBytes(st.Size())      // a .srt file the job wrote is bytes written too (L2)
		h, err := fullHash(staged) // .srt files are small: their whole content identifies them
		if err != nil {
			return nil, err
		}
		recs = append(recs, jobs.Sidecar{JobID: jobID, Path: plan.SrtNameFor(final, k), Bytes: st.Size(),
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
			w.recordMove(jobID, "write_sidecar", rules.StagedSrt(tmp, k), c.Path, err)
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
		w.recordMove(jobID, "write_sidecar", rules.StagedSrt(tmp, k), c.Path, nil)
		w.logLine(jobID, secReplace, jobs.LevelInfo, "Wrote the subtitle file "+c.Path+" next to the new file.")
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
			w.recordMove(jobID, "remove", c.Path, "", err)
			w.jobNote(jobID, secReplace, jobs.LevelWarning, "Could not remove the subtitle file %s we had put there: %v. It is kept on record and removed at the next start; delete it by hand if it stays.", c.Path, err)
			continue
		}
		w.recordMove(jobID, "remove", c.Path, "", nil)
		w.logLine(jobID, secReplace, jobs.LevelInfo, "Removed the subtitle file "+c.Path+" we had put there, because the new file was not put in place.")
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

// goneWords: the original was moved or deleted while it was converted. A failure, never a damaged file.
const goneWords = "The file was moved or deleted while it was being converted."

// headerWords is the message for ffmpeg stopping before it wrote the new file's header: its type can't
// hold a track as the plan maps it in (user bug 11-20). ffmpeg's own sentence about what is supported,
// when it gives one, else plain words. "" when the error is not a header refusal, so a damaged file is
// never called a file type's limit.
func headerWords(encErr string) string {
	if !strings.Contains(encErr, "Could not write header") {
		return ""
	}
	why := ""
	for _, line := range strings.Split(encErr, "\n") {
		if !strings.Contains(line, "supported for") {
			continue
		}
		if i := strings.Index(line, "] "); i >= 0 { // "[webm @ 0x...] Only VP8 ... are supported for WebM."
			why = strings.TrimSpace(line[i+2:])
		}
	}
	if why == "" {
		return "ffmpeg couldn't write the new file. Your original was not touched."
	}
	return "ffmpeg couldn't write the new file: " + why + " Your original was not touched."
}

// sourceGone says whether nothing is at the original's path any more. Only a missing name counts: a
// different file put in its place is a change (kept), and any other error proves nothing about the file.
func sourceGone(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, fs.ErrNotExist)
}

// sourceUnchanged refuses the new file if the original is no longer the file that was read: a
// different file now (another device or inode, or Windows file ID), or the same file with another
// size, modification time or (where the system records one) change time. The change time catches
// an edit that keeps the size and puts the modification time back, as some tag editors do.
func sourceUnchanged(path string, before os.FileInfo, beforeState media.State) error {
	now, err := os.Stat(path)
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		return failed(goneWords, err)
	}
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

// moveIntoFolder puts the new file in the folder for new files (it passed its checks): over our own
// earlier output of the same file, which goes now, but never over a file we didn't make (design 01:04Z).
func (w *Worker) moveIntoFolder(ctx context.Context, jobID int64, tmp, final string, stillOK func() error) error {
	return w.moveOverOwn(ctx, jobID, tmp, final, stillOK, notOurs)
}

// moveBeside is moveIntoFolder for the new file next to the original (slice 18, Convert again).
func (w *Worker) moveBeside(ctx context.Context, jobID int64, tmp, final string, stillOK func() error) error {
	return w.moveOverOwn(ctx, jobID, tmp, final, stillOK, besideTaken)
}

// maxNameLen is the longest name this worker makes from a {{name}} template: ext4 refuses a piece over
// 255 bytes and Windows one over 255 characters, and 200 bytes fits both (a UTF-8 byte is at least one
// UTF-16 unit), so a name this long is one both hold.
const maxNameLen = 200

// nameTooLong reports a name the final rename would refuse after the conversion had run (review2-504 P3).
func nameTooLong(name string) bool { return len(name) > maxNameLen }

// tooLongWords says which setting made a name too long, in plain words, before anything is converted.
func tooLongWords(field, name string) string {
	return fmt.Sprintf("The name from “%s” would be %d characters long, and a file name can be at most %d, so nothing was converted. Nothing was changed. Shorten that setting in the workflow's Save the result step, then press Retry.",
		field, len(name), maxNameLen)
}

// takenWords is the failure for a file we didn't make at the new file's name.
func takenWords(plan rules.Plan) func(string) string {
	if OutputMode(plan.Output) == Beside {
		return besideTaken
	}
	return notOurs
}

// besideTaken is the failure for a file we didn't make at the name beside the original.
func besideTaken(name string) string {
	return fmt.Sprintf("There is already a file named %s next to the original, so the new file was not put there. Nothing was changed. Move or rename that file, then press Retry.", filepath.Base(name))
}

// moveOverOwn renames tmp to final over our own earlier output there (it goes now), never over a file
// we didn't make (taken says so). Our earlier output that changed since the job made it is moved aside first.
func (w *Worker) moveOverOwn(ctx context.Context, jobID int64, tmp, final string, stillOK func() error, taken func(string) string) error {
	return moveNoClobber(ctx, tmp, final, func() error {
		if err := stillOK(); err != nil {
			return err
		}
		if lst, err := os.Lstat(final); err == nil {
			if w.ownOutput(final, lst) {
				rerr := os.Remove(final) // the older generation: the original is still in the library
				w.recordMove(jobID, "remove", final, "", rerr)
				if rerr == nil {
					w.logLine(jobID, secReplace, jobs.LevelInfo, "Removed our earlier converted file at "+final+"; the source is still in the library.")
				}
				return rerr
			}
			if w.setAsideable(final, lst) {
				return w.setAside(jobID, final)
			}
		}
		return nil
	}, taken)
}

// setAsideable says whether the regular file at p (lst its Lstat) is the output a done job of ours wrote at p,
// changed since (its size or time is not the one recorded). Convert again moves such a file aside (setAside).
func (w *Worker) setAsideable(p string, lst fs.FileInfo) bool {
	if !lst.Mode().IsRegular() {
		return false
	}
	_, ok, err := w.Queue.OutputOwner(p)
	return err == nil && ok
}

// setAside moves our earlier output at final, changed since the job made it, into the .syp-trash beside it, so
// the new file takes its name and the earlier one stays restorable (its name keeps the id of the job that made it).
// The job log says so. A failure leaves the earlier output where it was.
func (w *Worker) setAside(jobID int64, final string) error {
	name := filepath.Base(final)
	refused := func(err error) error {
		return failed(fmt.Sprintf("Your earlier converted file %s had been changed since it was made, and it could not be moved to the trash, so nothing was changed. Move or rename it, then press Retry.", name), err)
	}
	owner, ok, err := w.Queue.OutputOwner(final)
	if err == nil && !ok {
		err = errors.New("no done job made this file")
	}
	if err != nil {
		return refused(err)
	}
	dir := filepath.Join(filepath.Dir(final), TrashDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return refused(err)
	}
	dst, err := trashPath(dir, name, owner)
	if err != nil {
		return refused(err)
	}
	if err := os.Rename(final, dst); err != nil {
		w.recordMove(jobID, "move", final, dst, err)
		return refused(err)
	}
	w.recordMove(jobID, "move", final, dst, nil)
	syncDirs(final, dst)
	w.movedAside = fmt.Sprintf("Your earlier converted file %s had been changed since it was made, so it was moved to the trash (%s) and the new file took its place.", name, filepath.Join(TrashDir, filepath.Base(dst)))
	w.Queue.AppendLogLine(jobID, 0, secReplace, jobs.LevelWarning, "app", w.movedAside)
	return nil
}

// withMovedAside adds the trash sentence to a step's words when this job moved our earlier output aside.
func (w *Worker) withMovedAside(words string) string {
	if w.movedAside == "" {
		return words
	}
	return words + " " + w.movedAside
}

// ownOutput reports whether the regular file at p (lst its Lstat) is one a done job of ours wrote.
func (w *Worker) ownOutput(p string, lst fs.FileInfo) bool {
	if !lst.Mode().IsRegular() {
		return false
	}
	own, err := w.Queue.IsOwnOutputExact(p, lst.Size(), lst.ModTime().UnixNano())
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
			syncDirs(a, b)
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

// keptVBRWords is the kept-original reason for a VBR file whose picture was too different (review a772397
// item 1): it names the max that actually set the file's bitrate, the person's limit or the level's figure.
func keptVBRWords(v rules.VideoEncode) string {
	kbps := v.TargetKbps // the level's figure: MaxRateKbps can be the source's cap in a saved VBR flow
	if v.PeakLimitBinds() {
		kbps = v.PeakLimitKbps
	}
	return "Kept the original: under a " + rules.KbpsWords(kbps) + " kbps max bitrate the picture would suffer."
}

// vbrAverageNote is the run-log line for a file held at its max all the way through (review a772397 item 3):
// "" unless the average and the max are the same.
func vbrAverageNote(v rules.VideoEncode) string {
	if !v.AverageIsMax() {
		return ""
	}
	return "Average and max bitrate are both " + rules.KbpsWords(v.TargetKbps) + " kbps, so the file runs at the max throughout."
}

// keepsOriginal is whether the plan leaves the original where it is (beside, or another folder, S6).
func keepsOriginal(plan rules.Plan) bool {
	return OutputMode(plan.Output) == Beside || OutputMode(plan.Output) == Folder
}

// FolderPath is where "Save to another folder" (S6) puts the new file of src (in library lib): the same
// sub-folders under dir, the same name with ext.
func FolderPath(dir, lib, src, ext string) string {
	return FolderPathAs(dir, lib, src, strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))+ext)
}

// FolderPathAs is FolderPath with the new file's own name (W4b: the Save the result step's template).
func FolderPathAs(dir, lib, src, name string) string {
	if rel, err := filepath.Rel(lib, filepath.Dir(src)); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Join(dir, rel, name)
	}
	return filepath.Join(dir, name)
}

// distinctNames is names without repeats, in their first order (the job log's empty variables line).
func distinctNames(names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// applyFailure does what the path chose for when its step (f) doesn't work (V6b): try again later, leave
// the file alone, or go to another path from the original. false: stop (the job fails or is kept as it
// would have, but without a retry).
func (w *Worker) applyFailure(job jobs.Job, err error, f *rules.Failure) (bool, error) {
	plain := plainReason(err)
	act := f.Action
	if act == "retry" {
		if job.Attempts+1 < f.Max {
			wait := time.Duration(f.BackoffSeconds) * time.Second
			w.jobf(job.ID, " (%s): %s didn't work, try %d of %d, again in %s: %v", job.Path, f.Name, job.Attempts+1, f.Max, wait, err)
			w.Queue.AddFailureStep(job.ID, f.NodeID, retryNote(job.Attempts+1, f.Max, plain, wait))
			return true, w.Queue.RetryLater(job.ID, w.now().Add(wait), reasonCode(err), plain)
		}
		act = f.Then // the last try
	}
	switch act {
	case "skip":
		w.Queue.AddFailureStep(job.ID, f.NodeID, f.Name+" didn't work: "+plain)
		w.jobf(job.ID, " (%s): %s didn't work, the file is left as it is: %v", job.Path, f.Name, err)
		return true, w.Queue.Skip(job.ID, f.SkipWords, rules.SkipLeftByWorkflow)
	case "fallback":
		if f.Fallback == nil {
			return false, nil
		}
		if f.Fallback.Then == nil { // the other path is "Leave the file alone"
			w.Queue.AddFailureStep(job.ID, f.NodeID, f.Name+" didn't work: "+plain)
			return true, w.Queue.Skip(job.ID, f.Fallback.End, rules.SkipLeftByWorkflow)
		}
		b, _ := json.Marshal(f.Fallback)
		w.jobf(job.ID, " (%s): %s didn't work, trying the other path from the original: %v", job.Path, f.Name, err)
		return true, w.Queue.FallBack(job.ID, string(b), f.Fallback.Path, f.NodeID, f.Name+" didn't work: "+plain+" Trying the other path.")
	}
	return false, nil // stop
}

// LeaseRenewEvery is how often a worker renews its running job's lease (well inside jobs.LeaseFor).
var LeaseRenewEvery = 10 * time.Second

// renewLease renews job id's lease until done is closed (V7a).
func (w *Worker) renewLease(id int64, lease string, done <-chan struct{}) {
	if lease == "" {
		return
	}
	t := time.NewTicker(LeaseRenewEvery)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			if held, err := w.Queue.RenewLease(id, lease); err == nil && !held {
				w.jobf(id, ": this worker no longer holds the job (it was given back to the queue)")
				return
			}
		}
	}
}

// errLeaseLost: the job was taken back from this worker; nothing is put in place.
var errLeaseLost = errors.New("the job's lease was lost")

// stillHolds fails when this worker no longer holds job id's lease: it was put back in the queue (the
// worker had stopped answering), so the worker must not put anything in place.
func (w *Worker) stillHolds(id int64) error {
	w.mu.Lock()
	lease := w.lease
	w.mu.Unlock()
	if lease == "" {
		return nil
	}
	held, err := w.Queue.HoldsLease(id, lease)
	if err != nil {
		return err
	}
	if !held {
		return failed("This file was given back to the queue while this worker was busy, so nothing was put in place. The original was not touched.", errLeaseLost)
	}
	return nil
}

// ffmpegExit tells, from the error of a failed encode, whether ffmpeg ran and what it exited with: a run that ended
// with an exit status ran. On POSIX a signal ends a run with no status (-1, so no code); Windows has no signals and
// reports the process's exit code. A program that could not be started did not run.
func ffmpegExit(err error) (ran bool, exit int) {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return true, ee.ExitCode()
	}
	return false, -1
}

// CheckScore is the picture check's score for a job and the least it needed (the job's check step).
type CheckScore struct {
	Score, Min float64
	Passed     bool
}

// recordCheck keeps the job's check step: the picture score, when the check ran (w.check, set by the picture comparison).
func (w *Worker) recordCheck(id int64, started time.Time, verr error) {
	if w.Queue == nil || w.check == nil {
		return
	}
	c := w.check
	outcome := "ok"
	if verr != nil {
		outcome = "failed"
	}
	passed := c.Passed
	w.Queue.RecordRunStep(id, jobs.RunStep{Index: 1, Name: "check", StartedAt: started.UTC().Format(time.RFC3339),
		TookMs: w.now().Sub(started).Milliseconds(), Outcome: outcome, CheckScore: &c.Score, CheckMin: &c.Min, CheckPassed: &passed})
}

// recordCheckFailure keeps the check step of a refused conversion: the plain reason and the job log's detail (what
// was checked, how far playback got, the errors, and the encode and both files). It sets no failure summary: a
// kept job is not a failed one, and the drawer would call a summary "Why it failed".
func (w *Worker) recordCheckFailure(id int64, started time.Time, ke *KeptError, args []string, src media.Info) {
	if w.Queue == nil {
		return
	}
	newFile := "not read"
	if w.verified.DurationSec > 0 {
		newFile = fileSummary(w.verified)
	}
	detail := append(append([]string(nil), ke.Detail...), encoderDetail(args), "Original: "+fileSummary(src), "New file: "+newFile)
	if w.check == nil { // the picture check did not run: this is the job's check step
		w.Queue.RecordRunStep(id, jobs.RunStep{Index: 1, Name: "check", StartedAt: started.UTC().Format(time.RFC3339),
			TookMs: w.now().Sub(started).Milliseconds(), Outcome: "failed", Result: ke.Reason})
	}
	for _, l := range detail {
		w.Queue.AppendLogLine(id, 1, secOutput, jobs.LevelInfo, "check", l)
	}
}

// encoderDetail is the job log's encoder line: the video encoder, its quality setting and the hardware used.
func encoderDetail(args []string) string {
	argOf := func(flag string) string {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == flag || strings.HasPrefix(args[i], flag+":") { // -c:v also matches -c:v:0
				return args[i+1]
			}
		}
		return ""
	}
	quality := "none"
	// -qp_i is AMF's intra quality (-rc cqp -qp_i <n> -qp_p <n>): without it an AMF job said "none".
	for _, f := range []string{"-global_quality", "-crf", "-cq", "-qp", "-qp_i", "-b:v"} {
		if v := argOf(f); v != "" {
			quality = f + " " + v
			break
		}
	}
	hw := argOf("-init_hw_device")
	if hw == "" {
		hw = argOf("-hwaccel")
	}
	if hw == "" {
		hw = "none"
	}
	return fmt.Sprintf("Encoder: %s, %s, hardware: %s", argOf("-c:v"), quality, hw)
}

// fileSummary is a file in a few plain words for the job log: "hevc 1920x1080 23.976 fps 10-bit, 1 audio, 2 subtitles, 0:44:12".
func fileSummary(info media.Info) string {
	var parts []string
	if v := info.Video(); v != nil {
		s := fmt.Sprintf("%s %dx%d", v.Codec, v.Width, v.Height)
		if v.FrameRate > 0 {
			s += fmt.Sprintf(" %.3f fps", v.FrameRate)
		}
		if v.BitDepth > 0 {
			s += fmt.Sprintf(" %d-bit", v.BitDepth)
		}
		parts = append(parts, s)
	}
	parts = append(parts, fmt.Sprintf("%d audio", len(info.Audio())), fmt.Sprintf("%d subtitles", len(info.Subtitles())), rules.Clock(info.DurationSec))
	return strings.Join(parts, ", ")
}

// encoderReasonMax caps the reason a fall-back keeps, as the remote path's notes are capped.
const encoderReasonMax = 300

// shortReason is reason cut to encoderReasonMax characters.
func shortReason(reason string) string {
	if r := []rune(reason); len(r) > encoderReasonMax {
		return string(r[:encoderReasonMax])
	}
	return reason
}

// graphicsArgs picks the pipeline for the graphics encoder want: the full GPU pipeline when its family decodes
// the source here (full is true), else the processor decodes and the encoder encodes. ok is false when neither
// can do this plan. The job log says which, and why the full pipeline is not used.
func (w *Worker) graphicsArgs(ctx context.Context, job jobs.Job, plan rules.Plan, src media.Info, srcPath, tmp, want string) (args []string, full, ok bool) {
	fam := rules.GraphicsFamily(want)
	if v := src.Video(); v != nil && w.GraphicsDecodes != nil && (fam == "qsv" || fam == "vaapi") {
		if w.GraphicsDecodes(ctx, fam, v.Codec, tenBitSource(plan)) { // one rule for 10-bit, here and at the encoder
			if args, full = plan.FFmpegArgsFullGPU(srcPath, tmp, want); full {
				w.jobf(job.ID, ": decoding and encoding on the graphics card (full GPU pipeline)")
				return args, true, true
			}
		} else {
			w.jobf(job.ID, ": %s can't decode %s here; the processor decodes", fam, codecName(v.Codec))
		}
	}
	args, ok = plan.FFmpegArgsWith(srcPath, tmp, want)
	return args, false, ok
}

// gpuFailed says whether a graphics encode that ended with err should convert again on the processor. Off the full
// GPU pipeline, only an encoder or ffmpeg failure (jobs.FallsBackToProcessor). On the full rung the graphics card
// also decodes, and a decode error there can be the card's, not the file's (a profile or chroma layout it cannot
// read), so any failure steps down except a stop (cancel, shutdown, the stall stop), a lost lease, a full disk, a
// missing source or an unwritable output, which the processor would hit too.
func (w *Worker) gpuFailed(ctx context.Context, err error, stderr string, full bool) bool {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, errLeaseLost) {
		return false
	}
	w.mu.Lock()
	stalled := w.stopped == stopStall
	w.mu.Unlock()
	if stalled {
		return false
	}
	lines := strings.Split(stderr, "\n")
	if full {
		switch jobs.FailureCode(jobs.ReasonConvertFailed, lines) {
		case "no_space", "source_missing", "output_not_writable":
			return false
		}
		return true
	}
	return jobs.FallsBackToProcessor(lines)
}

// encodeLocal runs ffmpeg for the local worker's job: its progress, stall watch and live figures.
func (w *Worker) encodeLocal(ctx context.Context, job jobs.Job, args []string) (string, int64, error) {
	var encErr string
	var terr error
	frames := int64(-1) // the last frame count ffmpeg reported
	wrote := int64(0)   // the most bytes it reported writing in this attempt (the run counters, L2)
	stopWatch := w.watchStall(job.ID)
	w.quietOn.Store(true) // R9's quiet warning: the stall watcher checks it with this clock
	w.outputSeen(w.now())
	defer w.quietOn.Store(false)
	var tenths progressLines
	at := -1.0       // the furthest position ffmpeg has reached
	moved := w.now() // when it last moved (a warning says so after stallWarnAfter, R9)
	stalled := false // the warning has been written; one line says when it moves again
	encErr, terr = media.TranscodeLines(ctx, w.Exec, w.FFmpeg, args, job.DurationSec, func(p media.Progress) {
		// the encode is alive while its position moves, or while its frame count or bytes written grow and the output
		// stays within twice the source's size (a runaway output past that is not progress). out_time follows the
		// most-behind stream, so it freezes in a subtitle gap while frames still go on (bug 2).
		posMoved := p.OutTimeUS > at // a position further on: one that repeats is not progress
		outputOK := job.SizeBefore > 0 && p.TotalSize <= 2*job.SizeBefore
		alive := posMoved || (outputOK && (p.Frame > frames || p.TotalSize > wrote))
		frames = p.Frame
		if p.TotalSize > wrote {
			wrote = p.TotalSize
		}
		if posMoved {
			at = p.OutTimeUS
			w.finishing.Store(reachedEnd(p.OutTimeUS, job.DurationSec))
		}
		if alive {
			w.progressAt.Store(time.Now().UnixNano())
			moved = w.now()
			if stalled { // R9: the log says it is moving again
				stalled = false
				w.logLine(job.ID, secEncode, jobs.LevelInfo, "The conversion is moving again.")
			}
		} else if !stalled && w.now().Sub(moved) >= stallWarnAfter { // R9: frozen media time, an ordinary warning
			stalled = true
			w.logLine(job.ID, secEncode, jobs.LevelWarning,
				fmt.Sprintf("The conversion has not moved for %s: ffmpeg may be stuck, or the file may be slow to read.", stallWarnAfter))
		}
		if job.DurationSec <= 0 {
			return // no length: no percent to show
		}
		w.Queue.Progress(job.ID, p.Percent)
		updateLive(job.ID, p)
		if line := tenths.next(p); line != "" {
			w.jobf(job.ID, ": %s", line)
		}
	}, func() { w.outputSeen(w.now()) })
	stopWatch()
	w.addPass()       // an ffmpeg run the job needed, whatever came of it (a retry is another)
	w.addBytes(wrote) // what this attempt wrote
	return encErr, frames, terr
}

// probeSource is ffprobe's view of the original, from the scanner's probe cache when the file has the same size and
// modification time now (a fresh stat, not the one from the scan).
func (w *Worker) probeSource(ctx context.Context, path string) (media.Info, error) {
	st, err := os.Stat(path)
	if err != nil {
		return media.Info{}, err
	}
	return scanner.Probe(ctx, w.Queue, w.Exec, w.FFprobe, path, st)
}
