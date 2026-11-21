// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Remote conversion (V7b2, slices/v2-engine/v7-plan.md "V7b design"): a job leased by a worker on
// another computer runs here, on the server, through the same process() as a local one: the same
// pre-checks, the Plan made and recorded here, the same commit with its lease fence. Only the encode
// differs: the plan goes to the remote worker, which converts and runs every check (the full picture
// check too: Q4 a), and the file it uploads takes the encode's place. Then the server re-checks it
// (its shape, a sampled decode; the upload's checksum was checked on arrival) and puts it in place.

// RemotePlan is what a remote worker needs to convert: the Plan with what the result is checked
// against, and the file to read (the original, or its kept copy for Convert again).
type RemotePlan struct {
	Snapshot rules.Snapshot
	Source   string // on the server; the worker downloads it
	// Stage is the staged new file on the destination's disk (empty, made by the run): the upload is
	// written into it, so the commit is a rename of the very file that was checked (staged.intact).
	Stage string
}

// RemoteResult is a remote worker's answer: its checked file is in the plan's Stage (its extracted
// subtitles at their staged names), or why it didn't work.
type RemoteResult struct {
	Stderr  string               // what its ffmpeg said (damage in the original shows there)
	Command []string             // the ffmpeg arguments it ran: the job log records these, not the server's plan
	Kept    string               // its checks said no: the original is kept, as a local check would
	Err     error                // it failed to convert
	Code    string               // its failure's code when it is not the general one (slice 15: settings_rejected); "" from an older worker
	Steps   []jobs.RunStep       // the run steps (encode, probe, check) with the remote's own timings
	Failure *jobs.FailureSummary // the remote's failure summary (exit code, key lines, etc.)
}

// remoteFailure is a remote worker's conversion failure with the code its result carried: "" from an
// older worker, whose failure keeps today's code (slice 15).
type remoteFailure struct {
	code string
	err  error
}

func (e *remoteFailure) Error() string { return e.err.Error() }
func (e *remoteFailure) Unwrap() error { return e.err }

// remoteFailureCode is the code a remote worker's result carried. ok is true for a remote failure even
// when the code is "" (an older worker): the server does not work one out from its stderr then.
func remoteFailureCode(err error) (string, bool) {
	var rf *remoteFailure
	if errors.As(err, &rf) {
		return rf.code, true
	}
	return "", false
}

// RemoteJob joins the server's run of a leased job with the remote worker converting it.
type RemoteJob struct {
	Lease   string
	mu      sync.Mutex
	plan    *RemotePlan
	job     int64 // the job the plan is for: its live row shows the encoder the remote worker uses
	planned chan struct{}
	result  chan RemoteResult
	ended   chan struct{}
	endErr  error
	// the remote encode, for the stall stop (row 17:40Z, orch-lead 19:13Z): judged only between the
	// worker's "converting" (with its position) and its next stage, and only for a worker that sends one
	judging bool
	pos     float64   // the furthest position (out_time, µs) it reported
	frames  int64     // the most frames it reported (-1 when it sends none: an older worker)
	wrote   int64     // the most bytes it reported written (0 when it sends none)
	movedAt time.Time // when that position last moved on (or the encode started)
	posAt   time.Time // when out_time last moved forward (or the encode started): the frozen-position ceiling's clock, which frames and bytes do not move
	finish  bool      // that position is the file's length: the worker is finishing the file (its own allowance)
}

// Converting records the remote worker's encode at position pos (out_time, µs) in a file durationSec long: the
// first report starts the stall clock, and only a position further on moves it. A worker that sends no frames
// or bytes is judged by its position alone.
func (r *RemoteJob) Converting(pos float64, now time.Time, durationSec float64) {
	r.ConvertingFrames(pos, -1, 0, 0, now, durationSec)
}

// ConvertingFrames is Converting with what else the worker reports: frame (-1 when none) and wrote, the bytes
// of the new file so far, judged against sizeBefore, the source's size. Like a local encode (encodeLocal): a
// position further on is progress; so is a frame or byte count that grows while the output stays within twice
// the source. Runaway output past that is not progress.
func (r *RemoteJob) ConvertingFrames(pos float64, frame, wrote, sizeBefore int64, now time.Time, durationSec float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.judging {
		r.judging, r.pos, r.frames, r.wrote, r.movedAt, r.posAt = true, pos, frame, wrote, now, now
		r.finish = reachedEnd(pos, durationSec)
		return
	}
	posMoved := pos > r.pos
	outputOK := sizeBefore > 0 && wrote <= 2*sizeBefore
	grew := frame > r.frames || wrote > r.wrote
	if frame > r.frames {
		r.frames = frame
	}
	if wrote > r.wrote {
		r.wrote = wrote
	}
	if posMoved {
		r.pos = pos
		r.posAt = now
		r.finish = reachedEnd(pos, durationSec)
	}
	if posMoved || (outputOK && grew) {
		r.movedAt = now
	}
}

// finishing says the encode's position is the file's length (see reachedEnd).
func (r *RemoteJob) finishing() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.finish
}

// SetEncoder puts the encoder the remote worker converts with ("" for the processor) on the job's live row, in
// the words a local job uses. Nothing changes before the plan is known.
func (r *RemoteJob) SetEncoder(enc string) {
	r.mu.Lock()
	plan, id := r.plan, r.job
	r.mu.Unlock()
	if plan != nil {
		setLiveEncoder(id, encoderWordsUsed(plan.Snapshot.Plan, enc))
	}
}

