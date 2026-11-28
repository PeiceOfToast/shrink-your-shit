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

// Want is what the runner must be able to encode for one attempt (L-HW-9): the codec and the
// properties that decide whether a graphics card may take the job.
type Want struct {
	Codec encode.Codec
	// TenBit is true when the output picture is ten bits.
	TenBit bool
	// HDR is true when the source's mastering data must survive the encode.
	HDR bool
	// Scales is true when the plan scales the picture.
	Scales bool
	// FullOK is true when nothing in the plan needs the processor's frames: no frame-rate change,
	// no tone map, no bit-depth change.
	FullOK bool
	// Bitrate is true when the plan carries a set bitrate rather than a quality slider. The card
	// encoders cannot follow one yet (L-HW-10 is quality-only), so such a job converts on the
	// processor.
	Bitrate bool
}

// Attempt is a picker's answer: the rungs to try in order (L-HW-5), the line the job log shows for
// the card it chose, and, when it chose none, why in plain words (L-HW-9). A picker may name the
// processor rung itself; the engine adds it otherwise, unless the step's ifGpuFails stops first.
type Attempt struct {
	Rungs  []encode.Rung
	Line   string
	Reason string
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
	// Replacer swaps the original for the new file when the save step replaces (S2-10b). nil in a try.
	Replacer Replacer
	// Pick chooses the graphics card's rungs for one attempt (the worker passes flow.PickFromAnswer
	// over the hardware detector's answer). nil means this worker has no card and every job runs on
	// the processor.
	Pick func(Want) (Attempt, error)
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
