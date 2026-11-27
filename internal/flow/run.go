// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// Outcome is how a run ended. The worker writes it on the job row (R1-4's words): smaller and
// bigger_kept are successes, skip is a file left alone on purpose, may_pass is a file that could
// not be checked, fatal is something to report, cancelled is a stop, and requeue sends the file
// back to the next scan.
type Outcome string

const (
	OutcomeSmaller    Outcome = "smaller"
	OutcomeBiggerKept Outcome = "bigger_kept"
	OutcomeSkip       Outcome = "skip"
	OutcomeMayPass    Outcome = "may_pass"
	OutcomeFatal      Outcome = "fatal"
	OutcomeCancelled  Outcome = "cancelled"
	OutcomeRequeue    Outcome = "requeue"
)

// Result is what a run leaves the worker. Outcome is never empty after Run returns; the other
// fields are filled in as far as the run got.
type Result struct {
	Outcome    Outcome
	SizeBefore int64
	SizeAfter  int64
	OutputPath string
	// Reason is a stable code ("check.too_short", "size.not_smaller", "file.gone"); Message is the
	// sentence the job card shows.
	Reason     string
	Message    string
	FailedStep string
	NextAction string
	// KeyLines is ffmpeg's tail when it failed, so the job's failure card can show the lines that
	// matter without reading the raw file.
	KeyLines []string
}

// RunInput is everything the engine knows before it touches the file: the job it runs for, the
// file as it was scanned, and the revision to run.
type RunInput struct {
	JobID     int64
	Attempt   int
	LibraryID string
	WorkerID  string

	WorkflowID int64
	Revision   int

	// LibraryRoot is the library's folder; Source must be inside it.
	LibraryRoot string
	Source      string
	// Size, MTime and FileID are what the scan recorded for the file.
	Size   int64
	MTime  time.Time
	FileID string

	// Try is one page's try on one file: Run uses Flow (the flow as the page holds it, no saved
	// revision) and stops after the result check. Nothing is placed, and the staging goes with the
	// run.
	Try  bool
	Flow *Flow
	// StageBase is where the private staging folder is made; empty means beside the destination,
	// which is what a job wants. A try sets it to the app's temp folder, so a killed process
	// leaves nothing in the original's folder.
	StageBase string
}

// Want is the codec the runner must be able to encode.
type Want struct {
	Codec encode.Codec
}

// ProcessorPick is stage 1's encoder choice: the processor encoder for the codec, software decode.
// Stage 2 replaces it with hw.Pick.
func ProcessorPick(want Want) (encode.Rung, error) {
	encoder, ok := encode.CPUEncoder(want.Codec)
	if !ok {
		return encode.Rung{}, fmt.Errorf("no processor encoder for %s", want.Codec)
	}
	return encode.Rung{Encoder: encoder, Family: encode.FamilyCPU, Decode: encode.DecodeSoftware}, nil
}

// recheckLook is how long a run waits before looking again at a file that changed since it was
// scanned: long enough for a copy that just ended to settle, short enough not to hold a worker.
const recheckLook = time.Second

// renameSidecar is fsx.RenameNoReplace; a test makes it fail after linking the file, which is the
// portable fallback's one failure.
var renameSidecar = fsx.RenameNoReplace

// Run does §7 steps 1-9 for one file and one revision: re-check, probe, plan, encode, check,
// size, place. It writes every event itself, so a job's log is the same whichever step failed,
// and it never touches the source except to read it.
func Run(ctx context.Context, in RunInput, env Env) Result {
	e := &engine{in: in, env: env}
	return e.run(ctx)
}

type engine struct {
	in      RunInput
	env     Env
	started time.Time
	// replace is true when the flow's save step replaces the original (S2-10b).
	replace bool
}

