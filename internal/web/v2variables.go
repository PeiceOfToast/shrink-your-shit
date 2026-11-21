// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/vars"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// builtInSample is the file the variable list's example uses when no library using the workflow has
// converted anything yet (sample.builtIn true): the design's own example file.
const builtInSamplePath = "/media/Movie (2019).mkv"

// v2WorkflowVariables is GET /api/v2/workflow/variables?workflow=<id> (design-joblog-vars section 3):
// the names the workflow's {{name}} fields take, with the sample file's values for the editor's live
// example. The page builds the example and checks names itself; this is the list it checks against.
// The workflow's own variables come last, under "Workflow"; a secret's value is never sent.
func v2WorkflowVariables(api *http.ServeMux, store *jobs.Store) {
	api.HandleFunc("GET /api/v2/workflow/variables", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.URL.Query().Get("workflow"), 10, 64)
		if err != nil || id <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such workflow.")
			return
		}
		f, err := store.GetFlow(id)
		if err != nil {
			if errors.Is(err, jobs.ErrNotFound) {
				apiError(w, http.StatusNotFound, "not_found", "There is no such workflow.")
			} else {
				serverError(w, err)
			}
			return
		}
		sample := variableSample(store, id)
		values := sample.values(store, f)
		items := []map[string]any{}
		for _, b := range workflow.Builtins {
			var fields any // the settings a field-only name belongs to (the .srt name's own two); null otherwise
			if len(b.Fields) > 0 {
				fields = b.Fields
			}
			items = append(items, map[string]any{"name": b.Name, "description": b.Description,
				"group": b.Group, "value": valueOrNil(values, b.Name), "fields": fields})
		}
		for _, v := range ownVariables(store, id) {
			items = append(items, map[string]any{"name": v.Name, "description": v.Description,
				"group": "Workflow", "value": ownValue(v), "fields": nil})
		}
		for _, p := range publishedVariables(store, id) { // FB1d: what a step before sets ("Set a variable")
			items = append(items, map[string]any{"name": p.name, "description": p.words, "group": "Set by a step", "value": nil, "fields": nil})
		}
		writeJSON(w, map[string]any{
			"sample":    map[string]any{"path": sample.path, "builtIn": sample.builtIn},
			"variables": items,
		})
	})
}

// publishedVariables are the names the workflow's steps set for the steps after them, as its draft says
// (else its newest published one): a "Set a variable" step's name, with the step's own words.
type published struct{ name, words string }

func publishedVariables(store *jobs.Store, flowID int64) []published {
	var doc *workflow.Doc
	if d, _, err := store.Draft(flowID); err == nil {
		doc = &d.Doc
	} else if rev, ok, err := store.LatestRevision(flowID); err == nil && ok {
		doc = &rev.Doc
	}
	if doc == nil {
		return nil
	}
	var out []published
	seen := map[string]bool{}
	for _, n := range doc.Nodes {
		if n.Type != workflow.TypeSetVariable {
			continue
		}
		name, _ := n.Config["name"].(string)
		if name == "" || seen[name] || !workflow.NamePattern.MatchString(name) {
			continue
		}
		seen[name] = true
		words := "Set by the step “Set a variable”"
		if n.Name != "" {
			words = "Set by the step “" + n.Name + "”"
		}
		if v, _ := n.Config["value"].(string); v != "" {
			words += " to " + v
		}
		out = append(out, published{name, words})
	}
	return out
}

// ownVariables are the workflow's own variables as its draft says (else its newest published one, for
// a workflow whose editor has not been opened yet).
func ownVariables(store *jobs.Store, flowID int64) []workflow.Variable {
	if d, _, err := store.Draft(flowID); err == nil {
		return d.Doc.Variables
	}
	if rev, ok, err := store.LatestRevision(flowID); err == nil && ok {
		return rev.Doc.Variables
	}
	return nil
}

// valueOrNil is a name's example value, null when the sample has none for it: nothing is made up.
func valueOrNil(values map[string]string, name string) any {
	if v, ok := values[name]; ok {
		return v
	}
	return nil
}

// ownValue is a workflow variable's example value: null for a secret (its value is never sent) and
// for one with no value.
func ownValue(v workflow.Variable) any {
	if v.Type == "secret" || v.Value == nil {
		return nil
	}
	return fmt.Sprint(v.Value)
}

// varSample is where the example's values come from: the newest file a library using the workflow
// converted, or the built-in sample when none has been.
type varSample struct {
	path    string
	builtIn bool
	job     jobs.Job
	lib     jobs.Library
	info    *media.Info // nil when only the path is known
}

func variableSample(store *jobs.Store, flowID int64) varSample {
	j, ok, err := store.LatestConvertedIn(flowID)
	if err != nil || !ok {
		s := varSample{path: filepath.FromSlash(builtInSamplePath), builtIn: true}
		for _, l := range librariesOf(store, flowID) { // the example's library, when one uses the workflow
			s.lib = l
			break
		}
		return s
	}
	s := varSample{path: j.Path, job: j, info: probeOf(store, j)}
	if l, err := store.GetLibrary(j.LibraryID); err == nil {
		s.lib = l
	}
	return s
}

// librariesOf is the libraries using flowID (none if the list can't be read here: the example just
// goes without).
func librariesOf(store *jobs.Store, flowID int64) []jobs.Library {
	libs, err := store.Libraries()
	if err != nil {
		return nil
	}
	var out []jobs.Library
	for _, l := range libs {
		if l.FlowID == flowID {
			out = append(out, l)
		}
	}
	return out
}

// probeOf is the sample as it was when the job was planned (the job's own "before" probe, which a
// replace keeps), else the newest stored probe of its path.
func probeOf(store *jobs.Store, j jobs.Job) *media.Info {
	var snap rules.Snapshot
	if json.Unmarshal([]byte(j.Plan), &snap) == nil && snap.Source != nil {
		return snap.Source
	}
	if raw, ok := store.LatestProbe(j.Path); ok {
		var info media.Info
		if json.Unmarshal([]byte(raw), &info) == nil {
			return &info
		}
	}
	return nil
}

// values are the example's values: a name is left out (the list shows null) when it isn't known for
// this file, so nothing is made up. They come from the same place a run's do (internal/vars).
func (s varSample) values(store *jobs.Store, flow jobs.Flow) map[string]string {
	info := vars.Source{Path: s.path, Library: s.lib.Name, LibraryRoot: s.lib.Path, SizeBytes: s.job.SizeBefore,
		Info: s.info, Workflow: flow.Name, Worker: s.job.Worker, OutputPath: s.job.OutputPath, NewSize: s.job.SizeAfter}
	if rev, ok, err := store.LatestRevision(flow.ID); err == nil && ok && rev.Number != nil {
		info.Revision = *rev.Number
	}
	var snap rules.Snapshot
	if json.Unmarshal([]byte(s.job.Plan), &snap) == nil {
		switch v := snap.Plan.Video; {
		case v.Copy: // the video is the original's bytes: its own codec
			if s.info != nil {
				if sv := s.info.Video(); sv != nil {
					info.TargetCodec = sv.Codec
				}
			}
		case v.Codec != "": // "" is HEVC, as every Plan before S6 has it
			info.TargetCodec = v.Format
			if info.TargetCodec == "" {
				info.TargetCodec = "hevc"
			}
		}
	}
	return info.Values()
}
