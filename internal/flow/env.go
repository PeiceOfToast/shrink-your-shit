// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"context"
	"io"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// Emitter is the event bus as flow uses it; *events.Bus has this shape.
type Emitter interface {
	Emit(events.Event) error
}

// Env is the world a flow is planned and run against: what this worker can encode, and the
// services the engine needs. A step reads only Encoders and CardFamily from it; the rest belongs
// to the engine.
type Env struct {
	// Encoders names the encoders this worker lists; a choice no worker can run is greyed.
	Encoders []string
	// CardFamily is the graphics card family this worker has, "" for none.
	CardFamily encode.Family

	// Media probes and runs ffmpeg. Tests swap in media/fake.
	Media media.Media
	// Safety stages, checks and places the output. Tests swap in a fake.
	Safety Safety
	// Sidecars records the .srt files a run writes before they are placed, and keeps their names
	// on the job (L-AUD-25). nil runs without records, which is what a try wants.
	Sidecars safety.SidecarStore
	// Pick chooses the encoder for one attempt (hw.Pick in stage 2). Stage 1 passes a picker that
	// answers with the processor only.
	Pick func(Want) (encode.Rung, error)
	// LoadRevision reads one saved revision; the store's Revision method has this shape.
	LoadRevision func(ctx context.Context, workflowID int64, rev int) (Flow, error)
	// Progress receives ffmpeg's progress blocks. It is live only and never stored.
	Progress func(media.Progress)
	// Raw receives the run's tool output for the attempt, the tail in stage 1. nil discards it.
	Raw io.Writer
	// Bus receives the job's events. nil sends none.
	Bus Emitter
	// SafetyLog receives one decision line per result check the safety check runs. A zero value
	// writes nothing, which is what a test wants.
	SafetyLog safety.Recorder
	// Now and Sleep are the clock; tests set them.
	Now   func() time.Time
	Sleep func(time.Duration)
	// AppVersion is the program's version, for run_start.
	AppVersion string
}

func (e Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e Env) sleep(d time.Duration) {
	if e.Sleep != nil {
		e.Sleep(d)
		return
	}
	time.Sleep(d)
}
