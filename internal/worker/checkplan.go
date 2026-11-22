// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// L1: the checks before anything is converted are one named step of the job's run. It sorts before the encode
// step (index 0) and gathers what used to be decided in several places: the encoder for the file and its bit
// depth (R4), the free space beside the original (R7), and the folder for new files: made, cleaned, and shown to
// be writable, with its own free space.
const (
	stepCheck     = -1      // the run step before the encode (encode is 0, the check after it is 1, replace is 2)
	secCheck      = "check" // the job log's section for the pre-write checks
	checkStepName = "preflight"
)

// encoderRun is what the pre-write check of the encoder decided: the encoder the conversion will use and what its
// encode needs besides the plan and the paths (the arguments are built once the staged file has a path).
type encoderRun struct {
	gpu     string   // the graphics encoder, "" when the processor encodes
	noSlots bool     // the graphics slots are set to 0: no graphics encode may run here
	full    bool     // gpu runs the full graphics pipeline (decode and encode on the graphics card)
	slot    *GPUSlot // the card's place this job holds while its graphics encode runs
	why     string   // why that encoder, for the log's Chosen line
}

// checkPlan runs every check that must pass before this file is converted, in a fixed order, and records them as
// one run step: a job that stops here says which check stopped it, and a job that goes on shows the step passed
// before its encode. The errors, codes and words are the ones each check already had.
func (w *Worker) checkPlan(ctx context.Context, job jobs.Job, plan rules.Plan, src media.Info, srcPath string, size int64, newName string) (encoderRun, error) {
	started := w.now()
	refuse := func(err error) (encoderRun, error) {
		w.recordCheckStep(job.ID, started, "failed", plainReason(err)+" No new file was written.")
		return encoderRun{}, err
	}
	// 1. The encoder that can make this file, at the bit depth it needs.
	enc, err := w.encoderFor(ctx, job, plan, src)
	if err != nil {
		return refuse(err)
	}
	if enc.gpu == "" && plan.Video.Hardware == "only" && !plan.Video.Copy && w.remote == nil {
		words := HardwareOnlyNoEncoderWords
		if enc.noSlots {
			words = HardwareOnlySlotsWords
		}
		return refuse(failed(words, errors.New("no graphics encoder for this file")))
	}
	// 2. The free space beside the original, where the new file is made (or, in Folder mode, staged).
	if err := w.preflight(srcPath, size); err != nil {
		return refuse(err)
	}
	checked := "the encoder for this file and the free space beside it"
	// 3. The folder for new files: made if it is not there, an earlier run's temp file removed, and written to
	// once. 4. Its own disk has the room the new file needs.
	if OutputMode(plan.Output) == Folder {
		dir := filepath.Dir(newName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return refuse(failed("The folder for new files can't be written to, so nothing was converted. Your original was not touched. Check the flow's folder, then press Retry.", err))
		}
		if err := removeLeftoverTemps(newName); err != nil { // an earlier run's, staged there (review-697332b P2)
			return refuse(err)
		}
		if err := jobs.WritableDir(dir); err != nil {
			return refuse(failed("The folder for new files can't be written to, so nothing was converted. Your original was not touched. Check the flow's folder, then press Retry.", err))
		}
		if err := w.preflight(newName, size); err != nil { // its own disk: the new file is made there (review 6cafc6f)
			return refuse(err)
		}
		checked += ", the folder for new files and the free space there"
	}
	w.recordCheckStep(job.ID, started, "ok", "Checked "+checked+".")
	return enc, nil
}

// recordCheckStep keeps the pre-write check step: its outcome and its one line, which says what passed or which
// check stopped the job (the run log groups the line under the step, and the download has its own section).
func (w *Worker) recordCheckStep(id int64, started time.Time, outcome, result string) {
	if w.Queue == nil {
		return
	}
	level := jobs.LevelInfo
	if outcome == "failed" {
		level = jobs.LevelError
	}
	w.Queue.RecordRunStep(id, jobs.RunStep{Index: stepCheck, Name: checkStepName,
		StartedAt: started.UTC().Format(time.RFC3339), TookMs: w.now().Sub(started).Milliseconds(), Outcome: outcome, Result: result})
	w.Queue.AppendLogLine(id, stepCheck, secCheck, level, "app", result)
}