func (e *engine) run(ctx context.Context) (res Result) {
	e.started = e.env.now()
	defer func() { e.runEnd(res) }()
	e.runStart()

	// 1. Re-check the file against what the scan recorded.
	if res = e.recheck(ctx); res.Outcome != "" {
		return res
	}

	// 2. Probe the source.
	var info media.Info
	if info, res = e.probeSource(ctx); res.Outcome != "" {
		return res
	}

	// 3. Load the revision and let every step decide.
	rev, err := e.revision(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return cancelled()
		}
		return fatal("invalid_flow", err.Error(), "", "")
	}
	var decisions Decisions
	var plan encode.Plan
	if decisions, plan, res = e.plan(ctx, rev, info); res.Outcome != "" {
		return res
	}
	if !decisions.Work() {
		labels := make([]string, 0, len(decisions.Steps))
		for _, s := range decisions.Steps {
			labels = append(labels, s.Decision.Label)
		}
		return skip("flow.nothing", strings.Join(labels, "; "))
	}

	// 3b. Measure the loudness of every track whose loudness will be evened out (L-AUD-15). The
	// measurement only feeds Build's filter, so it happens after the steps have agreed there is
	// work and before the command is written.
	if res = e.measureLoudness(ctx, &plan, info); res.Outcome != "" {
		return res
	}

	// 4. Where the new file goes: its staging folder, and the name the place will use. The name is
	// resolved now, when subtitles become .srt files, because their names are built from it; Place
	// still never overwrites anything, so a file that appears meanwhile moves the new file instead.
	if res = ctxStop(ctx); res.Outcome != "" {
		return res
	}
	extracts, err := encode.Extracts(plan, info)
	if err != nil {
		return fatal("encode.plan_failed", err.Error(), e.encodeOwner(decisions), "")
	}
	if len(extracts) > 0 && !e.in.Try {
		name, err := safety.FreeName(plan.Output)
		if err != nil {
			// Nothing has been written yet: the original is untouched and no new file exists.
			return fatal("save.name_failed",
				"The original was left as it is and no new file was made, because no free name for it could be found. ("+err.Error()+")",
				"save", "")
		}
		plan.Output = name
		// A subtitle file name taken already stops the run now, before anything is written: the
		// person moves or renames that file and retries (L-AUD-25).
		for _, x := range extracts {
			if p := encode.SrtName(plan.Output, x.Part); exists(p) {
				return Result{Outcome: OutcomeFatal, Reason: "save.srt_exists", Message: srtTaken(p),
					FailedStep: "save", NextAction: "retry"}
			}
		}
	}
	// The output is staged beside its destination (a try stages in the app's temp folder instead).
	// The encoder writes the staged file, not the destination, so nothing appears there until Place
	// has checked the result; extracted subtitles are staged in the same private folder.
	staged, err := e.env.Safety.Stage(e.in.StageBase, plan.Output)
	if err != nil {
		return fatal("save.stage_failed", err.Error(), "save", "Check the destination folder's permissions.")
	}
	placed := false
	defer func() {
		if !placed {
			os.RemoveAll(staged.Folder)
		}
	}()
	if len(extracts) > 0 {
		plan.SidecarDir = staged.Folder
	}

	// 5. Build the command.
	rung, err := e.pick(plan)
	if err != nil {
		return fatal("encoder.unavailable", err.Error(), "", "")
	}
	built, err := encode.Build(plan, info, rung)
	if err != nil {
		if errors.Is(err, encode.ErrSkip) {
			return skip("encode.skip", err.Error())
		}
		return fatal("encode.plan_failed", err.Error(), e.encodeOwner(decisions), "")
	}
	// A stream the file type cannot hold is left out of the output, and the note says which and
	// why. Some of this is invisible to the check: Audio copies a track, so the flow's container
	// rule sees "copy" and passes, while the source's own codec is one the file type refuses. The
	// job log shows the plain sentence instead of dropping the track silently.
	for _, note := range built.Notes {
		e.emit(events.Event{Kind: events.KindWarning, Level: events.LevelWarn, Message: note})
	}
	argv := append([]string{"ffmpeg"}, built.Argv...)
	// Build always ends the command with the plan's destination; the run writes the staged file.
	if argv[len(argv)-1] != plan.Output {
		return fatal("encode.plan_failed", "the command does not end in the output path", e.encodeOwner(decisions), "")
	}
	argv[len(argv)-1] = staged.Path
	e.command(argv)

	run, err := e.env.Media.Run(ctx, argv, e.env.Progress)
	e.writeRaw(run)
	if res = ctxStop(ctx); res.Outcome != "" {
		return res
	}
	if err != nil {
		return Result{
			Outcome: OutcomeFatal, Reason: "encode.failed", Message: err.Error(),
			FailedStep: e.encodeOwner(decisions), NextAction: "retry", KeyLines: run.KeyLines,
		}
	}

	// 6. Check the result against the source (§7.1 checks 1-3 in stage 1).
	want, err := e.verifyWant(plan, info)
	if err != nil {
		return fatal("encode.plan_failed", err.Error(), "save", "")
	}
	verdict, err := e.env.Safety.Verify(ctx, e.env.Media, e.env.SafetyLog, staged, info, want, safety.DefaultVerifyTimeout)
	if err != nil {
		if ctx.Err() != nil {
			return cancelled()
		}
		// A check that could not run is not a no: the file may be fine (L-SAFE-1).
		return Result{
			Outcome: OutcomeMayPass, Reason: "check.unavailable",
			Message:    fmt.Sprintf("The new file could not be checked: %v", err),
			SizeBefore: e.in.Size, NextAction: "retry",
		}
	}
	if !verdict.OK {
		return Result{
			Outcome: OutcomeBiggerKept, SizeBefore: e.in.Size, Reason: verdict.Code,
			Message: verdict.Message, NextAction: verdict.NextAction,
		}
	}

	// Each subtitle that was to be saved as a file was written, and is not empty.
	for k := range extracts {
		st, err := os.Lstat(encode.StagedSidecar(plan.SidecarDir, k))
		if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
			return Result{Outcome: OutcomeBiggerKept, SizeBefore: e.in.Size, Reason: "subtitle.not_saved",
				Message: "A subtitle could not be saved as a separate file, so the original was kept."}
		}
	}

	// 7. The size rule: a result that is not smaller is not worth keeping.
	st, err := os.Stat(staged.Path)
	if err != nil {
		return fatal("save.read_failed", err.Error(), "save", "")
	}
	after := st.Size()
	e.size(e.in.Size, after)
	// A try stops here: its result has been checked and measured. Nothing is placed, and the
	// staging is removed by the deferred cleanup (placed stays false).
	if e.in.Try {
		return Result{Outcome: OutcomeSmaller, SizeBefore: e.in.Size, SizeAfter: after}
	}
	if after >= e.in.Size {
		return Result{
			Outcome: OutcomeBiggerKept, SizeBefore: e.in.Size, SizeAfter: after,
			Reason: "size.not_smaller", Message: "The new file is not smaller, so the original was kept.",
		}
	}

	// 8. Put it in place, never overwriting anything. The .srt files go beside it first, each
	// recorded before it is placed, so a name taken mid-run leaves nothing behind and a crash never
	// leaves one of ours unaccounted for (L-AUD-25).
	if res = ctxStop(ctx); res.Outcome != "" {
		return res
	}
	sidecars, res := e.placeSidecars(plan, extracts, staged)
	if res.Outcome != "" {
		return res
	}
	if e.replace {
		return e.replaceOriginal(ctx, staged, plan.Output, after, &placed, sidecars)
	}
	final, err := e.env.Safety.Place(ctx, staged, plan.Output)
	if err != nil {
		if ctx.Err() != nil {
			// Whether the new file went in is not known: the .srt records stay, and the next start
			// removes the ones whose new file is not in place (RecoverSidecars).
			return cancelled()
		}
		if errors.Is(err, safety.ErrSourceLeftBehind) {
			// The new file is in place, so its subtitle files belong beside it and the job keeps
			// their names.
			e.move(staged.Path, final)
			placed = true
			e.finishSidecars()
			return Result{
				Outcome: OutcomeSmaller, SizeBefore: e.in.Size, SizeAfter: after, OutputPath: final,
				Reason:  "source.not_removed",
				Message: "The new file is in place, but the original could not be removed.", FailedStep: "save",
			}
		}
		e.removeSidecars(sidecars) // the new file was not placed: our .srt files go again
		return fatal("save.failed", err.Error(), "save", "Check the destination folder.")
	}
	placed = true
	e.move(staged.Path, final)
	e.finishSidecars()
	if final != plan.Output {
		e.warn("The new file was saved as " + filepath.Base(final) + ": a file with the name it was going to use appeared meanwhile, and the subtitle files kept the name for it.")
	}

	// 9. The result is in place; its probe is the last thing the log says.
	e.probeSide(ctx, "after", final)
	return Result{
		Outcome: OutcomeSmaller, SizeBefore: e.in.Size, SizeAfter: after, OutputPath: final,
		Message: "Saved " + filepath.Base(final),
	}
}

