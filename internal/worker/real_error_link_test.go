// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/flow"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// W9 with real ffmpeg: "Copy the result to a folder" fails (its folder is a file), and its error link runs
// "Write a file beside the result" instead of the rest of the path. The job stays done with the copy failure
// as a warning, the sidecar is beside the saved result, and the success path's notification does not run.
func TestRealCopyFailureRunsItsErrorLink(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "Movie.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=4:size=640x360:rate=25", "-c:v", "libx264", "-b:v", "800k", "-pix_fmt", "yuv420p", src)
	notAFolder := filepath.Join(t.TempDir(), "taken")
	if err := os.WriteFile(notAFolder, []byte("a file, not a folder"), 0o644); err != nil {
		t.Fatal(err)
	}
	sel := rules.DefaultSelectors(3)
	then := rules.Template{Kind: rules.KindSelectors, Selectors: &sel, CompilerVersion: rules.CompilerVersion}
	tmpl := rules.Template{Kind: rules.KindBranches, CompilerVersion: rules.CompilerVersion,
		Branches: &[]rules.Branch{{Then: &then, Path: []string{"in", "conv", "verify", "save"}}}} // no checks: every file takes it
	(*tmpl.Branches)[0].After = []rules.Card{
		{NodeID: "copy", Type: workflow.TypeCopy, Name: "Copy the result to a folder", Config: map[string]any{"folder": notAFolder, "name": "{{title}}"},
			OnError: []rules.Card{{NodeID: "side", Type: workflow.TypeSidecar, Name: "Write a file beside the result",
				Config: map[string]any{"name": "copy-failed.txt", "text": "the copy didn't work"}}}},
		{NodeID: "say", Type: workflow.TypeNotify, Name: "Send a notification", Config: map[string]any{"text": "copied"}},
	}
	st, id := realJob(t, src, 4)
	w := realWorker(st, media.OSExec{})
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tmpl, 0, nil }
	var sent []string
	w.Cards = func(jobs.Job, string) flow.Env {
		return flow.Env{Notify: func(target, kind, text string) (string, error) { sent = append(sent, text); return "Sent", nil }}
	}
	if err := w.run(t); err != nil {
		t.Fatal(err)
	}
	j, _ := st.Get(id)
	if j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(j.OutputPath), "copy-failed.txt")); err != nil || string(b) != "the copy didn't work" {
		t.Errorf("sidecar: %q %v", b, err)
	}
	if len(sent) != 0 {
		t.Errorf("the success path ran after the failed copy: %v", sent)
	}
	if _, err := os.Stat(j.OutputPath); err != nil {
		t.Errorf("the saved result is not in place: %v", err)
	}
	steps, _ := st.Steps(id)
	warned := false
	for _, s := range steps {
		if s.Step == "after" && strings.Contains(s.Words, "Copy the result to a folder didn't work") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no copy warning on the timeline: %+v", steps)
	}
}
