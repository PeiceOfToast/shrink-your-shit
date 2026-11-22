// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/vars"
)

// A portable file is one workflow written out to be read on another machine (the export and import of
// the workflow architecture, section 3.2). It carries the workflow's logic, its drawing, and what the
// workflow needs from the machine it lands on. It never carries the machine's own values: a library's
// overrides and a global variable's value stay where they are, and a secret's value is left out.
const (
	PortableFormat  = "syp-workflow"
	PortableVersion = 1
)

// Portable is the file itself.
type Portable struct {
	Format        string   `json:"format"`
	FormatVersion int      `json:"formatVersion"`
	AppVersion    string   `json:"appVersion"`
	ExportedAt    string   `json:"exportedAt"`
	Name          string   `json:"name"`
	Workflow      Doc      `json:"workflow"`
	Layout        Layout   `json:"layout"`
	Requires      Requires `json:"requires"`
}

// Requires is what the workflow uses from outside itself: the global variables it names, and the version
// of each step type it was made with. An import reads the globals it needs from the machine, not from here.
type Requires struct {
	Globals []string       `json:"globals"`
	Steps   map[string]int `json:"steps"`
}

// Export writes d (with its drawing l) as a portable file. d's secret values are masked, and only the
// workflow's own variables go in: a global is named in requires, never copied.
func Export(name string, d Doc, l Layout, appVersion string, now time.Time) Portable {
	d = Masked(d)
	steps := map[string]int{}
	for _, n := range d.Nodes {
		steps[n.Type] = n.TypeVersion
	}
	return Portable{
		Format:        PortableFormat,
		FormatVersion: PortableVersion,
		AppVersion:    appVersion,
		ExportedAt:    now.UTC().Format(time.RFC3339),
		Name:          name,
		Workflow:      d,
		Layout:        l,
		Requires:      Requires{Globals: globalNames(d), Steps: steps},
	}
}

// Import is a portable file read for this machine, before anything is saved. Doc is the workflow upgraded to
// this build's shape. MissingGlobals are the global variables it names that this machine does not have;
// UnknownSteps are its step types this build does not have. Both are reported, and the draft is still saved.
type Import struct {
	Name           string
	Doc            Doc
	Layout         Layout
	MissingGlobals []string
	UnknownSteps   []string
}

// ReadPortable parses a portable file and checks it against this machine's global variables (globals, by
// name). A file that is not a workflow export, or that is from a newer format than this build reads, is an
// error and nothing is read from it further.
func ReadPortable(raw []byte, globals map[string]Variable) (Import, error) {
	var p Portable
	if err := json.Unmarshal(raw, &p); err != nil {
		return Import{}, errors.New("This file is not a workflow export. Nothing was changed.")
	}
	if p.Format != PortableFormat || p.FormatVersion < 1 {
		return Import{}, errors.New("This file is not a workflow export. Nothing was changed.")
	}
	if p.FormatVersion > PortableVersion {
		return Import{}, fmt.Errorf("This export is in format version %d, which is newer than this version reads (%d). Update shrink-your-shit to open it. Nothing was changed.", p.FormatVersion, PortableVersion)
	}
	body, err := json.Marshal(p.Workflow)
	if err != nil {
		return Import{}, err
	}
	d, err := Load(body) // upgrades the document through the migrations, as a stored one is read
	if err != nil {
		return Import{}, errors.New("This workflow could not be read. Nothing was changed.")
	}
	imp := Import{Name: strings.TrimSpace(p.Name), Doc: d, Layout: p.Layout}
	for _, g := range globalNames(d) {
		if _, ok := globals[g]; !ok {
			imp.MissingGlobals = append(imp.MissingGlobals, g)
		}
	}
	for _, n := range d.Nodes {
		if _, ok := Definition(n.Type); !ok && !slices.Contains(imp.UnknownSteps, n.Type) {
			imp.UnknownSteps = append(imp.UnknownSteps, n.Type)
		}
	}
	return imp, nil
}

// globalNames are the names d's templates use that d does not define itself: the ones it needs the
// machine's global variables for. Built-in names (set by the server) are not globals.
func globalNames(d Doc) []string {
	own := map[string]bool{}
	for _, v := range d.Variables {
		own[v.Name] = true
	}
	for _, name := range PublishedNames(d) {
		own[name] = true
	}
	seen := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			refs, _ := vars.Refs(x)
			for _, r := range refs {
				if !own[r.Name] && !BuiltinName(r.Name) {
					seen[r.Name] = true
				}
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	for _, n := range d.Nodes {
		walk(n.Config)
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}
