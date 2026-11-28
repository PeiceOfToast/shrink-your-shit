// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// stopNextAction is what the failure card tells the person when a step is set to stop rather than
// use the processor: the setting, or a retry, is the way forward.
const stopNextAction = "Change the step to use the processor, or retry it from Jobs."

// ProcessorPick is the picker for an attempt that runs only on the processor: the processor encoder
// for the codec, software decode. A worker without a card detector leaves Env.Pick nil, which is the
// same answer.
func ProcessorPick(want Want) (Attempt, error) {
	rung, err := processorRung(want.Codec)
	if err != nil {
		return Attempt{}, err
	}
	return Attempt{Rungs: []encode.Rung{rung}}, nil
}

// processorRung is the processor encoder for a codec.
func processorRung(codec encode.Codec) (encode.Rung, error) {
	encoder, ok := encode.CPUEncoder(codec)
	if !ok {
		return encode.Rung{}, fmt.Errorf("no processor encoder for %s", codec)
	}
	return encode.Rung{Encoder: encoder, Family: encode.FamilyCPU, Decode: encode.DecodeSoftware}, nil
}

// convert runs one file's encode down the ladder (L-HW-5): the picker's card rungs in order, then
// the processor unless the step stops first. A card rung that fails on the encoder's account hands
// the staged file — truncated, never removed — to the next rung, and the ladder never goes back up.
// An empty Result is a file the last rung wrote.
func (e *engine) convert(ctx context.Context, plan encode.Plan, info media.Info, d Decisions, staged safety.Staged) Result {
	want := wantFor(plan, info)
	useGpu, ifFails := gpuChoice(d)
	// ifGpuFails is about a card that could not do the file; with useGpu off there is no card in
	// the attempt to stop.
	stop := useGpu && ifFails == "stop"

	var card Attempt
	picked := useGpu && e.env.Pick != nil
	if picked {
		got, err := e.env.Pick(want)
		if err != nil {
			return fatal("encoder.unavailable", err.Error(), "", NextEncoder)
		}
		card = got
		if card.Line != "" {
			e.note(card.Line)
		}
	}
	if picked && len(card.Rungs) == 0 {
		// L-HW-9: the processor converts the file, and the log says why no card took it.
		if note := processorNote(card.Reason); note != "" {
			e.warn(note)
		}
		if stop {
			return Result{
				Outcome: OutcomeFatal, Reason: "encode.gpu_unavailable",
				Message: noCardLine(card.Reason), FailedStep: e.encodeOwner(d),
				NextAction: stopNextAction,
			}
		}
	}

	rungs := append([]encode.Rung(nil), card.Rungs...)
	// The processor rung ends the ladder, unless the step stops first or the picker named it.
	if !stop && !namesProcessor(rungs) {
		cpu, err := processorRung(want.Codec)
		if err != nil {
			return fatal("encoder.unavailable", err.Error(), "", NextEncoder)
		}
		rungs = append(rungs, cpu)
	}

	var run media.Result
	for i, rung := range rungs {
		built, err := encode.Build(plan, info, rung)
		if err != nil {
			if errors.Is(err, encode.ErrSkip) {
				return skip("encode.skip", err.Error())
			}
			if errors.Is(err, encode.ErrRungNotBuilt) && rung.Family != encode.FamilyCPU {
				// A rung Build refuses is one the card cannot take this plan with; the next rung
				// still converts the file (L-HW-11).
				e.warn(rungNotBuiltNote(rung, err))
				continue
			}
			return fatal("encode.plan_failed", err.Error(), e.encodeOwner(d), NextFixWorkflow)
		}
		// Build always ends the command with the plan's destination; the run writes the staged file.
		if n := len(built.Argv); n == 0 || built.Argv[n-1] != plan.Output {
			return fatal("encode.plan_failed", "the command does not end in the output path", e.encodeOwner(d), NextFixWorkflow)
		}
		if i > 0 {
			// Truncate, don't remove: the staged file is checked by identity, so every rung writes
			// into the same file (L-HW-5).
			if err := os.Truncate(staged.Path, 0); err != nil && !os.IsNotExist(err) {
				return fatal("save.stage_failed",
					"The partial file could not be cleared for the next try: "+err.Error(), "save", NextDestination)
			}
		}
		// A stream the file type cannot hold is left out of the output, and the note says which and
		// why. Some of this is invisible to the check: Audio copies a track, so the flow's container
		// rule sees "copy" and passes, while the source's own codec is one the file type refuses. The
		// job log shows the plain sentence instead of dropping the track silently.
		for _, note := range built.Notes {
			e.warn(note)
		}
		argv := append([]string{"ffmpeg"}, built.Argv...)
		argv[len(argv)-1] = staged.Path
		e.command(argv)

		run, err = e.env.Media.Run(ctx, argv, e.env.Progress)
		e.writeRaw(run)
		if res := ctxStop(ctx); res.Outcome != "" {
			return res
		}
		if err == nil {
			return Result{}
		}

		code := failureCode(run.KeyLines)
		if i+1 < len(rungs) && stepsDown(rung, code) {
			if rungs[i+1].Family == encode.FamilyCPU {
				// L-HW-7: the card's encoder failed and the processor converts the file instead.
				e.warn(fallbackLine(rung, reasonOf(run, err)))
			} else {
				// The full pipeline failed and the card's hybrid rung still converts the file: the
				// processor decodes, the card encodes (L-HW-5).
				e.warn(fullPipelineNote(rung, reasonOf(run, err)))
			}
			continue
		}
		if stop && rung.Family != encode.FamilyCPU && stepsDown(rung, code) {
			// L-HW-5: under ifGpuFails: stop the ladder ends here, with plain words, and the file
			// never reaches the processor rung.
			return Result{
				Outcome: OutcomeFatal, Reason: "encode.gpu_stopped",
				Message: stopLine(rung, reasonOf(run, err)), FailedStep: e.encodeOwner(d),
				NextAction: stopNextAction, KeyLines: run.KeyLines,
			}
		}
		return Result{
			Outcome: OutcomeFatal, Reason: "encode.failed", Message: err.Error(),
			FailedStep: e.encodeOwner(d), NextAction: NextRetryLog, KeyLines: run.KeyLines,
		}
	}
	// The ladder always ends in a rung that returns; this is only reachable if every rung was
	// skipped by Build, which leaves nothing that could convert the file.
	return fatal("encoder.unavailable", "no encoder could convert this file", e.encodeOwner(d), NextEncoder)
}