// encoderFor picks the encoder this job will use: the local worker's graphics encoder for the plan's format when
// it can do the plan, else the processor. A card that cannot do the file, or whose slots are set to 0, is said so
// and the processor takes the file, unless the flow allows only the card (checkPlan refuses that). It writes the
// selection's lines to the encode section, as they were written there before.
func (w *Worker) encoderFor(ctx context.Context, job jobs.Job, plan rules.Plan, src media.Info) (encoderRun, error) {
	var run encoderRun
	w.setPhase(secEncode)                                      // the encoder choice and its notes belong to encode (log-spec section 4)
	cpuOnly := plan.Video.Hardware == "cpu" && w.remote == nil // the Convert step says never the graphics card
	if cpuOnly {
		run.why = "CPU only, as the Convert step says"
	}
	if w.remote != nil || w.Encoder == nil || cpuOnly {
		return run, nil
	}
	need10 := rules.NeedsTenBit(plan, &src)
	if w.EncoderCandidates != nil && !plan.Video.Copy { // R5: what hardware is here, and each encoder considered
		for _, line := range w.EncoderCandidates(plan.Video.Codec, need10, plan.Video.HDR) {
			w.logLine(job.ID, secEncode, jobs.LevelInfo, line)
		}
	}
	want, why := w.Encoder(plan.Video.Codec, need10, plan.Video.HDR)
	note := rules.FallbackNote(plan.Video, why)
	if want == "" || plan.Video.Copy {
		if note != "" && !plan.Video.Copy {
			w.jobf(job.ID, ": %s", note) // why the processor: the Chosen line follows it, without repeating it
		}
		return run, nil
	}
	until, paused, err := w.Queue.EncoderPaused(w.ID)
	switch {
	case err == nil && paused && (plan.Video.Hardware == "only" || plan.Video.HardwareFail == "fail"):
		// The Convert step does not let the processor take this file: the card is only temporarily out, so wait for it
		w.jobf(job.ID, ": %s is paused until %s after three failures in a row; this step waits for it and never converts on the processor", want, until.Local().Format("15:04"))
		return run, retryable(failed(HardwarePausedWords, errors.New(want+" is paused after three failures in a row")), retryPaused)
	case err == nil && paused:
		w.jobf(job.ID, ": %s is paused until %s after three failures in a row; using the processor", want, until.Local().Format("15:04"))
		run.why = want + " is paused after three failures in a row"
		return run, nil
	}
	full, ok := w.graphicsPlan(ctx, job, plan, src, want)
	if !ok {
		if plan.Video.Hardware == "only" { // never the processor here: checkPlan's refusal says what happens
			w.jobf(job.ID, ": encoder %s can't do this file", want)
		} else {
			w.jobf(job.ID, ": encoder %s can't do this file; using the processor", want)
		}
		run.why = want + " can't do this file"
		return run, nil
	}
	// The card's slots come first (user bug 11-20): a job waits while they are all in use, so no two graphics
	// jobs run on it at once beyond the limit. With none (the limit at 0), the processor does the encoding, as
	// it does for any plan the card can't do.
	slot, serr := w.takeGPUSlot(ctx, job)
	if serr != nil { // stopped while it waited
		return run, serr
	}
	if slot != nil {
		run.gpu, run.full, run.slot = want, full, slot
		run.why = want + " passed its test on this computer and can do this file"
		return run, nil
	}
	run.why = want + " passed its test on this computer, but the graphics slots are set to 0"
	run.noSlots = true
	return run, nil
}

// graphicsPlan picks the pipeline for the graphics encoder want: the full GPU pipeline when its family decodes the
// source here (full is true), else the processor decoding and the encoder encoding. ok is false when neither can
// do this plan. It takes no output path: the arguments are built once the staged file has one (graphicsArgs).
func (w *Worker) graphicsPlan(ctx context.Context, job jobs.Job, plan rules.Plan, src media.Info, want string) (full, ok bool) {
	if !plan.CanEncodeWith(want) {
		return false, false
	}
	fam := rules.GraphicsFamily(want)
	prof, _ := rules.ProfileFor(want) // a family without a full graphics pipeline is decoded by the processor
	if v := src.Video(); v != nil && w.GraphicsDecodes != nil && prof.FullPipeline() {
		if w.GraphicsDecodes(ctx, fam, v.Codec, rules.NeedsTenBit(plan, &src)) { // one rule for 10-bit, here and at the encoder
			if plan.HasFullPipeline(want) {
				w.jobf(job.ID, ": decoding and encoding on the graphics card (full GPU pipeline)")
				return true, true
			}
		} else {
			w.jobf(job.ID, ": %s can't decode %s here; the processor decodes", fam, codecName(v.Codec))
		}
	}
	return false, true
}

// graphicsArgs builds the arguments for the graphics encoder want from what graphicsPlan decided. ok is false only
// when the plan turned out not to take that encoder: the processor's arguments are returned then.
func graphicsArgs(plan rules.Plan, srcPath, tmp, want string, full bool) (args []string, ok bool) {
	if full {
		if args, full := plan.FFmpegArgsFullGPU(srcPath, tmp, want); full {
			return args, true
		}
	}
	return plan.FFmpegArgsWith(srcPath, tmp, want)
}
