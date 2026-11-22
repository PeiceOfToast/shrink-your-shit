// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var exportNow = time.Date(2026, 11, 22, 1, 0, 0, 0, time.UTC)

// Every golden workflow survives export then import with its logic hash unchanged. A secret's value is masked
// by export, so the hash compared is the masked document's: its value is not in the file.
func TestExportImportRoundTripKeepsLogicHash(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(goldenDir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no golden workflows: %v", err)
	}
	for _, f := range files {
		if filepath.Base(f) == "fingerprints.json" {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		d, err := Load(raw)
		if err != nil {
			t.Fatalf("%s: load: %v", f, err)
		}
		file, err := json.Marshal(Export("golden", d, Layout{}, "test", exportNow))
		if err != nil {
			t.Fatal(err)
		}
		imp, err := ReadPortable(file, nil)
		if err != nil {
			t.Fatalf("%s: import: %v", f, err)
		}
		if got, want := LogicHash(imp.Doc), LogicHash(Masked(d)); got != want {
			t.Errorf("%s: logic hash %s after the round trip, want %s", f, got, want)
		}
	}
}

// A newer format version is refused, and so is a file that is not a workflow export.
func TestNewerFormatVersionIsRefused(t *testing.T) {
	p := Export("x", Preset(), Layout{}, "test", exportNow)
	p.FormatVersion = PortableVersion + 1
	raw, _ := json.Marshal(p)
	if _, err := ReadPortable(raw, nil); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("a format version %d file was read (err %v), want it refused as newer", p.FormatVersion, err)
	}
	for _, bad := range []string{`{"format":"something-else","formatVersion":1}`, `{"formatVersion":1}`, `not json`} {
		if _, err := ReadPortable([]byte(bad), nil); err == nil {
			t.Errorf("%s was read as a workflow export", bad)
		}
	}
}

// An import reports a global the machine does not have, and reports nothing once the machine has it.
func TestImportReportsMissingGlobal(t *testing.T) {
	d := Preset()
	d.Nodes[0].Config = map[string]any{"quality_target": "{{quality_target}}"}
	raw, _ := json.Marshal(Export("x", d, Layout{}, "test", exportNow))

	imp, err := ReadPortable(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(imp.MissingGlobals, []string{"quality_target"}) {
		t.Errorf("missing globals %v, want [quality_target]", imp.MissingGlobals)
	}
	imp, err = ReadPortable(raw, map[string]Variable{"quality_target": {Name: "quality_target", Type: "number"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(imp.MissingGlobals) != 0 {
		t.Errorf("missing globals %v with the global present, want none", imp.MissingGlobals)
	}
}

// A step type this build does not have is reported by name.
func TestImportReportsUnknownStepType(t *testing.T) {
	d := Preset()
	d.Nodes = append(d.Nodes, Node{ID: "x", Type: "video.from_the_future", TypeVersion: 1, Enabled: true, Config: map[string]any{}})
	raw, _ := json.Marshal(Export("x", d, Layout{}, "test", exportNow))
	imp, err := ReadPortable(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(imp.UnknownSteps, "video.from_the_future") {
		t.Errorf("unknown steps %v, want video.from_the_future", imp.UnknownSteps)
	}
}

// A secret's value is never written to the file; a global's value is never in it either.
func TestExportMasksSecretValues(t *testing.T) {
	d := Preset()
	d.Variables = append(d.Variables, Variable{Name: "token", Type: "secret", Value: "hunter2", HasValue: true})
	raw, err := json.Marshal(Export("x", d, Layout{}, "test", exportNow))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hunter2") {
		t.Fatal("the exported file holds a secret's value")
	}
}
