// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media/fake"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// fakeReplacer records the swap a run asks for and answers with a set error.
type fakeReplacer struct {
	got    safety.SwapPlan
	called bool
	err    error
}

func (f *fakeReplacer) Swap(ctx context.Context, p safety.SwapPlan) (safety.SwapResult, error) {
	f.called = true
	f.got = p
	return safety.SwapResult{}, f.err
}

// replaceFlow is the starter with its save step set to replace the original.
func replaceFlow(t *testing.T) flow.Flow {
	t.Helper()
	return setReplace(t, starterFlow(t))
}

// setReplace sets a flow's save step to replace the original.
func setReplace(t *testing.T, f flow.Flow) flow.Flow {
	t.Helper()
	for i, ref := range f.Steps {
		if ref.Type != "file.save" {
			continue
		}
		var s map[string]any
		if err := json.Unmarshal(ref.Settings, &s); err != nil {
			t.Fatal(err)
		}
		s["where"] = "replace"
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		f.Steps[i].Settings = b
	}
	return f
}

// TestRunReplaceSwapsTheOriginal: a replace run hands the swap the original, the staged file and the final name, and
// the place step is not used.
func TestRunReplaceSwapsTheOriginal(t *testing.T) {
	root, src, info := libraryFile(t, "clip.mkv", "h264")
	m := &fake.Media{Probes: map[string]media.Info{src: info}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	rp := &fakeReplacer{}
	env := newEnv(m, sf, &recorder{}, replaceFlow(t), nil)
	env.Replacer = rp
	res := flow.Run(context.Background(), runInput(t, root, src), env)
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("result = %+v, want smaller", res)
	}
	if !rp.called {
		t.Fatal("the swap was not asked for")
	}
	if rp.got.Original != src || rp.got.Final != src || rp.got.JobID != 42 || rp.got.KeepDays != 7 {
		t.Errorf("swap plan = %+v, want the original, its own name, job 42, 7 days", rp.got)
	}
	if len(sf.staged) != 1 || rp.got.Staged != sf.staged[0].Path {
		t.Errorf("swap staged = %q, want the staged file", rp.got.Staged)
	}
	if sf.placedTo != "" {
		t.Errorf("the place step ran (%q); a replace must not place", sf.placedTo)
	}
}

// TestRunReplaceRefusalsKeepTheOriginal: a trash folder, a changed original and a shared one keep the job (may pass or
// skip), and a failed put-back is fatal and says the original is in the trash.
func TestRunReplaceRefusalsKeepTheOriginal(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want flow.Outcome
		why  string
	}{
		{"no trash folder", safety.ErrNoTrash, flow.OutcomeMayPass, "replace.not_possible"},
		{"read-only library", safety.ErrReadOnlyLibrary, flow.OutcomeMayPass, "replace.not_possible"},
		{"original changed", safety.ErrSourceChanged, flow.OutcomeMayPass, "replace.source_changed"},
		{"hard-linked original", safety.ErrSharedSource, flow.OutcomeSkip, "replace.shared"},
		{"put-back failed", safety.ErrPutBackFailed, flow.OutcomeFatal, "replace.put_back_failed"},
		{"stranger in the trash", safety.ErrStrangerInTrash, flow.OutcomeFatal, "replace.stranger_in_trash"},
		{"swap done, record not settled", safety.ErrRecordNotSettled, flow.OutcomeFatal, "replace.record_unsettled"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, src, info := libraryFile(t, "clip.mkv", "h264")
			m := &fake.Media{Probes: map[string]media.Info{src: info}}
			sf := &fakeSafety{verify: safety.Verdict{OK: true}}
			env := newEnv(m, sf, &recorder{}, replaceFlow(t), nil)
			env.Replacer = &fakeReplacer{err: c.err}
			res := flow.Run(context.Background(), runInput(t, root, src), env)
			if res.Outcome != c.want || res.Reason != c.why {
				t.Fatalf("result = %+v, want %s %s", res, c.want, c.why)
			}
			// The page shows NextAction as the action and a retry is accepted only for a code it knows: a human
			// sentence there gives no action (review #865 add-on).
			if c.want != flow.OutcomeSkip && res.NextAction != "retry" {
				t.Errorf("next action = %q, want the code retry", res.NextAction)
			}
			if _, err := os.Stat(src); err != nil {
				t.Errorf("the original is gone: %v", err)
			}
		})
	}
}

// TestRunReplaceWithoutAJournalKeepsTheOriginal: a worker without the replace journal does not replace.
func TestRunReplaceWithoutAJournalKeepsTheOriginal(t *testing.T) {
	root, src, info := libraryFile(t, "clip.mkv", "h264")
	m := &fake.Media{Probes: map[string]media.Info{src: info}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	res := flow.Run(context.Background(), runInput(t, root, src), newEnv(m, sf, &recorder{}, replaceFlow(t), nil))
	if res.Outcome != flow.OutcomeFatal || res.Reason != "replace.unavailable" {
		t.Fatalf("result = %+v, want fatal replace.unavailable", res)
	}
}

// TestRunReplaceWithSidecarsUsesTheOriginalName: a replace with subtitle files takes the original's own name with the
// new extension, and the subtitle files follow that name. The free-name rename does not apply to a replace (review
// #865, the "clip (2).mkv" bug).
func TestRunReplaceWithSidecarsUsesTheOriginalName(t *testing.T) {
	root, src, info, rev := srtRun(t)
	rev = setReplace(t, rev)
	m := &srtWriting{Media: &fake.Media{Probes: map[string]media.Info{src: info}}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	rp := &fakeReplacer{}
	journal := &fakeJournal{}
	env := newEnv(m, sf, &recorder{}, rev, nil)
	env.Replacer = rp
	env.Sidecars = journal
	res := flow.Run(context.Background(), runInput(t, root, src), env)
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("result = %+v, want smaller", res)
	}
	if rp.got.Final != src {
		t.Errorf("swap final = %q, want the original's own name %q", rp.got.Final, src)
	}
	for _, name := range []string{"clip.eng.srt", "clip.eng.2.srt"} {
		if b, err := os.ReadFile(filepath.Join(root, name)); err != nil || len(b) == 0 {
			t.Errorf("%s: %v, want the subtitle file beside the replaced name", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "clip (2).mkv")); err == nil {
		t.Errorf("a renamed copy clip (2).mkv was made")
	}
}
