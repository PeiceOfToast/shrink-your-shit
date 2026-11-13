// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import "github.com/shrink-your-shit/shrink-your-shit/internal/jobs"

// failureView is a failed job's summary as the API shows it (user review 11-13 item E): the step it stopped
// at, after how long, the exit code and the last five error lines of ffmpeg; nil when the job has none.
func failureView(store *jobs.Store, id int64) map[string]any {
	f, err := store.FailureOf(id)
	if err != nil || f == nil {
		return nil
	}
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
	var media, hint, exit any
	if f.MediaTime != "" {
		media = f.MediaTime
	}
	if f.Hint != "" {
		hint = f.Hint
	}
	if f.ExitCode >= 0 {
		exit = f.ExitCode
	}
	return map[string]any{"step": name, "stepIndex": f.Step, "stepCount": f.StepCount, "afterMs": f.AfterMs,
		"mediaTime": media, "hint": hint, "exitCode": exit, "keyLines": keys}
}

// logState is whether job id has its log in the database: complete (it has lines) or none.
func logState(store *jobs.Store, id int64) string {
	if n, err := store.LogLineCount(id); err == nil && n > 0 {
		return "complete"
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
