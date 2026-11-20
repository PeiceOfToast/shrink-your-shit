// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/vars"
)

// The run's {{name}} values (W4b, design-joblog-vars sections 2 and 3): what the Save the result step's
// fields resolve with, and the variables table the job log shows. The values are the same ones the
// editor's variable list computes (internal/vars), from this file and this workflow.

// runValues are the values a job's templates resolve with, and the variables table its log shows. It
// needs the probe and the plan (the target codec), so it runs once the plan is known.
// published are the names the path's cards set before the conversion (FB1c): they join the values,
// and the table, as values set at the start.
func (w *Worker) runValues(job jobs.Job, src media.Info, plan rules.Plan, published map[string]string) (map[string]string, []rules.RunVariable, error) {
	info, err := w.runInfo(job)
	if err != nil {
		return nil, nil, err
	}
	before := vars.Source{Path: job.Path, Library: info.Library, LibraryRoot: job.Library, SizeBytes: job.SizeBefore,
		Info: &src, Workflow: info.Workflow, Revision: info.Revision, Worker: w.ID, Variables: ownValues(info)}
	after := before
	after.TargetCodec = targetCodec(plan, src)
	start, end := before.Values(), after.Values()
	for k, v := range published {
		start[k], end[k] = v, v
	}
	return end, variableTable(start, end, info), nil
}

// runFacts are what the path's questions beyond the probe ask about (FB1c, rules.Facts): the run's
// values so far, the file's path and library, whether it shares its data with another name, and when
// it was last changed. A workflow that can't be read gives empty facts, not an error: a question then
// answers No and says why.
func (w *Worker) runFacts(job jobs.Job, src media.Info, st os.FileInfo) rules.Facts {
	info, _ := w.runInfo(job)
	source := vars.Source{Path: job.Path, Library: info.Library, LibraryRoot: job.Library, SizeBytes: job.SizeBefore,
		Info: &src, Workflow: info.Workflow, Revision: info.Revision, Worker: w.ID, Variables: ownValues(info)}
	f := rules.Facts{Vars: source.Values(), Path: job.Path, Library: info.Library}
	if job.LibraryID > 0 {
		f.LibraryID = strconv.FormatInt(job.LibraryID, 10)
	}
	if st != nil {
		f.ModTime, f.HardLinks = st.ModTime(), media.LinkCount(job.Path, st) > 1
	}
	return f
}

// runInfo is what the job's workflow says about it (its library, name, revision and own variables).
func (w *Worker) runInfo(job jobs.Job) (jobs.RunInfo, error) {
	if w.RunInfo == nil {
		return jobs.RunInfo{}, nil
	}
	return w.RunInfo(job)
}

// ownValues are the workflow's own variables as the run uses them.
func ownValues(info jobs.RunInfo) []vars.Own {
	out := make([]vars.Own, 0, len(info.Variables))
	for _, v := range info.Variables {
		out = append(out, vars.Own{Name: v.Name, Type: v.Type, Value: v.Value})
	}
	return out
}

// changedByStep is the step that sets a run value (design's variables table "Set by"): the encode makes
// the target codec; the web adds the saved file's values with the replace step as their step.
var changedByStep = map[string]string{"target_codec": "encode"}

// variableTable is the job log's variables table (design-joblog-vars section 1): every name with a value
// at the start or the end, and the workflow's own variables (a secret's value never recorded, only its
// name). Sorted by name, so the table does not depend on map order.
func variableTable(start, end map[string]string, info jobs.RunInfo) []rules.RunVariable {
	names := map[string]bool{}
	for n, v := range start {
		if v != "" {
			names[n] = true
		}
	}
	for n, v := range end {
		if v != "" {
			names[n] = true
		}
	}
	own := map[string]bool{}
	for _, v := range info.Variables {
		names[v.Name] = true
		own[v.Name] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	var out []rules.RunVariable
	for _, n := range sorted {
		r := rules.RunVariable{Name: n, Start: start[n], End: end[n]}
		if own[n] {
			for _, v := range info.Variables {
				if v.Name == n && v.Type == "secret" {
					r.Secret, r.Start, r.End = true, "", ""
				}
			}
		}
		if r.Start != r.End && !r.Secret {
			r.ChangedBy = changedByStep[n]
		}
		if r.Start == "" && r.End == "" && !r.Secret {
			continue
		}
		out = append(out, r)
	}
	return out
}

// targetCodec is the codec the new file's video has, as the {{target_codec}} name says it: the source's
// own when the video is copied, else the plan's format ("" is HEVC, as every Plan before S6 has it).
func targetCodec(plan rules.Plan, src media.Info) string {
	if plan.Video.Copy {
		if v := src.Video(); v != nil {
			return v.Codec
		}
		return ""
	}
	if plan.Video.Format != "" {
		return plan.Video.Format
	}
	if plan.Video.Codec != "" {
		return "hevc"
	}
	return ""
}

// subtitlesOf are the plan's extracted subtitles as the .srt name template sees them.
func subtitlesOf(plan rules.Plan) []vars.Subtitle {
	files := plan.SubtitleFiles()
	out := make([]vars.Subtitle, len(files))
	for k, o := range files {
		kind := o.Kind
		if kind != "" {
			kind = "." + kind // sub_kind carries its dot: Movie.eng.forced.srt
		}
		out[k] = vars.Subtitle{Part: o.Extract, Lang: o.Language, Kind: kind}
	}
	return out
}

// variableLines writes the job log's variables section: the values the run used, a secret's shown as
// Hidden, and the names that came out empty (a typo or a value this file has not) once. Nothing here
// ever writes a secret's value.
func (w *Worker) variableLines(id int64, table []rules.RunVariable, empty []string) {
	w.logLine(id, secVariables, jobs.LevelInfo, fmt.Sprintf("Variables ({{name}}), %d in this run", len(table)))
	for _, v := range table {
		switch {
		case v.Secret:
			w.logLine(id, secVariables, jobs.LevelInfo, "{{"+v.Name+"}} = Hidden")
		case v.ChangedBy != "" || v.Start != v.End:
			w.logLine(id, secVariables, jobs.LevelInfo, fmt.Sprintf("{{%s}} = %s (at the start: %s)",
				v.Name, valueOrNothing(v.End), valueOrNothing(v.Start)))
		default:
			w.logLine(id, secVariables, jobs.LevelInfo, fmt.Sprintf("{{%s}} = %s", v.Name, v.End))
		}
	}
	if len(empty) > 0 {
		quoted := make([]string, len(empty))
		for k, n := range empty {
			quoted[k] = "{{" + n + "}}"
		}
		w.logLine(id, secVariables, jobs.LevelInfo, fmt.Sprintf("Empty for this file, so a {{name}} of theirs is nothing: %s",
			strings.Join(quoted, ", ")))
	}
}

// valueOrNothing is a variable's value in the log, saying "nothing" for the empty one.
func valueOrNothing(v string) string {
	if v == "" {
		return "nothing"
	}
	return v
}
