// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package worker runs transcode jobs. It only talks to the Queue interface,
// so a remote worker (HTTP pull) can implement Queue later; the built-in
// worker is just one instance using the local store.
package worker

import (
	"context"
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
	// Keep marks a job "kept original": a safety check refused the result (reason says why).
	Keep(id int64, reason string) error
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

	mu         sync.Mutex
	filterMemo map[string]bool
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
	final, err := w.process(ctx, job)
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

// TempPath is where ffmpeg writes before the file is trusted. It keeps the
// original extension so ffmpeg picks the right container.
func TempPath(src string) string {
	ext := filepath.Ext(src)
	return strings.TrimSuffix(src, ext) + ".syp-tmp" + ext
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
	before, serr := os.Stat(job.Path)
	if serr != nil {
		return "", fmt.Errorf("source file: %w", serr)
	}
	tmp := TempPath(job.Path)
	os.Remove(tmp) // leftover from a crash
	done := false
	defer func() {
		if !done {
			os.Remove(tmp)
		}
	}()

	// What the source looks like (and so what the new file must look like).
	src, perr := media.Probe(ctx, w.Exec, w.FFprobe, job.Path)
	if perr != nil {
		return "", kept("the original cannot be read any more (%v)", perr)
	}
	if err := w.preflight(job.Path, before.Size()); err != nil {
		return "", err
	}
	var packets int64
	if ord := src.MainVideoOrdinal(); ord >= 0 {
		n, cerr := media.CountVideoPackets(ctx, w.Exec, w.FFprobe, job.Path, ord)
		if cerr != nil {
			return "", kept("could not count the original's video frames (%v)", cerr)
		}
		packets = n
	}
	exp := DefaultExpect(src, packets)
	if exp.DurationSec <= 0 {
		exp.DurationSec = job.DurationSec
	}

	args := media.HEVCArgs(job.Path, tmp, w.Rule.CRF, w.Rule.Preset)
	if terr := media.Transcode(ctx, w.Exec, w.FFmpeg, args, job.DurationSec, func(p float64) {
		w.Queue.Progress(job.ID, p)
	}); terr != nil {
		return "", terr
	}

	if err := w.verify(ctx, src, exp, job.Path, tmp, before.Size()); err != nil {
		return "", err
	}
	// If the source changed while we encoded, our result is stale: don't clobber it.
	if now, nerr := os.Stat(job.Path); nerr != nil || now.Size() != before.Size() || !now.ModTime().Equal(before.ModTime()) {
		return "", kept("the original file changed while it was being converted, so the new file is out of date")
	}
	os.Chmod(tmp, before.Mode().Perm()) // keep the original's permissions
	final = job.Path
	if w.Mode == Beside {
		final = BesidePath(job.Path)
	}
	// Rename is atomic on one filesystem: the original is only replaced by a complete file.
	if err := moveIntoPlace(ctx, tmp, final); err != nil {
		return "", fmt.Errorf("move result into place: %w", err)
	}
	done = true
	return final, nil
}

// moveIntoPlace renames tmp over final. On Windows the rename fails while
// another program (a media player, a virus scanner) holds the target open, so
// it is retried for a while there. Elsewhere one try is enough.
func moveIntoPlace(ctx context.Context, tmp, final string) error {
	tries := 1
	if runtime.GOOS == "windows" {
		tries = 10
	}
	return renameRetry(ctx, os.Rename, tmp, final, tries, time.Second)
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
	}
	return err
}