// recheck compares the file to the scan's record. A file that moved on is looked at once more,
// so a copy that just finished is converted now and a file still being written goes back to the
// next scan.
func (e *engine) recheck(ctx context.Context) Result {
	if res := ctxStop(ctx); res.Outcome != "" {
		return res
	}
	src := e.in.Source
	lst, err := os.Lstat(src)
	if os.IsNotExist(err) {
		return skip("file.gone", "The file is gone.")
	}
	if err != nil {
		return fatal("file.unreadable", fmt.Sprintf("Cannot read %s: %v", src, err), "", "")
	}
	if lst.Mode()&os.ModeSymlink != 0 {
		return skip("file.linked", "The file is a link, and links are never converted.")
	}
	if !fsx.Inside(src, e.in.LibraryRoot) {
		return skip("file.outside_library", "The file is not inside its library.")
	}
	if ours(src) {
		return skip("file.ours", "The file is one this program made.")
	}
	if links, err := fsx.LinkCount(src); err == nil && links > 1 {
		return skip("file.shared", "The file has more than one name, so it is left alone.")
	}
	st, err := os.Stat(src)
	if err != nil {
		return fatal("file.unreadable", fmt.Sprintf("Cannot read %s: %v", src, err), "", "")
	}
	if e.in.FileID != "" {
		id, err := fsx.FileID(src)
		if err != nil {
			return fatal("file.unreadable", fmt.Sprintf("Cannot identify %s: %v", src, err), "", "")
		}
		if id != e.in.FileID {
			return skip("file.changed", "The file was replaced since it was scanned.")
		}
	}
	if st.Size() == e.in.Size && (e.in.MTime.IsZero() || st.ModTime().Equal(e.in.MTime)) {
		return Result{}
	}

	size, mtime := st.Size(), st.ModTime()
	e.env.sleep(recheckLook)
	st2, err := os.Stat(src)
	if os.IsNotExist(err) {
		return skip("file.gone", "The file is gone.")
	}
	if err != nil {
		return fatal("file.unreadable", fmt.Sprintf("Cannot read %s: %v", src, err), "", "")
	}
	if st2.Size() != size || !st2.ModTime().Equal(mtime) {
		return Result{Outcome: OutcomeRequeue, Reason: "file.still_changing",
			Message: "The file is still being written, so the next scan will look at it again."}
	}
	return skip("file.changed", "The file changed since it was scanned.")
}

