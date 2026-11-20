// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
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
			if b.FieldOnly { // the .srt name takes it and nothing else does, so it is not offered on its own
				continue
			}
			items = append(items, map[string]any{"name": b.Name, "description": b.Description,
				"group": b.Group, "value": values[b.Name], "fields": nil})
		}
		for _, v := range ownVariables(store, id) {
			items = append(items, map[string]any{"name": v.Name, "description": v.Description,
				"group": "Workflow", "value": ownValue(v), "fields": nil})
		}
		writeJSON(w, map[string]any{
			"sample":    map[string]any{"path": sample.path, "builtIn": sample.builtIn},
			"variables": items,
		})
	})
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
// this file, so nothing is made up. Words that name languages are the tags as the file has them.
func (s varSample) values(store *jobs.Store, flow jobs.Flow) map[string]any {
	out := map[string]any{}
	put := func(name, v string) {
		if v != "" {
			out[name] = v
		}
	}
	base := filepath.Base(s.path)
	put("file_name", base)
	put("title", strings.TrimSuffix(base, filepath.Ext(base)))
	put("ext", strings.ToLower(strings.TrimPrefix(filepath.Ext(base), ".")))
	put("folder", filepath.Dir(s.path))
	put("path", s.path)
	put("library", s.lib.Name)
	put("library_root", s.lib.Path)
	put("size_bytes", bytesText(s.job.SizeBefore))
	in := s.info
	if in != nil {
		put("size_bytes", bytesText(in.SizeBytes))
		put("container", strings.TrimPrefix(in.Ext, "."))
		if v := in.Video(); v != nil {
			put("video_codec", v.Codec)
			put("width", intText(v.Width))
			if v.Height > 0 {
				put("height", intText(v.Height))
				put("resolution", intText(v.Height)+"p")
			}
			put("hdr", map[bool]string{true: "yes", false: "no"}[in.IsHDR()])
			put("duration", durationText(in.DurationSec))
			put("bitrate_kbps", intText(in.BitrateKbps))
		}
		put("audio_languages", listText(distinct(in.Audio(), func(s media.Stream) string { return langTag(s.Language) })))
		put("audio_codecs", listText(distinct(in.Audio(), func(s media.Stream) string { return s.Codec })))
		put("audio_channels", listText(distinct(in.Audio(), func(s media.Stream) string { return intText(s.Channels) })))
		put("subtitle_languages", listText(distinct(in.Subtitles(), func(s media.Stream) string { return langTag(s.Language) })))
	}
	put("workflow", flow.Name)
	if rev, ok, err := store.LatestRevision(flow.ID); err == nil && ok && rev.Number != nil {
		put("workflow_revision", strconv.Itoa(*rev.Number))
	}
	put("worker", s.job.Worker)
	put("output_path", s.job.OutputPath)
	put("new_size_bytes", bytesText(s.job.SizeAfter))
	if s.job.SizeBefore > 0 && s.job.SizeAfter > 0 {
		put("saved_percent", percentText(float64(s.job.SizeBefore-s.job.SizeAfter)*100/float64(s.job.SizeBefore)))
	}
	var snap rules.Snapshot
	if json.Unmarshal([]byte(s.job.Plan), &snap) == nil {
		switch v := snap.Plan.Video; {
		case v.Copy: // the video is the original's bytes: its own codec
			if in != nil {
				if sv := in.Video(); sv != nil {
					put("target_codec", sv.Codec)
				}
			}
		case v.Codec != "": // "" is HEVC, as every Plan before S6 has it
			put("target_codec", firstNonEmpty(v.Format, "hevc"))
		}
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// distinct is one field of each stream, first seen first, without repeats or blanks.
func distinct(streams []media.Stream, f func(media.Stream) string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range streams {
		if v := f(s); v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// langTag is a stream's language as the file tags it ("" when untagged, "und" when unknown: neither
// is shown).
func langTag(l string) string {
	if l == "und" {
		return ""
	}
	return l
}

func listText(items []string) string { return strings.Join(items, ", ") }

func intText(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func bytesText(n int64) string {
	if n <= 0 {
		return ""
	}
	return strconv.FormatInt(n, 10)
}

// durationText is a length as the design shows it: 1:45:12.
func durationText(sec float64) string {
	if sec <= 0 {
		return ""
	}
	s := int(math.Round(sec))
	return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
}

// percentText is a percentage to one decimal, without a needless ".0" (62.5, 62, -4.2).
func percentText(p float64) string {
	s := strconv.FormatFloat(p, 'f', 1, 64)
	if strings.HasSuffix(s, ".0") {
		return strings.TrimSuffix(s, ".0")
	}
	return s
}
