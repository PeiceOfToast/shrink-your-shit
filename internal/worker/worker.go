// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package worker is the loop that joins jobs and flow: claim a job, run it, write its result. A panic, a cancel or
// a shutdown never leaves a job in running, and a shutdown never counts as a try.
package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
)

// DefaultTick is the fallback wake for an idle slot, in case a wake is missed.
const DefaultTick = 5 * time.Second

// DefaultShutdownGrace is how long a running job may finish after shutdown starts.
const DefaultShutdownGrace = 30 * time.Second

// Work is a claimed job with everything flow needs to run it. A Work with Err set could not be built (the file or
// the library is gone, or the workflow has no published revision); the worker fails it without running flow.
type Work struct {
	Job jobs.Job
	In  flow.RunInput
	Err error
	// Transient is true when Err is a database read that may work on a later try. The job retries; it does not fail.
	Transient bool
}

// Source is where the worker gets and reports jobs. The built-in source is a thin adapter over jobs; the remote
// client implements the same shape in a later stage.
type Source interface {
	Claim(ctx context.Context, workerID string) (*Work, error)
	Finish(ctx context.Context, id int64, attempt int, r jobs.Result) error
	Requeue(ctx context.Context, id int64, why string) error
	Cancel(ctx context.Context, id int64) error
}

// RunFunc runs one attempt. flow.Run has this shape.
type RunFunc func(ctx context.Context, in flow.RunInput, env flow.Env) flow.Result

// Emitter is the event bus as the worker uses it.
type Emitter interface {
	Emit(events.Event) error
}

// Config is what a worker needs. Zero durations take the defaults above. DataDir holds the raw ffmpeg logs at
// <DataDir>/jobs/<id>/<attempt>.log. Bus may be nil, in which case the worker records nothing.
type Config struct {
	Slots         int
	WorkerID      string
	Source        Source
	Run           RunFunc
	Env           flow.Env
	DataDir       string
	Bus           Emitter
	Tick          time.Duration
	ShutdownGrace time.Duration
}

// Worker runs jobs from a Source. Stage 1 runs one slot; Slots above 1 is accepted, but a Wake reaches one idle slot.
type Worker struct {
	cfg  Config
	wake chan struct{}
}

// New returns a worker with defaults filled in. Slots below 1 means 1.
func New(cfg Config) *Worker {
	if cfg.Slots < 1 {
		cfg.Slots = 1
	}
	if cfg.Tick <= 0 {
		cfg.Tick = DefaultTick
	}
	if cfg.ShutdownGrace <= 0 {
		cfg.ShutdownGrace = DefaultShutdownGrace
	}
	if cfg.WorkerID == "" {
		cfg.WorkerID = "local"
	}
	return &Worker{cfg: cfg, wake: make(chan struct{}, 1)}
}

// Wake tells an idle slot that work may be waiting. Call it after a job is enqueued. It never blocks, and a wake
// that arrives while a slot is busy is kept for the next idle moment.
func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Run starts the slots and blocks until ctx is done and every slot has returned. A job running when ctx is done gets
// ShutdownGrace to finish; after that it is cancelled and requeued.
func (w *Worker) Run(ctx context.Context) {
	done := make(chan struct{}, w.cfg.Slots)
	for i := 0; i < w.cfg.Slots; i++ {
		go func() {
			w.slot(ctx)
			done <- struct{}{}
		}()
	}
	for i := 0; i < w.cfg.Slots; i++ {
		<-done
	}
}

func (w *Worker) slot(ctx context.Context) {
	for ctx.Err() == nil {
		work, err := w.cfg.Source.Claim(ctx, w.cfg.WorkerID)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			w.record(events.KindError, events.LevelError, 0, 0, "claim.failed", err.Error())
			w.idle(ctx)
			continue
		}
		if work == nil {
			w.idle(ctx)
			continue
		}
		w.execute(ctx, *work)
	}
}

// idle waits for a wake, the fallback tick, or shutdown. It never spins.
func (w *Worker) idle(ctx context.Context) {
	t := time.NewTimer(w.cfg.Tick)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-w.wake:
	case <-t.C:
	}
}

