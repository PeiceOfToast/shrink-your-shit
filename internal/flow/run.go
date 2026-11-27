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

	// 4. Build the command.
	if res = ctxStop(ctx); res.Outcome != "" {
		return res
	}
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
	argv := append([]string{"ffmpeg"}, built.Argv...)

	// 5. Stage the output beside its destination. The encoder writes the staged file, not the
	// destination, so nothing appears there until Place has checked the result.
	if res = ctxStop(ctx); res.Outcome != "" {
		return res
	}
	staged, err := e.env.Safety.Stage(plan.Output)
	if err != nil {
		return fatal("save.stage_failed", err.Error(), "save", "Check the destination folder's permissions.")
	}
	placed := false
	defer func() {
		if !placed {
			os.RemoveAll(staged.Folder)
		}
	}()
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

	// 7. The size rule: a result that is not smaller is not worth keeping.
	st, err := os.Stat(staged.Path)
	if err != nil {
		return fatal("save.read_failed", err.Error(), "save", "")
	}
	after := st.Size()
	e.size(e.in.Size, after)
	if after >= e.in.Size {
		return Result{
			Outcome: OutcomeBiggerKept, SizeBefore: e.in.Size, SizeAfter: after,
			Reason: "size.not_smaller", Message: "The new file is not smaller, so the original was kept.",
		}
	}

	// 8. Put it in place, never overwriting anything.
	if res = ctxStop(ctx); res.Outcome != "" {
		return res
	}
	final, err := e.env.Safety.Place(ctx, staged, plan.Output)
	if err != nil {
		if ctx.Err() != nil {
			return cancelled()
		}
		if errors.Is(err, safety.ErrSourceLeftBehind) {
			e.move(staged.Path, final)
			placed = true
			return Result{
				Outcome: OutcomeSmaller, SizeBefore: e.in.Size, SizeAfter: after, OutputPath: final,
				Reason:  "source.not_removed",
				Message: "The new file is in place, but the original could not be removed.", FailedStep: "save",
			}
		}
		return fatal("save.failed", err.Error(), "save", "Check the destination folder.")
	}
	placed = true
	e.move(staged.Path, final)

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

// revision loads the saved flow the job names.
func (e *engine) revision(ctx context.Context) (Revision, error) {
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

// verifyWant is what the result must keep: the streams the plan maps, by language.
func (e *engine) verifyWant(plan encode.Plan, info media.Info) (safety.Want, error) {
	audio, subtitles, err := encode.KeptStreams(plan, info)
	if err != nil {
		return safety.Want{}, err
	}
	byIndex := make(map[int]media.AudioTrack, len(info.Audio))
	for _, a := range info.Audio {
		byIndex[a.Index] = a
	}
	subByIndex := make(map[int]media.SubtitleTrack, len(info.Subtitles))
	for _, s := range info.Subtitles {
		subByIndex[s.Index] = s
	}
	var want safety.Want
	for _, i := range audio {
		want.AudioLanguages = append(want.AudioLanguages, byIndex[i].Language)
	}
	for _, i := range subtitles {
		want.SubtitleLanguages = append(want.SubtitleLanguages, subByIndex[i].Language)
	}
	return want, nil
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
