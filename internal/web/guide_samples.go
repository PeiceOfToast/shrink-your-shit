// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/preview"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// guideSample is one chosen file under the unsaved answers: its track, or the error that means this file
// was not used (the other files are still answered).
type guideSample struct {
	File  string       `json:"file"`
	Error *sampleError `json:"error,omitempty"`
	*preview.Track
}

// sampleError is the error code and message of a file that was not used.
type sampleError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// answersInvalid is each file's entry when the answers do not validate: the same error for every file, and
// no lines.
func answersInvalid(files []string) []guideSample {
	out := make([]guideSample, 0, len(files))
	for _, f := range files {
		out = append(out, guideSample{File: f, Error: &sampleError{"answers_invalid", "Fix the answers above, then try them on a file."}})
	}
	return out
}

// guideSamples previews the answers' compiled setting on each file of lib (S1). Nothing is converted or
// saved. The setting is compiled as a save would compile it: the library's saved workflow keeps what the
// answers do not own, and the languages are the person's. Encoder speeds come from this computer's
// local worker, and only those.
func guideSamples(store *jobs.Store, prev *preview.Previewer, lib jobs.Library, files []string, doc workflow.Doc, sc workflow.Scopes, r *http.Request) ([]guideSample, error) {
	cur := rules.Template{}
	if f, err := store.GetFlow(lib.FlowID); err == nil {
		cur = f.Template
	} else if !errors.Is(err, jobs.ErrNotFound) {
		return nil, err
	}
	t, _ := workflow.ToTemplateWith(doc, cur, sc)
	t.Languages = strings.Join(store.Languages(), ",")

	speeds, err := store.EncoderSpeeds(encoderSpeedSamples)
	if err != nil {
		return nil, err
	}
	bySpeed := map[string]float64{}
	for _, s := range speeds {
		if s.Worker == "local" {
			bySpeed[s.Encoder] = s.Speed
		}
	}
	speed := func(encoder string) float64 { return bySpeed[encoder] }

	root, err := jobs.RealPath(lib.Path)
	if err != nil {
		root = lib.Path // the walk then says the folder cannot be opened, as the library Preview does
	}
	var dest preview.Destination
	if folder, problem := libraryFolder(t, lib); problem != nil {
		dest.Problem = problem.Message
	} else {
		dest.Folder = folder
	}
	out := make([]guideSample, 0, len(files))
	for _, f := range files {
		path, st, prob := resolveSample(root, f)
		if !prob.ok() {
			out = append(out, guideSample{File: f, Error: &sampleError{prob.code, prob.message}})
			continue
		}
		tr := prev.Track(r.Context(), root, path, st, t, dest, speed)
		out = append(out, guideSample{File: f, Track: &tr})
	}
	return out, nil
}
