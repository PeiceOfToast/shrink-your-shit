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
	"strings"
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

func (w *Worker) process(ctx context.Context, job jobs.Job) (string, error) {
	if _, err := os.Stat(job.Path); err != nil {
		return "", fmt.Errorf("source file: %w", err)
	}
	before, _ := os.Stat(job.Path)
	tmp := TempPath(job.Path)
	os.Remove(tmp) // leftover from a crash
	args := media.HEVCArgs(job.Path, tmp, w.Rule.CRF, w.Rule.Preset)
	err := media.Transcode(ctx, w.Exec, w.FFmpeg, args, job.DurationSec, func(p float64) {
		w.Queue.Progress(job.ID, p)
	})
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	st, err := os.Stat(tmp)
	if err != nil || st.Size() == 0 {
		os.Remove(tmp)
		return "", fmt.Errorf("ffmpeg produced no output")
	}
	// Sanity-check the result: same length as the source, and actually smaller.
	if err := w.verify(ctx, job, tmp, before.Size()); err != nil {
		os.Remove(tmp)
		return "", err
	}
	// If the source changed while we encoded, our result is stale: don't clobber it.
	if now, err := os.Stat(job.Path); err != nil || now.Size() != before.Size() || !now.ModTime().Equal(before.ModTime()) {
		os.Remove(tmp)
		return "", fmt.Errorf("source file changed while encoding; not replacing it")
	}
	os.Chmod(tmp, before.Mode().Perm()) // keep the original's permissions
	final := job.Path
	if w.Mode == Beside {
		final = BesidePath(job.Path)
	}
	// Rename is atomic on one filesystem: the original is only replaced by a complete file.
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("move result into place: %w", err)
	}
	return final, nil
}

// verify refuses output that looks truncated or did not save space.
func (w *Worker) verify(ctx context.Context, job jobs.Job, tmp string, origSize int64) error {
	info, err := media.Probe(ctx, w.Exec, w.FFprobe, tmp)
	if err != nil {
		return fmt.Errorf("result is not a readable video: %w", err)
	}
	if job.DurationSec > 0 && (info.DurationSec < job.DurationSec*0.98 || info.DurationSec > job.DurationSec*1.02) {
		return fmt.Errorf("result is %.0fs long, source is %.0fs; keeping the original", info.DurationSec, job.DurationSec)
	}
	if st, err := os.Stat(tmp); err == nil && st.Size() >= origSize {
		return fmt.Errorf("result (%d bytes) is not smaller than the original (%d bytes); keeping the original", st.Size(), origSize)
	}
	return nil
}
