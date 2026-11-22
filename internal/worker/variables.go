// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/vars"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// The run's {{name}} values (W4b, design-joblog-vars sections 2 and 3): what the Save the result step's
// fields resolve with, and the variables table the job log shows. The values are the same ones the
// editor's variable list computes (internal/vars), from this file and this workflow.

// runValues are the values a job's templates resolve with, and the variables table its log shows. It
// needs the probe and the plan (the target codec), so it runs once the plan is known.
// published are the names the path's cards set before the conversion (FB1c): they join the values,
// and the table, as values set at the start.
func (w *Worker) runValues(job jobs.Job, src media.Info, plan rules.Plan, published map[string]string, used []workflow.Usage) (map[string]string, []rules.RunVariable, error) {
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
	return end, variableTable(start, end, info, used), nil
}

// resolveForFile is the template as this file resolves it (W2): the workflow's published settings read
// again with the globals as they are now and the file's own reserved names (file.*, source.*) filled.
// It also says which names the settings used and where each value came from, for the variables table.
// A workflow with no typed references keeps the template it came with.
func (w *Worker) resolveForFile(job jobs.Job, src media.Info, tmpl rules.Template) (rules.Template, []workflow.Usage, error) {
	record := func(problem string) { // #561 review ask 2: the job log says what it refused to use
		w.logLine(job.ID, secVariables, jobs.LevelWarning, "The workflow's settings were not filled: "+problem)
	}
	info, err := w.runInfo(job)
	if err != nil {
		// #561 review ask 1: never fall back to the store's template here. It was made with the values
		// from before (a kept file.* reference is the schema's minimum there), so running it would
		// convert with a value nobody chose. A read that may pass is tried again; the next try resolves.
		var unreadable *jobs.SecretUnreadableError
		if errors.As(err, &unreadable) {
			// #561 review 2a: a secret sealed on another computer stays unreadable until someone
			// re-enters it, so a retry can't pass; the job stops in its plain words, the original untouched.
			record("its workflow's secret can't be opened on this computer.")
			return tmpl, nil, failed(unreadableSecretWords(unreadable.Names), err)
		}
		var unset *jobs.SecretUnsetError
		if errors.As(err, &unset) {
			record("its workflow's secret has no value.")
			return tmpl, nil, failed(unsetSecretWords(unset.Names), err)
		}
		// #561 review 2b: the raw error stays out of the job log's words (it can carry SQLite text);
		// failed(..., err) keeps it in the program log.
		record("its workflow could not be read.")
		return tmpl, nil, retryable(failed("This library's setting could not be read, so converting did not start. Your original was not touched. It is tried again shortly.", err), retryGeneral)
	}
	if !workflow.HasRefs(info.Doc) {
		return tmpl, nil, nil
	}
	sc := workflow.Scopes{Library: workflow.VariableMap(info.LibraryVariables), Global: workflow.VariableMap(info.Global),
		File: workflow.FileValues(job.Path, src, job.SizeBefore)}
	resolved, issues, used := workflow.ResolveWith(info.Doc, sc)
	if len(issues) > 0 {
		record(issues[0].Message)
		return tmpl, nil, failed(issues[0].Message+" Nothing was converted, and your original was not touched. Give it a value, or change the workflow's setting, then press Retry.", nil)
	}
	t, iss := workflow.ToTemplateWith(resolved, tmpl, workflow.Scopes{})
	if len(iss) > 0 {
		record(iss[0].Message)
		return tmpl, nil, failed(iss[0].Message+" Nothing was converted, and your original was not touched.", nil)
	}
	return t, used, nil
}

// runFacts are what the path's questions beyond the probe ask about (FB1c, rules.Facts): the run's
// values so far, the file's path and library, whether it shares its data with another name, and when
// it was last changed. A workflow that can't be read gives empty facts, not an error: a question then
// answers No and says why.
func (w *Worker) runFacts(job jobs.Job, src media.Info, st os.FileInfo) rules.Facts {
	info, err := w.runInfo(job)
	if err != nil { // review2-536 P3a: the facts route the file, so say when they come from nothing
		w.jobNote(job.ID, secEncode, jobs.LevelWarning, "(%s): the workflow's details could not be read (%v); its questions answer from the file alone", job.Path, err)
	}
	source := vars.Source{Path: job.Path, Library: info.Library, LibraryRoot: job.Library, SizeBytes: job.SizeBefore,
		Info: &src, Workflow: info.Workflow, Revision: info.Revision, Worker: w.ID, Variables: ownValues(info)}
	f := rules.Facts{Vars: source.Values(), Path: job.Path, Library: info.Library, Tags: w.jobTags(job.ID)} // FB2c: the record's tags, once the store has them
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

// libraryValue is the library's value for v when the workflow lets a library change it (W3), and
// whether there is one. The workflow's kind is the one that counts, so the value is checked and shown
// as the workflow says.
func libraryValue(info jobs.RunInfo, v workflow.Variable) (workflow.Variable, bool) {
	if !v.TemplateParam {
		return workflow.Variable{}, false
	}
	for _, lv := range info.LibraryVariables {
		if lv.Name == v.Name && lv.Value != nil {
			lv.Type = v.Type
			return lv, true
		}
	}
	return workflow.Variable{}, false
}

// ownValues are the variables a run's {{name}} templates take, in the lookup order's reverse so the
// last put wins (vars.Source.Values): the server's global variables first (W2; a template may name one,
// as the editor's check allows), then the workflow's own, a variable the workflow lets a library change
// taking the library's value (W3).
func ownValues(info jobs.RunInfo) []vars.Own {
	out := make([]vars.Own, 0, len(info.Global)+len(info.Variables))
	owned := map[string]bool{} // review-591: an own variable wins in every case, a secret included (Values skips a secret, so the global must never get in)
	for _, v := range info.Variables {
		owned[v.Name] = true
	}
	for _, g := range info.Global {
		if g.Type != "secret" && !owned[g.Name] {
			out = append(out, vars.Own{Name: g.Name, Type: g.Type, Value: g.Value})
		}
	}
	for _, v := range info.Variables {
		if lv, ok := libraryValue(info, v); ok {
			v = lv
		}
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
func variableTable(start, end map[string]string, info jobs.RunInfo, used []workflow.Usage) []rules.RunVariable {
	names := map[string]bool{}
	origin := map[string]string{}
	values := map[string]string{} // a used value the run's own names don't carry (a global, the file's)
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
	for _, g := range info.Global { // W2: a global a template read is the server's, unless the workflow's own of that name won
		if names[g.Name] {
			origin[g.Name] = "server"
		}
	}
	for _, v := range info.Variables {
		names[v.Name] = true
		own[v.Name] = true
		origin[v.Name] = "workflow"
		if _, ok := libraryValue(info, v); ok { // W3: the library's value is the one that ran
			origin[v.Name] = "library"
		}
	}
	// W2: the values the workflow's settings used, and where they came from, so a global reads as the
	// value it gave and not as one the workflow set.
	for _, u := range used {
		if start[u.Name] == "" && end[u.Name] == "" {
			if t := fmt.Sprint(u.Value); t != "" {
				values[u.Name] = t
				names[u.Name] = true
			}
		}
		origin[u.Name] = u.Origin
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	var out []rules.RunVariable
	for _, n := range sorted {
		r := rules.RunVariable{Name: n, Start: start[n], End: end[n], Origin: origin[n]}
		if r.Start == "" && r.End == "" {
			r.Start, r.End = values[n], values[n]
		}
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
