// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The golden workflows are stored shapes, one file each under testdata/golden. Each must load (through Load,
// which upgrades an older shape), validate, compile, and give the plan fingerprint recorded in
// testdata/golden/fingerprints.json. Regenerate both with WORKFLOW_GOLDEN_WRITE=1 go test -run TestGolden,
// only when a change to the shape or the plan is meant.

const goldenDir = "testdata/golden"

// goldenDocs are the documents the golden files are written from: today's builders, so the files are what this
// build stores, not hand-written JSON.
func goldenDocs() map[string]Doc {
	vars := Preset()
	vars.Variables = []Variable{{Name: "season", Type: "text"}}
	return map[string]Doc{
		"preset":                  Preset(),
		"slider-0":                FromTemplate(rules.SliderTemplate(0), nil),
		"slider-4":                FromTemplate(rules.SliderTemplate(4), nil),
		"legacy-template-replace": FromTemplate(rules.LegacyTemplate(rules.Default(), "replace"), nil), // what revision 1 is made from
		"branch-4k-check":         branchDocOnly(),
		"variables":               vars,
	}
}

func branchDocOnly() Doc {
	d, _ := branchDoc(segment(rules.SliderTemplate(2))) // "No" leads to a conversion too
	return d
}

func TestGoldenWorkflows(t *testing.T) {
	docs := goldenDocs()
	if os.Getenv("WORKFLOW_GOLDEN_WRITE") == "1" {
		writeGolden(t, docs)
	}
	fps := readFingerprints(t)
	names := make([]string, 0, len(docs))
	for name := range docs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(goldenDir, name+".json"))
		if err != nil {
			t.Fatalf("%s: %v (write them with WORKFLOW_GOLDEN_WRITE=1)", name, err)
		}
		d, err := Load(raw)
		if err != nil {
			t.Fatalf("%s: load: %v", name, err)
		}
		if r := Validate(d); r.Errors() {
			t.Fatalf("%s: validate: %+v", name, r.Issues)
		}
		// the stored shape is what this build reads: loading it changes nothing
		var plain Doc
		if err := json.Unmarshal(raw, &plain); err != nil {
			t.Fatal(err)
		}
		if got, want := canonical(t, d), canonical(t, plain); got != want {
			t.Errorf("%s: loading changed the stored shape:\n got %s\nwant %s", name, got, want)
		}
		tm, issues := ToTemplate(d, rules.Template{})
		if len(issues) > 0 {
			t.Fatalf("%s: runs as a template: %+v", name, issues)
		}
		got := (rules.Snapshot{Plan: tm.Compile(video(1920, 1080))}).Fingerprint()
		if want, ok := fps[name]; !ok || got != want {
			t.Errorf("%s: plan fingerprint %s, recorded %q", name, got, want)
		}
	}
	if len(fps) != len(names) {
		t.Errorf("fingerprints.json has %d entries for %d golden files", len(fps), len(names))
	}
}

func writeGolden(t *testing.T, docs map[string]Doc) {
	t.Helper()
	if err := os.MkdirAll(goldenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fps := map[string]string{}
	for name, d := range docs {
		b, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(goldenDir, name+".json"), append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		tm, _ := ToTemplate(d, rules.Template{})
		fps[name] = (rules.Snapshot{Plan: tm.Compile(video(1920, 1080))}).Fingerprint()
	}
	b, err := json.MarshalIndent(fps, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goldenDir, "fingerprints.json"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFingerprints(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(goldenDir, "fingerprints.json"))
	if err != nil {
		t.Fatalf("%v (write them with WORKFLOW_GOLDEN_WRITE=1)", err)
	}
	var fps map[string]string
	if err := json.Unmarshal(b, &fps); err != nil {
		t.Fatal(err)
	}
	return fps
}

// canonical is d as JSON with its keys in a fixed order, for comparing two documents.
func canonical(t *testing.T, d Doc) string {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(buf.String())
}
