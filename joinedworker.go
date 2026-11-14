// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"context"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/remote"
)

// joinedWorker runs this install's worker loop while its role is worker: the same loop `syp worker` runs, against
// the server it joined, with the credential it saved. It starts when the role becomes worker (at startup, or when a
// join is allowed) and stops when the role changes away from it.
type joinedWorker struct {
	dir     string // the saved state's folder (the worker's data folder)
	work    string // the folder for the originals it downloads and the new files it makes
	ffmpeg  string
	ffprobe string

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func newJoinedWorker(dir, ffmpeg, ffprobe string) *joinedWorker {
	return &joinedWorker{dir: dir, work: filepath.Join(dir, "work"), ffmpeg: ffmpeg, ffprobe: ffprobe}
}

// Working says whether the worker loop is running.
func (j *joinedWorker) Working() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.cancel != nil
}

// SetRole takes the install's saved role: at startup, and after each change. Only "worker" runs the loop.
func (j *joinedWorker) SetRole(role string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if role != "worker" {
		j.stopLocked()
		return
	}
	if j.cancel != nil {
		return
	}
	st, err := remote.Load(j.dir)
	if err != nil {
		log.Printf("worker: this install is a worker, but its saved state in %s can't be read: %v", j.dir, err)
		return
	}
	if j.ffmpeg == "" || j.ffprobe == "" {
		log.Printf("worker: this install is a worker, but it needs ffmpeg and ffprobe to convert; it is not working")
		return
	}
	release, err := takeWorkFolder(j.work)
	if err != nil {
		log.Printf("worker: %v", err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	j.cancel, j.done = cancel, done
	c := &remote.Client{Server: st.Server, Token: st.Token, Version: version}
	conv := remote.Converter{Exec: media.OSExec{}, FFmpeg: j.ffmpeg, FFprobe: j.ffprobe, Work: j.work, MinFree: 2 << 30}
	go func() {
		defer close(done)
		defer release()
		work := make(chan struct{})
		go func() { defer close(work); c.Work(ctx, conv) }()
		if err := c.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("worker: the server refused this worker: %v", err)
		}
		cancel()
		<-work
	}()
	log.Printf("worker: working for %s as %q", st.Server, st.Name)
}

// stopLocked ends the loop and waits briefly for a conversion in progress to stop. Call with mu held.
func (j *joinedWorker) stopLocked() {
	if j.cancel == nil {
		return
	}
	j.cancel()
	select {
	case <-j.done:
	case <-time.After(30 * time.Second):
		log.Printf("worker: a conversion is still stopping; its folder is locked until it does")
	}
	j.cancel, j.done = nil, nil
}

// localPoolTakes says whether this install's own converter may take a job: the switch is on and this install is
// not a joined worker (the joined worker does the converting then).
func localPoolTakes(j *joinedWorker, switchOn bool) bool {
	return switchOn && !j.Working()
}
