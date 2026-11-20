// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// R8: a new file bigger than its original is kept when the flow says keep, whatever the output, with both sizes in
// the log and the reason. A smaller file, or an equal one, is never touched by the choice.
func TestSizeRuleKeepsABiggerNewFile(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "new.tmp")
	if err := os.WriteFile(tmp, make([]byte, 1500), 0o644); err != nil {
		t.Fatal(err)
	}
	st, id := realJob(t, filepath.Join(dir, "a.mkv"), 1)
	w := realWorker(st, media.OSExec{})

	for _, output := range []string{"replace", "beside", "folder"} {
		err := w.sizeRule(id, rules.Plan{KeepIfBigger: true, Output: output}, tmp, 1000)
		var sk *SkippedError
		if !errors.As(err, &sk) {
			t.Fatalf("%s: a bigger new file with keep: %v, want the file left alone", output, err)
		}
		// Skipped, not failed: the workflow chose to keep the original, so the job carries its own reason.
		if sk.Code != jobs.ReasonNewFileBigger {
			t.Errorf("%s: code %q, want %q", output, sk.Code, jobs.ReasonNewFileBigger)
		}
		want := "The new file (" + rules.HumanBytes(1500) + ") was bigger than the original (" + rules.HumanBytes(1000) + "), so the original was kept"
		if sk.Words != want {
			t.Errorf("%s: words %q, want %q", output, sk.Words, want)
		}
		assertPlain(t, sk.Words)
	}
	lines, err := st.LogLines(id)
	if err != nil {
		t.Fatal(err)
	}
	kept := 0
	for _, l := range lines {
		if strings.Contains(l.Text, "keeping the original (this workflow keeps originals when the new file is bigger)") {
			kept++
		}
	}
	if kept != 3 {
		t.Errorf("%d keep lines in the log, want one per output", kept)
	}
}

func TestSizeRuleReplaceAnywayAndTheRest(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "new.tmp")
	st, id := realJob(t, filepath.Join(dir, "a.mkv"), 1)
	w := realWorker(st, media.OSExec{})

	// bigger, replace anyway: goes on, and the log says so with both sizes
	if err := os.WriteFile(tmp, make([]byte, 1500), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.sizeRule(id, rules.Plan{Output: "replace"}, tmp, 1000); err != nil {
		t.Fatalf("a bigger file replaced anyway: %v", err)
	}
	// smaller and equal: nothing, with keep or without
	for _, size := range []int{500, 1000} {
		if err := os.WriteFile(tmp, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, keep := range []bool{true, false} {
			if err := w.sizeRule(id, rules.Plan{KeepIfBigger: keep, Output: "replace"}, tmp, 1000); err != nil {
				t.Fatalf("a %d-byte file (keep %v): %v", size, keep, err)
			}
		}
	}
	lines, err := st.LogLines(id)
	if err != nil {
		t.Fatal(err)
	}
	replaced := 0
	for _, l := range lines {
		if strings.Contains(l.Text, "replacing anyway") {
			replaced++
		}
		if strings.Contains(l.Text, "keeping") {
			t.Errorf("a file that is not bigger logged a decision: %q", l.Text)
		}
	}
	if replaced != 1 {
		t.Errorf("%d replace-anyway lines, want 1", replaced)
	}
}

// R8 fix: a bigger new file with "keep" ends as a skipped file, not a failed job: the job record goes, the
// file is in Skipped with reason new_file_bigger and both sizes, the original is untouched and nothing is
// left beside it.
func TestBiggerNewFileEndsSkipped(t *testing.T) {
	s := newScenario(t)
	s.content = strings.Repeat("x", 100) // the new file (100 B) is bigger than the original (14 B)
	w := s.worker(Replace)
	sel := rules.DefaultSelectors(3)
	sel.KeepIfBigger = true
	tmpl := sel.Template()
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if _, err := s.store.Get(s.id); err == nil {
		t.Fatal("the job record is still there; want it gone (skipped)")
	}
	skipped, total, err := s.store.SkippedFiles([]string{jobs.ReasonNewFileBigger}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(skipped) != 1 || skipped[0].Path != s.src {
		t.Fatalf("skipped files: %+v (total %d), want the original", skipped, total)
	}
	want := "skip: The new file (100 B) was bigger than the original (14 B), so the original was kept"
	if skipped[0].Reason != jobs.ReasonNewFileBigger || skipped[0].Decision != want {
		t.Errorf("skipped: reason %q, decision %q; want %q and %q", skipped[0].Reason, skipped[0].Decision, jobs.ReasonNewFileBigger, want)
	}
	got, err := os.ReadFile(s.src)
	if err != nil || string(got) != "original-bytes" {
		t.Errorf("the original: %q, %v; want it untouched", got, err)
	}
	entries, _ := os.ReadDir(s.dir)
	if len(entries) != 1 || entries[0].Name() != filepath.Base(s.src) {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("left in the folder: %v (want only the original)", names)
	}
}