// ours is the staging-folder rule: a hidden file, or a path inside a *.syp-tmp folder, is this
// program's own work and is never converted.
func ours(path string) bool {
	if strings.HasPrefix(filepath.Base(path), ".") {
		return true
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.HasSuffix(strings.ToLower(part), ".syp-tmp") {
			return true
		}
	}
	return false
}

// probeSource reads the file and writes the "before" probe event.
func (e *engine) probeSource(ctx context.Context) (media.Info, Result) {
	info, err := e.env.Media.Probe(ctx, e.in.Source)
	if err != nil {
		if ctx.Err() != nil {
			return media.Info{}, cancelled()
		}
		if errors.Is(err, media.ErrNotMedia) {
			return media.Info{}, skip("source.unreadable", "The file could not be read as media.")
		}
		// ffprobe missing or crashing says nothing about this file (L-SAFE-5).
		return media.Info{}, Result{Outcome: OutcomeMayPass, Reason: "source.uncheckable",
			Message: fmt.Sprintf("The file could not be read: %v", err), NextAction: "retry"}
	}
	e.probeEvent("before", info)
	return info, Result{}
}

// revision loads the saved flow the job names. A try brings its own flow, which has no revision.
func (e *engine) revision(ctx context.Context) (Revision, error) {
	if e.in.Flow != nil {
		return Revision{Flow: *e.in.Flow}, nil
	}
	if e.env.LoadRevision == nil {
		return Revision{}, errors.New("no revision loader")
	}
	f, err := e.env.LoadRevision(ctx, e.in.WorkflowID, e.in.Revision)
	if err != nil {
		return Revision{}, err
	}
	return Revision{WorkflowID: e.in.WorkflowID, Rev: e.in.Revision, Flow: f}, nil
}

