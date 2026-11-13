// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
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
		out := []map[string]any{}
		for _, st := range steps {
			m := map[string]any{"name": st.Name, "startedAt": st.StartedAt, "tookMs": st.TookMs, "outcome": st.Outcome,
				"command": nilIfEmpty(st.Command), "probe": nil, "check": nil, "outputLines": st.OutputLines, "output": []string{}, "result": nilIfEmpty(st.Result)}
			if json.Valid([]byte(st.Probe)) && st.Probe != "" {
				m["probe"] = json.RawMessage(st.Probe) // the probe summary, as the step recorded it
			}
			if st.CheckScore != nil {
				m["check"] = map[string]any{"score": *st.CheckScore, "min": st.CheckMin, "passed": st.CheckPassed}
			}
			if st.Index == failed { // the last 200 lines of the failed step: the output that says where it stopped
				var mine []string
				for _, l := range lines {
					if l.Step == st.Index && l.Stream != "app" { // ffmpeg's own output, not the worker's words
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
		writeJSON(w, map[string]any{"state": logState(store, id), "steps": out, "final": nilIfEmpty(finalState(store, id))})
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
		var b strings.Builder
		for _, l := range lines {
			fmt.Fprintf(&b, "%s %s\n", l.At, l.Text)
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="job-%d.log"`, id))
		w.Write([]byte(b.String()))
	})
}

// finalState is how the job ended, as its status names it ("" while it has no status: a removed job).
func finalState(store *jobs.Store, id int64) string {
	j, err := store.Get(id)
	if err != nil {
		return ""
	}
	return string(j.Status)
}
