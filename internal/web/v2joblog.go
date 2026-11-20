// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/vars"
)

// v2JobLog is a job's log from the database (user review 11-13 item E): GET /api/v2/jobs/{id}/log (its steps,
// the output of a failed step and the final state) and /log/download (every line as text).
func v2JobLog(api *http.ServeMux, store *jobs.Store) {
	api.HandleFunc("GET /api/v2/jobs/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such job.")
			return
		}
		steps, err := store.RunSteps(id)
		if err != nil {
			serverError(w, err)
			return
		}
		lines, err := store.LogLines(id)
		if err != nil {
			serverError(w, err)
			return
		}
		failed := -1
		if f, err := store.FailureOf(id); err == nil && f != nil {
			failed = f.Step
		}
		moves, _ := store.JobMoves(id) // the replace step's file actions, in order (design's moves[])
		j, _ := store.Get(id)
		table := runVariables(j) // design-joblog-vars section 1: the run's values and who set them
		out := []map[string]any{}
		for _, st := range steps {
			m := map[string]any{"name": st.Name, "startedAt": st.StartedAt, "tookMs": st.TookMs, "outcome": st.Outcome,
				"command": nilIfEmpty(st.Command), "probe": nil, "check": nil, "outputLines": st.OutputLines, "output": []string{}, "result": nilIfEmpty(st.Result),
				"found": checkFound(lines, st.Index, st.StartedAt)}
			if json.Valid([]byte(st.Probe)) && st.Probe != "" {
				m["probe"] = json.RawMessage(st.Probe) // the probe summary, as the step recorded it
			}
			if st.CheckScore != nil {
				m["check"] = map[string]any{"score": *st.CheckScore, "min": st.CheckMin, "passed": st.CheckPassed}
			}
			if st.Name == "replace" && len(moves) > 0 {
				m["moves"] = movesJSON(moves)
			}
			if set := stepSet(st.Name, table); len(set) > 0 {
				m["set"] = set
			}
			if st.Index == failed { // the last 200 lines of the failed step: the output that says where it stopped
				var mine []string
				for _, l := range lines {
					if l.Step == st.Index && l.Stream != "app" && l.Stream != "info" { // ffmpeg's own output, not the worker's words
						mine = append(mine, l.Text)
					}
				}
				if len(mine) > 200 {
					mine = mine[len(mine)-200:]
				}
				m["output"] = mine
			}
			out = append(out, m)
		}
		final := finalState(store, id)
		if len(steps) == 0 && len(lines) > 0 {
			out = append(out, importedStep(lines, final))
		}
		writeJSON(w, map[string]any{"state": logState(store, id, j.Status, ""), "steps": out, "final": nilIfEmpty(final),
			"start": startJSON(store, id, j, lines), "variables": variablesJSON(table), "sections": sectionsJSON(lines),
			"summary": summaryJSON(store, j, lines)})
	})
	api.HandleFunc("GET /api/v2/jobs/{id}/log/download", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such job.")
			return
		}
		lines, err := store.LogLines(id)
		if err != nil {
			serverError(w, err)
			return
		}
		j, jerr := store.Get(id)
		if jerr != nil && len(lines) == 0 { // no job and no log: nothing to download
			apiError(w, http.StatusNotFound, "not_found", "There is no such job.")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, logFileName(id, j.Path)))
		w.Write([]byte(jobLogText(j, id, lines)))
	})
}

// checkFound is what a step's check wrote to the log in this attempt, in order (its "check" lines): the drawer's
// "What the check found" for a kept job, which has no failure output. A retry keeps the earlier attempt's lines
// under the same step index, so only lines from the step's current start on count (both are RFC 3339 UTC, so they
// compare as times). [] when there are none, never null.
func checkFound(lines []jobs.LogLine, step int, startedAt string) []string {
	out := []string{}
	for _, l := range lines {
		if l.Step == step && l.Stream == "check" && atOnOrAfter(l.At, startedAt) {
			out = append(out, l.Text)
		}
	}
	return out
}

// movesJSON is the replace step's file actions in design's shape: what was done, to which file, and whether it
// worked, in the order the worker did them.
func movesJSON(moves []jobs.Move) []map[string]any {
	out := make([]map[string]any, 0, len(moves))
	for _, m := range moves {
		out = append(out, map[string]any{"action": m.Action, "from": nilIfEmpty(m.From), "to": nilIfEmpty(m.To),
			"ok": m.OK, "error": nilIfEmpty(m.Error)})
	}
	return out
}

// runVariables is the log's variables table (design-joblog-vars section 1): the values the worker recorded
// when the job ran, plus the ones only a finished run knows (where the file was saved, its size and how much
// was saved), which the replace step set. Sorted by name.
func runVariables(j jobs.Job) []rules.RunVariable {
	var snap rules.Snapshot
	_ = json.Unmarshal([]byte(j.Plan), &snap)
	out := append([]rules.RunVariable(nil), snap.Variables...)
	for n, v := range vars.EndValues(j.OutputPath, j.SizeBefore, j.SizeAfter) {
		found := false
		for k := range out {
			if out[k].Name == n {
				out[k].Start, out[k].End, out[k].ChangedBy = "", v, "replace"
				found = true
			}
		}
		if !found {
			out = append(out, rules.RunVariable{Name: n, End: v, ChangedBy: "replace"})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

// variablesJSON is the variables table as the log's page reads it: a name the run had no value for is null,
// and a secret has no value at all (it is never recorded, only its name).
func variablesJSON(table []rules.RunVariable) []map[string]any {
	out := make([]map[string]any, 0, len(table))
	for _, v := range table {
		out = append(out, map[string]any{"name": v.Name, "start": nilIfEmpty(v.Start), "end": nilIfEmpty(v.End),
			"changedBy": nilIfEmpty(v.ChangedBy), "secret": v.Secret})
	}
	return out
}

// stepSet is what a step of that name set: the encode makes the target codec; the replace saves the file.
// A secret's value is never here.
func stepSet(step string, table []rules.RunVariable) []map[string]any {
	var out []map[string]any
	for _, v := range table {
		if v.Secret || v.ChangedBy != step {
			continue
		}
		item := map[string]any{"name": v.Name}
		if v.End != "" {
			item["value"] = v.End
		}
		out = append(out, item)
	}
	return out
}

// finalState is how the job ended, as its status names it ("" while it has no status: a removed job).
func finalState(store *jobs.Store, id int64) string {
	j, err := store.Get(id)
	if err != nil {
		return ""
	}
	return string(j.Status)
}

// importedStep is the one step shown for a job whose log came from an old .log file (imported once when job logs
// moved into the database): its lines belong to no step, so without this the drawer showed nothing (design's
// preview check 11-13 05:12Z). Design V6b: "Older log, not split into steps"; no time, duration, command,
// source or picture check; the last 200 lines as output, and outputLines for "Show all N".
func importedStep(lines []jobs.LogLine, final string) map[string]any {
	text := make([]string, 0, len(lines))
	for _, l := range lines {
		text = append(text, l.Text)
	}
	if len(text) > 200 {
		text = text[len(text)-200:]
	}
	return map[string]any{"name": "imported", "startedAt": nil, "tookMs": nil, "outcome": nilIfEmpty(final),
		"command": nil, "probe": nil, "check": nil, "outputLines": len(lines), "output": text, "result": nil, "found": []string{}}
}