// plan frames every step with step_start, decision and step_end, exactly like a run's log should
// read even when a step's plan refuses.
func (e *engine) plan(ctx context.Context, rev Revision, info media.Info) (Decisions, encode.Plan, Result) {
	var d Decisions
	var plan encode.Plan
	if problems := shapeProblems(rev.Flow); len(problems) > 0 {
		return d, plan, fatal("invalid_flow", strings.Join(problems, " "), "", "")
	}
	e.replace = saveReplaces(rev.Flow)
	for i, ref := range rev.Flow.Steps {
		if res := ctxStop(ctx); res.Outcome != "" {
			return d, plan, res
		}
		name := ref.Type
		if st, ok := stepByType(ref.Type); ok {
			name = st.Def().Name
		}
		started := e.env.now()
		e.stepEvent(events.KindStepStart, ref, name, i+1, events.LevelInfo, "", stepSentence(i+1, name), 0, nil)

		step, settings, err := decodeStep(ref)
		if err != nil {
			dur := e.env.now().Sub(started)
			e.stepEvent(events.KindStepEnd, ref, name, i+1, events.LevelError, "setting.invalid", err.Error(), dur, nil)
			return d, plan, fatal("invalid_settings", err.Error(), ref.ID, "")
		}
		decision, err := step.Plan(settings, PlanInput{Info: info, Env: e.env}, &plan)
		dur := e.env.now().Sub(started)
		if err != nil {
			pe := planError(ref.ID, err)
			e.stepEvent(events.KindStepEnd, ref, name, i+1, events.LevelError, pe.Code, pe.Message, dur, nil)
			return d, plan, Result{Outcome: OutcomeFatal, Reason: pe.Code, Message: pe.Message,
				FailedStep: pe.Step, NextAction: pe.NextAction}
		}
		sd := StepDecision{ID: ref.ID, Type: ref.Type, Name: name, Version: ref.Version, Index: i + 1, Decision: decision}
		d.Steps = append(d.Steps, sd)
		e.decisionEvent(sd)
		e.stepEvent(events.KindStepEnd, ref, name, i+1, events.LevelInfo,
			decision.Reason, decision.Label, dur, decision.Detail)
	}
	return d, plan, Result{}
}

// pick chooses this attempt's encoder. Nil means stage 1's processor picker.
func (e *engine) pick(plan encode.Plan) (encode.Rung, error) {
	if e.env.Pick == nil {
		return ProcessorPick(Want{Codec: plan.Video.Codec})
	}
	return e.env.Pick(Want{Codec: plan.Video.Codec})
}

// encodeOwner is the step that owns the encoder: the video step, or the audio step when only the
// audio is doing work.
func (e *engine) encodeOwner(d Decisions) string {
	for _, s := range d.Steps {
		if s.ID == "video" && s.Decision.Work {
			return "video"
		}
	}
	for _, s := range d.Steps {
		if s.ID == "audio" && s.Decision.Work {
			return "audio"
		}
	}
	return "video"
}

// verifyWant is what the result must keep: the audio the plan writes (each with its language and
// channels, since a conversion changes the channels) and the subtitle languages it maps.
func (e *engine) verifyWant(plan encode.Plan, info media.Info) (safety.Want, error) {
	audio, _, err := encode.AudioOutputs(plan, info)
	if err != nil {
		return safety.Want{}, err
	}
	_, subtitles, err := encode.KeptStreams(plan, info)
	if err != nil {
		return safety.Want{}, err
	}
	subByIndex := make(map[int]media.SubtitleTrack, len(info.Subtitles))
	for _, s := range info.Subtitles {
		subByIndex[s.Index] = s
	}
	var want safety.Want
	for _, a := range audio {
		want.Audio = append(want.Audio, safety.WantedAudio{Language: a.Language, Channels: a.Channels})
	}
	for _, i := range subtitles {
		want.SubtitleLanguages = append(want.SubtitleLanguages, subByIndex[i].Language)
	}
	return want, nil
}

// loudnessBudget is L-AUD-15's cap on the whole analysis: ten minutes across every track. Past it,
// every track is left un-normalized rather than half-normalized.
const loudnessBudget = 10 * time.Minute

