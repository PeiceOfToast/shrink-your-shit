// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		var k *KeptError
		if !errors.As(err, &k) {
			t.Fatalf("%s: a bigger new file with keep: %v, want the original kept", output, err)
		}
		if !strings.Contains(k.Reason, "bigger") || !strings.Contains(k.Reason, "original was kept") {
			t.Errorf("%s: reason %q does not say why", output, k.Reason)
		}
		assertPlain(t, k.Reason)
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