// execute runs one claimed job and writes its result. The run gets its own context: it ends when the job does, or
// when shutdown has lasted ShutdownGrace, so the write-back below can still reach the database during shutdown.
func (w *Worker) execute(ctx context.Context, work Work) {
	j := work.Job
	wctx := context.WithoutCancel(ctx)

	if work.Err != nil {
		if work.Transient {
			w.finish(wctx, j, jobs.Result{
				Verdict: jobs.VerdictRetry, Reason: "could not read the library right now: " + work.Err.Error(),
				FailedStep: "worker", NextAction: "retry",
			})
			return
		}
		w.finish(wctx, j, jobs.Result{
			Verdict: jobs.VerdictFail, Reason: "could not build the run: " + work.Err.Error(),
			FailedStep: "worker", NextAction: "fix_and_retry",
		})
		return
	}

	runCtx, cancel := context.WithCancel(wctx)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			t := time.NewTimer(w.cfg.ShutdownGrace)
			defer t.Stop()
			select {
			case <-done:
			case <-t.C:
				cancel()
			}
		case <-done:
		}
	}()

	res, p := w.safeRun(runCtx, work.In)
	if p != nil {
		w.record(events.KindError, events.LevelError, j.ID, j.Attempt, "internal_error",
			fmt.Sprintf("panic in run: %v\n%s", p.value, p.stack))
		w.finish(wctx, j, jobs.Result{
			Verdict: jobs.VerdictFail, Reason: "internal_error", FailedStep: "worker", NextAction: "report_bug",
		})
		return
	}

	stopping := ctx.Err() != nil
	r, end := settle(res, stopping)
	switch end {
	case EndFinish:
		w.finish(wctx, j, r)
	case EndRequeue:
		why := res.Reason
		if stopping {
			why = "shutdown"
		}
		if why == "" {
			why = "requeued"
		}
		if err := w.cfg.Source.Requeue(wctx, j.ID, why); err != nil {
			w.record(events.KindError, events.LevelError, j.ID, j.Attempt, "requeue.failed", err.Error())
		}
	case EndCancel:
		err := w.cfg.Source.Cancel(wctx, j.ID)
		var changed *jobs.ErrChangedSince
		if err != nil && !errors.As(err, &changed) {
			w.record(events.KindError, events.LevelError, j.ID, j.Attempt, "cancel.failed", err.Error())
		}
	}
}

func (w *Worker) finish(ctx context.Context, j jobs.Job, r jobs.Result) {
	if err := w.cfg.Source.Finish(ctx, j.ID, j.Attempt, r); err != nil {
		// The job stays running; startup recovery puts it back. Say so in the log rather than hide it.
		w.record(events.KindError, events.LevelError, j.ID, j.Attempt, "finish.failed", err.Error())
	}
}

type panicked struct {
	value any
	stack []byte
}

// safeRun calls the run function and turns a panic into a value, so one bad job cannot stop the slot.
func (w *Worker) safeRun(ctx context.Context, in flow.RunInput) (res flow.Result, p *panicked) {
	env, closeLog := w.attemptEnv(in)
	defer closeLog()
	defer func() {
		if r := recover(); r != nil {
			p = &panicked{value: r, stack: debug.Stack()}
		}
	}()
	return w.cfg.Run(ctx, in, env), nil
}

// attemptEnv gives this attempt its own raw log. A log that cannot be opened is recorded and the run goes ahead
// without one: the log is evidence, not a reason to skip the file.
func (w *Worker) attemptEnv(in flow.RunInput) (flow.Env, func()) {
	env := w.cfg.Env
	env.Raw = io.Discard
	if w.cfg.DataDir == "" {
		return env, func() {}
	}
	dir := filepath.Join(w.cfg.DataDir, "jobs", strconv.FormatInt(in.JobID, 10))
	path := filepath.Join(dir, strconv.Itoa(in.Attempt)+".log")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		w.record(events.KindWarning, events.LevelWarn, in.JobID, in.Attempt, "log.unavailable", err.Error())
		return env, func() {}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		w.record(events.KindWarning, events.LevelWarn, in.JobID, in.Attempt, "log.unavailable", err.Error())
		return env, func() {}
	}
	env.Raw = f
	return env, func() { f.Close() }
}

// record writes one event, if there is a bus. A failed emit is dropped: the job's outcome never depends on a log line.
func (w *Worker) record(kind events.Kind, level events.Level, jobID int64, attempt int, code, msg string) {
	if w.cfg.Bus == nil {
		return
	}
	_ = w.cfg.Bus.Emit(events.Event{
		JobID: jobID, Attempt: attempt, Kind: kind, Level: level, Code: code, Message: msg,
		WorkerID: w.cfg.WorkerID, Source: "worker",
	})
}