// measureLoudness fills in each converting track's loudness before Build writes the filter. A
// failed measurement leaves that track as it is; running past the budget drops them all.
func (e *engine) measureLoudness(ctx context.Context, plan *encode.Plan, info media.Info) Result {
	var targets []int
	for i, a := range plan.Audio {
		if a.Action != encode.StreamConvert || !a.Loudnorm {
			continue
		}
		// A track whose rate is unknown cannot be measured without guessing the rate to restore
		// afterwards (L-AUD-15).
		if t := sourceAudio(info.Audio, a.Index); t == nil || t.SampleRate <= 0 {
			continue
		}
		targets = append(targets, i)
	}
	if len(targets) == 0 {
		return Result{}
	}
	deadline := e.env.now().Add(loudnessBudget)
	for _, i := range targets {
		if res := ctxStop(ctx); res.Outcome != "" {
			return res
		}
		remaining := deadline.Sub(e.env.now())
		if remaining <= 0 {
			clearLoudness(plan)
			e.warn("Evening out loudness took too long, so no track's loudness is changed.")
			return Result{}
		}
		mctx, cancel := context.WithTimeout(ctx, remaining)
		run, err := e.env.Media.Run(mctx, encode.LoudnessArgs(e.in.Source, plan.Audio[i].Index), nil)
		timedOut := mctx.Err() != nil
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return cancelled()
			}
			if timedOut {
				clearLoudness(plan)
				e.warn("Evening out loudness took too long, so no track's loudness is changed.")
				return Result{}
			}
			e.warn("A track's loudness could not be measured, so it is kept as it is.")
			continue
		}
		l, perr := encode.ParseLoudness(run.KeyLines)
		if perr != nil {
			e.warn("A track's loudness could not be measured, so it is kept as it is.")
			continue
		}
		plan.Audio[i].Loudness = l
	}
	return Result{}
}

// clearLoudness forgets every measurement, so Build writes no loudnorm filter at all.
func clearLoudness(plan *encode.Plan) {
	for i := range plan.Audio {
		plan.Audio[i].Loudness = nil
	}
}

func sourceAudio(tracks []media.AudioTrack, index int) *media.AudioTrack {
	for i := range tracks {
		if tracks[i].Index == index {
			return &tracks[i]
		}
	}
	return nil
}

// writeRaw writes ffmpeg's tail to the raw file.
func (e *engine) writeRaw(r media.Result) {
	if e.env.Raw == nil || len(r.KeyLines) == 0 {
		return
	}
	for _, line := range r.KeyLines {
		fmt.Fprintln(e.env.Raw, line)
	}
}

// The events. Each one names the job and the file's library and worker, so a log line read on its
// own still says where it came from.

func (e *engine) emit(ev events.Event) {
	if e.env.Bus == nil {
		return
	}
	ev.JobID = e.in.JobID
	ev.Attempt = e.in.Attempt
	ev.LibraryID = e.in.LibraryID
	ev.WorkerID = e.in.WorkerID
	ev.TS = e.env.now()
	e.env.Bus.Emit(ev)
}

func (e *engine) runStart() {
	e.emit(events.Event{
		Kind: events.KindRunStart, Level: events.LevelInfo,
		Message: "Starting " + filepath.Base(e.in.Source),
		Data: map[string]any{
			"workflowId": e.in.WorkflowID,
			"revision":   e.in.Revision,
			"source":     e.in.Source,
			"appVersion": e.env.AppVersion,
		},
	})
}

func (e *engine) runEnd(res Result) {
	level := events.LevelInfo
	switch res.Outcome {
	case OutcomeFatal:
		level = events.LevelError
	case OutcomeMayPass, OutcomeCancelled, OutcomeRequeue:
		level = events.LevelWarn
	}
	e.emit(events.Event{
		Kind: events.KindRunEnd, Level: level,
		Message: res.Message, Reason: res.Reason, NextAction: res.NextAction,
		SizeBefore: res.SizeBefore, SizeAfter: res.SizeAfter,
		DurationMS: e.env.now().Sub(e.started).Milliseconds(),
		Data: map[string]any{
			"outcome":    string(res.Outcome),
			"outputPath": res.OutputPath,
			"failedStep": res.FailedStep,
		},
	})
}

func (e *engine) probeEvent(side string, info media.Info) {
	e.emit(events.Event{Kind: events.KindProbe, Level: events.LevelInfo, Message: probeSentence(info),
		Data: map[string]any{"side": side, "tracks": media.Summarize(info)}})
}

