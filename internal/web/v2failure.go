// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// failureView is a failed job's summary as the API shows it (user review 11-13 item E): the step it stopped
// at, after how long, the exit code and the last five error lines of ffmpeg; nil when the job has none.
func failureView(store *jobs.Store, id int64, errorCode, path, outputPath, message string) map[string]any {
	f, err := store.FailureOf(id)
	if err != nil || f == nil {
		return nil
	}
	_, hint := jobs.FailureWords(failureCode(store, id, errorCode), failureFacts(store, id, path, outputPath, message))
	name := ""
	if steps, err := store.RunSteps(id); err == nil && f.Step >= 0 && f.Step < len(steps) {
		name = steps[f.Step].Name
	}
	keys := f.KeyLines
	if len(keys) > 5 {
		keys = keys[len(keys)-5:]
	}
	if keys == nil {
		keys = []string{}
	}
	var media, hintOut, exit any
	if f.MediaTime != "" {
		media = f.MediaTime
	}
	if hint != "" {
		hintOut = hint
	}
	if f.ExitCode >= 0 {
		exit = f.ExitCode
	}
	// ffmpegRan is false when ffmpeg never started (the UI says "ffmpeg didn't run."); exitCode is null then, and when a signal ended it
	return map[string]any{"step": name, "stepIndex": f.Step, "stepCount": f.StepCount, "afterMs": f.AfterMs,
		"mediaTime": media, "hint": hintOut, "exitCode": exit, "ffmpegRan": f.FfmpegRan, "keyLines": keys}
}

// logState is where job id's log stands (user review 11-13 item E): coming_back while the job runs (its lines still
// arrive), complete when they are in the database, lost when its worker stopped and there are none, else none.
// expired (its lines pruned by logKeepDays) comes with the retention setting.
func logState(store *jobs.Store, id int64, status jobs.Status, errorCode string) string {
	if status == jobs.Running {
		return "coming_back"
	}
	n, err := store.LogLineCount(id)
	switch {
	case err == nil && n > 0:
		return "complete"
	case errorCode == jobs.ReasonWorkerLost:
		return "lost"
	}
	if steps, err := store.RunSteps(id); err == nil && len(steps) > 0 { // it had a log, and logKeepDays has pruned it
		return "expired"
	}
	return "none"
}

// failureCode is a failed job's code in design's V6b names, from its stored code and its ffmpeg key lines.
func failureCode(store *jobs.Store, id int64, errorCode string) string {
	var lines []string
	if f, err := store.FailureOf(id); err == nil && f != nil {
		lines = f.KeyLines
	}
	return jobs.FailureCode(errorCode, lines)
}

// failureFacts is what a failed job's sentence can name: its file, its output folder, and the summary's details.
func failureFacts(store *jobs.Store, id int64, path, outputPath, message string) jobs.FailureFacts {
	facts := jobs.FailureFacts{Path: path, ExitCode: -1, Stored: message}
	if f, err := store.FailureOf(id); err == nil && f != nil {
		facts.ExitCode, facts.MediaTime, facts.KeyLines, facts.FfmpegRan = f.ExitCode, f.MediaTime, f.KeyLines, f.FfmpegRan
	}
	// the folder a write was refused in: the new file's, from ffmpeg's own line; else the output path's, when it is
	// not the source (a replace writes next to the source: then no folder is named). Never the source's folder.
	facts.Folder = jobs.DeniedFolder(facts.KeyLines)
	if facts.Folder == "" && outputPath != "" && outputPath != path {
		facts.Folder = filepath.Dir(outputPath)
	}
	// no_space: the needed and free space for the sentence figures
	if facts.Folder != "" {
		if j, err := store.Get(id); err == nil && j.SizeBefore > 0 {
			facts.Needed = worker.SpaceNeeded(j.SizeBefore)
			facts.Free, _ = worker.DiskFree(facts.Folder)
		}
	}
	// the worker's display name (P3: encoder message names the worker)
	if j, err := store.Get(id); err == nil && j.Worker != "" {
		if j.Worker == "local" {
			facts.WorkerName = "this computer"
		} else {
			facts.WorkerName = j.Worker
		}
	}
	return facts
}

// FailureSentence is job id's failure sentence as the Jobs page shows it, for a notification; "" when the job is not there.
func FailureSentence(store *jobs.Store, id int64) string {
	j, err := store.Get(id)
	if err != nil || j.Error == "" {
		return ""
	}
	events, err := store.JobEvents(id) // the code and stored words are on the last event, as the Jobs detail reads them
	if err != nil || len(events) == 0 {
		return ""
	}
	last := events[len(events)-1]
	// the stored words are the job's Error, as the Jobs list reads them; the event's message can be empty
	facts := failureFacts(store, id, j.Path, j.OutputPath, j.Error)
	msg, _ := jobs.FailureWords(failureCode(store, id, last.Reason), facts)
	known := []string{facts.Folder, j.Path, j.OutputPath, j.Library}
	if o, err := store.OriginalOfJob(id); err == nil { // the file a Convert again found in place of (its Dest)
		known = append(known, o.Path, o.NewPath, o.Dest())
	}
	return withoutKnownFolders(msg, known...)
}

// withoutKnownFolders replaces each path the job knows (and its folder) with its last name, exactly, longest first,
// so a folder with spaces in its name leaves nothing behind. A notification leaves the server, so it names files only.
func withoutKnownFolders(msg string, paths ...string) string {
	seen := map[string]bool{}
	var olds []string
	add := func(p string) {
		if strings.Trim(p, `/\`) == "" || seen[p] {
			return
		}
		seen[p] = true
		olds = append(olds, p)
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		add(p)
		t := strings.TrimRight(p, `/\`)
		if i := strings.LastIndexAny(t, `/\`); i > 0 {
			add(t[:i]) // the folder
		}
	}
	sort.SliceStable(olds, func(a, b int) bool { return len(olds[a]) > len(olds[b]) })
	var pairs []string
	for _, o := range olds {
		pairs = append(pairs, o, jobs.LastName(o))
	}
	return strings.NewReplacer(pairs...).Replace(msg)
}

// failureMessage is a failed job's sentence in design's words (the stored words for a code the table does not name).
func failureMessage(store *jobs.Store, id int64, errorCode, path, outputPath, stored string) string {
	msg, _ := jobs.FailureWords(failureCode(store, id, errorCode), failureFacts(store, id, path, outputPath, stored))
	return msg
}
