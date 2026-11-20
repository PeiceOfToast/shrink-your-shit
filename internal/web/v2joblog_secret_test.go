// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The job log never carries a secret's value: its variables entry has no start or end at all (absent, not
// null) and no step's set[] names it. The stored plan here is the worst case, a secret with its value in it:
// the worker never writes one, and the log must not pass it on even if one were there.
func TestJobLogNeverCarriesASecretValue(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/a.mkv"})
	st.Claim("t")
	b, _ := json.Marshal(rules.Snapshot{Variables: []rules.RunVariable{
		{Name: "title", Start: "Movie", End: "Movie (2019)"},
		{Name: "api_key", Start: "s3cret", End: "s3cret", ChangedBy: "replace", Secret: true},
		{Name: "output_path", End: "/m/Movie (2019).mkv", ChangedBy: "replace"},
	}})
	if err := st.RecordPlan(id, string(b), "fp"); err != nil {
		t.Fatal(err)
	}
	st.RecordRunStep(id, jobs.RunStep{Index: 0, Name: "encode", Outcome: "ok"})
	st.RecordRunStep(id, jobs.RunStep{Index: 1, Name: "replace", Outcome: "ok"})
	st.Complete(id, "/m/Movie (2019).mkv", 100)

	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10) + "/log"})
	if strings.Contains(body, "s3cret") {
		t.Fatalf("the log carries a secret's value: %s", body)
	}
	var r struct {
		Variables []map[string]any
		Steps     []map[string]any
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("log %s: %v", body, err)
	}
	var secret, title, out map[string]any
	for _, v := range r.Variables {
		switch v["name"] {
		case "api_key":
			secret = v
		case "title":
			title = v
		case "output_path":
			out = v
		}
	}
	if secret == nil || secret["secret"] != true {
		t.Fatalf("the secret's entry %v in %s", secret, body)
	}
	if _, ok := secret["start"]; ok {
		t.Errorf("a secret has no start at all, got %v", secret)
	}
	if _, ok := secret["end"]; ok {
		t.Errorf("a secret has no end at all, got %v", secret)
	}
	if title == nil || title["start"] != "Movie" || title["end"] != "Movie (2019)" {
		t.Errorf("a normal variable keeps its values: %v", title)
	}
	if out == nil || out["start"] != "" || out["end"] != "/m/Movie (2019).mkv" {
		t.Errorf("a value only the end knows has start \"\" and its end: %v", out)
	}
	for _, s := range r.Steps {
		set, _ := s["set"].([]any)
		for _, item := range set {
			if m, _ := item.(map[string]any); m["name"] == "api_key" {
				t.Errorf("a step's set names the secret: %v", s)
			}
		}
	}
}