// probeSide is the probe after a placement. Its failure is not a run failure: the file is in
// place either way, so the event is only written when the probe answers.
func (e *engine) probeSide(ctx context.Context, side, path string) {
	info, err := e.env.Media.Probe(ctx, path)
	if err != nil {
		return
	}
	e.probeEvent(side, info)
}

func (e *engine) stepEvent(kind events.Kind, ref StepRef, name string, index int, level events.Level,
	code, message string, dur time.Duration, data map[string]any) {
	e.emit(events.Event{
		Kind: kind, Level: level, Code: code, Message: message,
		StepID: ref.ID, StepType: ref.Type, StepName: name, StepIndex: index,
		DurationMS: dur.Milliseconds(), Data: data,
	})
}

func (e *engine) decisionEvent(sd StepDecision) {
	port, label := "nothing", sd.Decision.Label
	if sd.Decision.Work {
		port = "work"
	}
	e.emit(events.Event{
		Kind: events.KindDecision, Level: events.LevelInfo,
		StepID: sd.ID, StepType: sd.Type, StepName: sd.Name, StepIndex: sd.Index,
		Port: port, PortLabel: label, Reason: sd.Decision.Reason, Message: label,
		Data: sd.Decision.Detail,
	})
	// A note explains why the plan is not exactly what the settings asked for (a language that
	// matched nothing, a track kept so the file keeps its sound). It is a warning, not a refusal.
	for _, note := range sd.Decision.Notes {
		e.warn(note)
	}
}

// warn writes a warning line for the job log.
func (e *engine) warn(message string) {
	e.emit(events.Event{Kind: events.KindWarning, Level: events.LevelWarn, Message: message})
}

func (e *engine) command(argv []string) {
	line := commandLine(argv)
	e.emit(events.Event{Kind: events.KindCommand, Level: events.LevelInfo, Message: line,
		Command: &events.Command{Argv: argv, Line: line}})
}

func (e *engine) size(before, after int64) {
	e.emit(events.Event{Kind: events.KindSize, Level: events.LevelInfo, Message: sizeSentence(before, after),
		SizeBefore: before, SizeAfter: after})
}

func (e *engine) move(from, to string) {
	e.emit(events.Event{Kind: events.KindMove, Level: events.LevelInfo,
		Message: filepath.Base(to), Data: map[string]any{"from": from, "to": to}})
}

// placeSidecars records the extracted subtitles and puts them beside the new file, never over
// another file (L-AUD-25). A record goes for every file not placed; one placed before a failure
// comes back while it is still exactly ours.
func (e *engine) placeSidecars(plan encode.Plan, extracts []encode.StreamPlan, staged safety.Staged) ([]safety.Sidecar, Result) {
	if len(extracts) == 0 {
		return nil, Result{}
	}
	vst, err := os.Stat(staged.Path)
	if err != nil {
		return nil, fatal("save.read_failed", err.Error(), "save", "")
	}
	vhash, err := fsx.SampleHash(staged.Path)
	if err != nil {
		return nil, fatal("save.read_failed", err.Error(), "save", "")
	}
	recs := make([]safety.Sidecar, 0, len(extracts))
	for k, x := range extracts {
		stagedSrt := encode.StagedSidecar(plan.SidecarDir, k)
		st, err := os.Stat(stagedSrt)
		if err != nil {
			return nil, fatal("subtitle.not_saved", err.Error(), "save", "retry")
		}
		h, err := fsx.SHA256(stagedSrt)
		if err != nil {
			return nil, fatal("subtitle.not_saved", err.Error(), "save", "retry")
		}
		recs = append(recs, safety.Sidecar{
			JobID: e.in.JobID, Path: encode.SrtName(plan.Output, x.Part),
			Bytes: st.Size(), MTime: st.ModTime().UnixNano(), Hash: h,
			VideoPath: plan.Output, VideoBytes: vst.Size(), VideoHash: vhash,
		})
	}
	if e.env.Sidecars != nil && e.in.JobID != 0 {
		if err := e.env.Sidecars.AddSidecars(recs); err != nil {
			// The records go in before any .srt is renamed into place and before Place: on this
			// path nothing has changed.
			return nil, fatal("subtitle.record_failed",
				"The subtitle files could not be recorded, so nothing was changed. Press Retry. ("+err.Error()+")",
				"save", "retry")
		}
	}
	var placed []safety.Sidecar
	for k, c := range recs {
		from := encode.StagedSidecar(plan.SidecarDir, k)
		err := renameSidecar(from, c.Path)
		if errors.Is(err, fsx.ErrNotRemoved) {
			// Linked into place, the staged name not removed: the file is placed and ours; the
			// staging folder's cleanup removes the leftover name.
			err = nil
		}
		if err != nil {
			e.dropSidecars(recs[k:])
			e.removeSidecars(placed)
			if errors.Is(err, os.ErrExist) {
				return nil, Result{Outcome: OutcomeFatal, Reason: "save.srt_exists", Message: srtTaken(c.Path),
					FailedStep: "save", NextAction: "retry"}
			}
			return nil, Result{Outcome: OutcomeFatal, Reason: "subtitle.place_failed",
				Message:    "A subtitle file could not be put next to the new file, so nothing was changed.",
				FailedStep: "save", NextAction: "retry"}
		}
		e.move(from, c.Path)
		placed = append(placed, c)
	}
	return placed, Result{}
}