// namesProcessor says whether the rungs already end on the processor, so a picker's own processor
// rung is not added twice.
func namesProcessor(rungs []encode.Rung) bool {
	for _, r := range rungs {
		if r.Family == encode.FamilyCPU {
			return true
		}
	}
	return false
}

// gpuChoice is the video step's graphics setting: whether a card may be used and what happens when
// it cannot (page 07's useGpu and ifGpuFails). A flow with no video work, or an older one whose
// detail does not carry the pair, runs on the processor.
func gpuChoice(d Decisions) (useGpu bool, ifFails string) {
	for _, s := range d.Steps {
		if s.ID != "video" || !s.Decision.Work {
			continue
		}
		useGpu, _ = s.Decision.Detail["useGpu"].(bool)
		ifFails, _ = s.Decision.Detail["ifGpuFails"].(string)
		if ifFails == "" {
			ifFails = "cpu"
		}
		return useGpu, ifFails
	}
	return false, "cpu"
}

// wantFor is what this attempt asks of a card (L-HW-9): the codec it writes, whether the output is
// ten bits, whether HDR mastering data must survive, whether it scales, and whether nothing in the
// plan needs the processor's frames (L-HW-11).
func wantFor(plan encode.Plan, info media.Info) Want {
	want := Want{Codec: plan.Video.Codec, Scales: plan.Video.Scale != (encode.Scale{})}
	main := info.MainVideo()
	if main == nil {
		return want
	}
	if depth, err := encode.OutputDepth(plan, *main); err == nil && depth >= 10 {
		want.TenBit = true
	}
	// Only a card whose HDR probe passed may keep mastering data (L-HW-9): HDR10's static
	// metadata is what that probe checks.
	want.HDR = encode.HDRKept(plan, *main) && (main.HDR.MasteringDisplay || main.HDR.ContentLight)
	want.FullOK = encode.FullPipelineOK(plan, *main)
	return want
}

// note writes a plain line to the job log.
func (e *engine) note(message string) {
	e.emit(events.Event{Kind: events.KindNote, Level: events.LevelInfo, Message: message})
}

// rungNotBuiltNote is the log line for a card rung Build refuses: the card cannot run this plan, so
// the next rung converts the file.
func rungNotBuiltNote(rung encode.Rung, err error) string {
	detail := strings.TrimPrefix(err.Error(), encode.ErrRungNotBuilt.Error()+": ")
	return "The graphics card's " + rung.Encoder + " cannot run this file (" + detail + "); the next rung converts it."
}
