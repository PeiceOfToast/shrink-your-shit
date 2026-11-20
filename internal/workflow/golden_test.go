// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
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
// testdata/golden/fingerprints.json. Regenerate the builder goldens with WORKFLOW_GOLDEN_WRITE=1 go test -run
// TestGolden, only when a change to the shape or the plan is meant. The raw goldens (rawGoldens) are bytes the
// app really stored, copied from git history; WORKFLOW_GOLDEN_WRITE never rewrites them.

const goldenDir = "testdata/golden"

// rawGoldens are stored revision-1 documents, copied byte for byte from what the app wrote (see the PR for the
// commit each came from). Their fingerprints are recorded like the others.
var rawGoldens = []string{
	"rev1-pre-r8-save",     // app at a73fd894 (migration 20): a Save with no ifBigger, from before FillCommitDefaults
	"rev1-legacy-template", // app at fcd01ab9^: a revision 1 of a legacy template, a video.legacy_settings node
}

// goldenSecretPlain is the value the secret-sealed golden seals; fixtureKey is the fixed test key it is sealed with.
const goldenSecretPlain = "test-secret-value"

var fixtureKey = bytes.Repeat([]byte{0x42}, 32)

// sealedFixture is a secret variable's stored form (nonce and sealed, base64, as the database holds it), sealed
// with fixtureKey and a fixed nonce so the golden is stable. Loading the doc must not open it.
func sealedFixture(plain string) map[string]any {
	bc, err := aes.NewCipher(fixtureKey) // a 32-byte key cannot fail
	if err != nil {
		panic(err)
	}
	gcm, err := cipher.NewGCM(bc)
	if err != nil {
		panic(err)
	}
	nonce := bytes.Repeat([]byte{7}, gcm.NonceSize())
	ct := gcm.Seal(nil, nonce, []byte(plain), nil)
	return map[string]any{"nonce": base64.StdEncoding.EncodeToString(nonce), "sealed": base64.StdEncoding.EncodeToString(ct)}
}

// convertNodeOf is the first video.convert node of d (the golden's own edits of a builder doc); the builders
// always have one.
func convertNodeOf(d *Doc) *Node {
	for i := range d.Nodes {
		if d.Nodes[i].Type == TypeConvert {
			return &d.Nodes[i]
		}
	}
	panic("no convert node")
}

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
		"flow-end":                flowEndDoc(),
		"on-error":                onErrorDoc(),
		"secret-sealed":           secretDoc(),
		"legacy-quality":          legacyRateDoc("quality_legacy", map[string]any{"maxBitrate": "medium"}),
		"legacy-vbr":              legacyRateDoc("vbr_legacy", map[string]any{"maxBitrate": "medium", "vbrMaxKbps": 3000}),
	}
}

func branchDocOnly() Doc {
	d, _ := branchDoc(segment(rules.SliderTemplate(2))) // "No" leads to a conversion too
	return d
}

// flowEndDoc has a branch that ends with "Leave the file alone" (flow.end), as the UI's quick fix adds it.
func flowEndDoc() Doc {
	d, _ := branchDoc([]Node{{ID: NewID(), Type: TypeEnd, TypeVersion: 1, Enabled: true, Config: map[string]any{"reason": "Not 4K: left for later."}}})
	return d
}

// onErrorDoc has a convert step whose error port is a retry (onError), not an unwired error.
func onErrorDoc() Doc {
	d := FromTemplate(rules.SliderTemplate(3), nil)
	convertNodeOf(&d).OnError = &ErrorPolicy{Action: "retry", Max: 2, BackoffSeconds: 5, Then: "skip"}
	return d
}

// secretDoc is a workflow with a secret variable as stored after #511: its value is the sealed form.
func secretDoc() Doc {
	d := Preset()
	d.Variables = []Variable{{Name: "api_token", Type: "secret", Value: sealedFixture(goldenSecretPlain), HasValue: true}}
	return d
}

// legacyRateDoc is a convert step with a rate control kept from an earlier build (the *_legacy values).
func legacyRateDoc(rateControl string, cfg map[string]any) Doc {
	d := Preset()
	c := convertNodeOf(&d)
	c.Config["rateControl"] = rateControl
	for k, v := range cfg {
		c.Config[k] = v
	}
	return d
}

func TestGoldenWorkflows(t *testing.T) {
	docs := goldenDocs()
	if os.Getenv("WORKFLOW_GOLDEN_WRITE") == "1" {
		writeGolden(t, docs)
	}
	fps := readFingerprints(t)
	names := make([]string, 0, len(docs)+len(rawGoldens))
	for name := range docs {
		names = append(names, name)
	}
	names = append(names, rawGoldens...)
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
		fps[name] = goldenFingerprint(d)
	}
	for _, name := range rawGoldens { // the stored bytes are the source: never rewritten, only fingerprinted
		raw, err := os.ReadFile(filepath.Join(goldenDir, name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		d, err := Load(raw)
		if err != nil {
			t.Fatalf("%s: load: %v", name, err)
		}
		fps[name] = goldenFingerprint(d)
	}
	b, err := json.MarshalIndent(fps, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goldenDir, "fingerprints.json"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// goldenFingerprint is the plan fingerprint a golden doc compiles to (the same as the test checks).
func goldenFingerprint(d Doc) string {
	tm, _ := ToTemplate(d, rules.Template{})
	return (rules.Snapshot{Plan: tm.Compile(video(1920, 1080))}).Fingerprint()
}

// TestGoldenSecretOpensWithTheTestKey: loading the secret golden does not open it (that is the job's, with the
// share key); this checks the sealed form really is the test key's, so the fixture is what a sealed value looks like.
func TestGoldenSecretOpensWithTheTestKey(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(goldenDir, "secret-sealed.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d Doc
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	m, ok := d.Variables[0].Value.(map[string]any)
	if !ok {
		t.Fatalf("secret value is %T, want the sealed object", d.Variables[0].Value)
	}
	nonce, _ := base64.StdEncoding.DecodeString(m["nonce"].(string))
	ct, _ := base64.StdEncoding.DecodeString(m["sealed"].(string))
	bc, _ := aes.NewCipher(fixtureKey)
	gcm, _ := cipher.NewGCM(bc)
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil || string(plain) != goldenSecretPlain {
		t.Fatalf("open with the test key: %q %v", plain, err)
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