// dropSidecars forgets the records of files that were never placed.
func (e *engine) dropSidecars(list []safety.Sidecar) {
	if e.env.Sidecars == nil {
		return
	}
	for _, c := range list {
		if err := e.env.Sidecars.DropSidecar(c.JobID, c.Path); err != nil {
			e.warn("A subtitle file's record could not be dropped: " + err.Error())
		}
	}
}

// removeSidecars removes the .srt files this run placed, each only while it is still exactly ours;
// the record of one that is gone or not ours goes too. One that cannot be removed keeps its
// record, so the next start tries again.
func (e *engine) removeSidecars(list []safety.Sidecar) {
	for _, c := range list {
		removed, err := safety.RemoveSidecarIfOurs(c)
		if err != nil {
			e.warn("Could not remove the subtitle file " + c.Path + " put there before the new file was placed: " + err.Error() +
				". It is kept on record and removed at the next start.")
			continue
		}
		if removed {
			e.emit(events.Event{Kind: events.KindMove, Level: events.LevelInfo,
				Message: "Removed " + filepath.Base(c.Path), Data: map[string]any{"from": c.Path}})
		}
		if e.env.Sidecars != nil {
			if err := e.env.Sidecars.DropSidecar(c.JobID, c.Path); err != nil {
				e.warn("A subtitle file's record could not be dropped: " + err.Error())
			}
		}
	}
}

// finishSidecars is the place done: the job keeps the names of its .srt files and their records go.
func (e *engine) finishSidecars() {
	if e.env.Sidecars == nil || e.in.JobID == 0 {
		return
	}
	if err := e.env.Sidecars.FinishSidecars(e.in.JobID); err != nil {
		e.warn("The subtitle files were saved, but the job could not keep their names: " + err.Error())
	}
}

// exists says whether anything has this name.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// srtTaken is the sentence for a subtitle file name already there.
func srtTaken(name string) string {
	return fmt.Sprintf("A subtitle file named %s is already there. Nothing was changed. Move or rename that file, then press Retry.", filepath.Base(name))
}

// commandLine is the argv as one line a person can paste into a shell: an argument with nothing
// special in it stays bare, anything else is single-quoted.
func commandLine(argv []string) string {
	parts := make([]string, len(argv))
	for i, arg := range argv {
		parts[i] = quoteArg(arg)
	}
	return strings.Join(parts, " ")
}

func quoteArg(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n'\"\\$&|;<>()*?[]{}!#~`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func cancelled() Result {
	return Result{Outcome: OutcomeCancelled, Reason: "cancelled", Message: "The job was stopped."}
}

func skip(code, message string) Result {
	return Result{Outcome: OutcomeSkip, Reason: code, Message: message}
}

func fatal(code, message, step, next string) Result {
	return Result{Outcome: OutcomeFatal, Reason: code, Message: message, FailedStep: step, NextAction: next}
}

// ctxStop answers a cancelled context with the cancelled result, and continues otherwise.
func ctxStop(ctx context.Context) Result {
	if ctx.Err() != nil {
		return cancelled()
	}
	return Result{}
}