// EncodeEnded is the worker's next stage (its checks, the upload): its time no longer counts as a stall.
func (r *RemoteJob) EncodeEnded() {
	r.mu.Lock()
	r.judging = false
	r.mu.Unlock()
}

// stillFor is how long the encode's position hasn't moved; false while it isn't being judged.
func (r *RemoteJob) stillFor(now time.Time) (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return now.Sub(r.movedAt), r.judging
}

// posStillFor is how long the encode's out_time has not moved forward (frames and bytes do not count); false while
// it isn't being judged.
func (r *RemoteJob) posStillFor(now time.Time) (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return now.Sub(r.posAt), r.judging
}

// NewRemoteJob is the join for lease.
func NewRemoteJob(lease string) *RemoteJob {
	return &RemoteJob{Lease: lease, planned: make(chan struct{}), result: make(chan RemoteResult, 1), ended: make(chan struct{})}
}

// Plan is the plan once the server's run reached the conversion; ready false while it prepares.
func (r *RemoteJob) Plan() (p RemotePlan, ready bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.plan == nil {
		return RemotePlan{}, false
	}
	return *r.plan, true
}

// Planned is closed once the plan is ready.
func (r *RemoteJob) Planned() <-chan struct{} { return r.planned }

// Ended is closed once the server's run of the job ended (done, failed, given back...).
func (r *RemoteJob) Ended() <-chan struct{} { return r.ended }

// Deliver hands the worker's answer to the run; false if it already has one or has ended.
func (r *RemoteJob) Deliver(res RemoteResult) bool {
	select {
	case <-r.ended:
		return false
	default:
	}
	select {
	case r.result <- res:
		return true
	default:
		return false
	}
}

// RemoteCheckEvery is how often a run waiting for its remote worker checks that the lease still holds.
var RemoteCheckEvery = 5 * time.Second

// convert gives the plan to the remote worker and waits for its file, put at tmp (and its subtitles at
// their staged names). A lease that ended (the worker stopped answering) ends the wait: errLeaseLost.
// It also returns the ffmpeg arguments the remote worker ran (nil when it reported none).
func (r *RemoteJob) convert(ctx context.Context, w *Worker, job jobs.Job, snap rules.Snapshot, srcPath, tmp string) (string, []string, error) {
	r.mu.Lock()
	r.plan = &RemotePlan{Snapshot: snap, Source: srcPath, Stage: tmp}
	r.job = job.ID
	r.mu.Unlock()
	close(r.planned)
	t := time.NewTicker(RemoteCheckEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-t.C:
			if held, err := w.Queue.HoldsLease(job.ID, r.Lease); err == nil && !held {
				return "", nil, errLeaseLost
			}
		case res := <-r.result:
			switch {
			case res.Kept != "":
				return res.Stderr, res.Command, kept("%s", res.Kept)
			case res.Err != nil:
				return res.Stderr, res.Command, &remoteFailure{code: res.Code, err: res.Err}
			}
			return res.Stderr, res.Command, nil // the upload is in tmp already
		}
	}
}

// RunRemote runs job, leased by a remote worker as rj.Lease, on this server until it ends: the lease
// is the remote worker's to renew. The outcome is recorded as for a local job.
func (w *Worker) RunRemote(ctx context.Context, job jobs.Job, rj *RemoteJob) error {
	defer close(rj.ended)
	jobCtx, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	w.current, w.cancelJob, w.stopped = job.ID, cancel, stopNone
	w.failures, w.lease, w.remote = nil, rj.Lease, rj
	w.mu.Unlock()
	if w.FullDecodeMaxSec <= 0 || w.FullDecodeMaxSec > remoteDecodeFull {
		w.FullDecodeMaxSec = remoteDecodeFull // a quick decode here: samples of a longer file (Q4 a)
	}
	_, err := w.runClaimed(ctx, job, jobCtx, cancel, rj.Lease, false)
	if errors.Is(err, errLeaseLost) {
		err = nil // the job went back to the queue: nothing to record
	}
	rj.endErr = err
	return err
}

// remoteDecodeFull: a remote worker's file is decoded completely here only when it is this short;
// longer, one-minute samples (it decoded the whole file itself).
const remoteDecodeFull = 120

// CheckResult runs the checks a local conversion's result gets (its shape, a decode, the picture check
// at the plan's threshold) on the file at out, made from src: a remote worker's checks before it
// uploads (Q4 a). A *KeptError: a check said no. The picture check's score is returned when it ran (nil
// when none did), so the remote report can carry the job's check step.
func CheckResult(ctx context.Context, x media.Exec, ffmpeg, ffprobe string, snap rules.Snapshot, src, out string) (*CheckScore, error) {
	if snap.Source == nil {
		return nil, errors.New("the plan has no probe of the original")
	}
	st, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	w := &Worker{Exec: x, FFmpeg: ffmpeg, FFprobe: ffprobe, MinSSIM: snap.Verify.MinSSIM, DecodeCheck: snap.Verify.DecodeCheck,
		FullDecodeMaxSec: snap.Verify.FullDecodeMaxSec}
	err = w.verify(ctx, *snap.Source, snap.Expect, src, out, st.Size())
	return w.check, err
}
