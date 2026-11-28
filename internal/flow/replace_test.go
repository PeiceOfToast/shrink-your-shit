// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
		next string // the next-action sentence the page shows, or retry where a retry is the step
	}{
		{"no trash folder", safety.ErrNoTrash, flow.OutcomeMayPass, "replace.not_possible", "Make the library folder writable so a trash folder can be made there, or save the new file next to the original instead."},
		{"read-only library", safety.ErrReadOnlyLibrary, flow.OutcomeMayPass, "replace.not_possible", "Make the library folder writable so a trash folder can be made there, or save the new file next to the original instead."},
		{"original changed", safety.ErrSourceChanged, flow.OutcomeMayPass, "replace.source_changed", "Try it again once nothing else is changing the file."},
		{"hard-linked original", safety.ErrSharedSource, flow.OutcomeSkip, "replace.shared", ""},
		{"put-back failed", safety.ErrPutBackFailed, flow.OutcomeFatal, "replace.put_back_failed",
			"Check the library's trash folder and put the original back by hand."},
		{"stranger in the trash", safety.ErrStrangerInTrash, flow.OutcomeFatal, "replace.stranger_in_trash",
			"Check the library's trash folder."},
		{"swap done, record not settled", safety.ErrRecordNotSettled, flow.OutcomeFatal, "replace.record_unsettled",
			"The next start settles the record; check the file names."},
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
			// The page shows NextAction as the action: each failure carries the sentence it needs (the lead's correction).
			if c.want != flow.OutcomeSkip && res.NextAction != c.next {
				t.Errorf("next action = %q, want %q", res.NextAction, c.next)
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

// TestRunReplaceRecordUnsettledKeepsTheSubtitles (B3): the swap happened but its record did not settle. The new file is
// in place, so its subtitle files stay with it, as a settled swap keeps them (review #865).
func TestRunReplaceRecordUnsettledKeepsTheSubtitles(t *testing.T) {
	root, src, info, rev := srtRun(t)
	rev = setReplace(t, rev)
	m := &srtWriting{Media: &fake.Media{Probes: map[string]media.Info{src: info}}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	rp := &fakeReplacer{err: safety.ErrRecordNotSettled}
	journal := &fakeJournal{}
	env := newEnv(m, sf, &recorder{}, rev, nil)
	env.Replacer = rp
	env.Sidecars = journal
	res := flow.Run(context.Background(), runInput(t, root, src), env)
	if res.Outcome != flow.OutcomeFatal || res.Reason != "replace.record_unsettled" {
		t.Fatalf("result = %+v, want fatal replace.record_unsettled", res)
	}
	for _, name := range []string{"clip.eng.srt", "clip.eng.2.srt"} {
		if b, err := os.ReadFile(filepath.Join(root, name)); err != nil || len(b) == 0 {
			t.Errorf("%s: %v, want the subtitle file kept with the new file", name, err)
		}
	}
	if len(journal.dropped) != 0 {
		t.Errorf("dropped = %v, want the subtitle records kept", journal.dropped)
	}
	if len(journal.finished) != 1 || journal.finished[0] != 42 {
		t.Errorf("finished = %v, want the job finished", journal.finished)
	}
}

// TestRunReplaceTrashesAnEarlierSubtitleOfOurs: a subtitle file of ours that the replace would write over is moved to
// the trash, the new subtitles take its name, and its record is dropped (the .srt rule of S2-10b).
func TestRunReplaceTrashesAnEarlierSubtitleOfOurs(t *testing.T) {
	root, src, info, rev := srtRun(t)
	rev = setReplace(t, rev)
	earlier := filepath.Join(root, "clip.eng.srt")
	if err := os.WriteFile(earlier, []byte("EDITED-BY-HAND"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("EARLIER"))
	journal := &fakeJournal{recs: []safety.Sidecar{{JobID: 7, Path: earlier, Bytes: 7, Hash: hex.EncodeToString(sum[:])}}}
	m := &srtWriting{Media: &fake.Media{Probes: map[string]media.Info{src: info}}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	env := newEnv(m, sf, &recorder{}, rev, nil)
	env.Replacer = &fakeReplacer{}
	env.Sidecars = journal
	res := flow.Run(context.Background(), runInput(t, root, src), env)
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("result = %+v, want smaller", res)
	}
	if b, err := os.ReadFile(filepath.Join(root, ".syp-trash", "clip.eng.7.srt")); err != nil || string(b) != "EDITED-BY-HAND" {
		t.Errorf("the earlier subtitle in the trash = %q (%v), want it kept there", b, err)
	}
	if b, err := os.ReadFile(earlier); err != nil || string(b) == "EDITED-BY-HAND" {
		t.Errorf("clip.eng.srt = %q (%v), want the new subtitles at its name", b, err)
	}
	found := false
	for _, p := range journal.dropped {
		if p == earlier {
			found = true
		}
	}
	if !found {
		t.Errorf("dropped = %v, want the earlier record dropped", journal.dropped)
	}
}

// TestRunReplaceRefusesAForeignSubtitle: a subtitle file with no record of ours is never moved or replaced; the run
// refuses with save.srt_exists, as before.
func TestRunReplaceRefusesAForeignSubtitle(t *testing.T) {
	root, src, info, rev := srtRun(t)
	rev = setReplace(t, rev)
	foreign := filepath.Join(root, "clip.eng.srt")
	if err := os.WriteFile(foreign, []byte("SOMEONE ELSE"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &srtWriting{Media: &fake.Media{Probes: map[string]media.Info{src: info}}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	env := newEnv(m, sf, &recorder{}, rev, nil)
	env.Replacer = &fakeReplacer{}
	// A record of ours for a different file (clip.eng.2.srt) does not make clip.eng.srt ours (review #869).
	journal := &fakeJournal{recs: []safety.Sidecar{{JobID: 9, Path: filepath.Join(root, "clip.eng.2.srt"), Bytes: 1}}}
	env.Sidecars = journal
	res := flow.Run(context.Background(), runInput(t, root, src), env)
	if res.Outcome != flow.OutcomeFatal || res.Reason != "save.srt_exists" {
		t.Fatalf("result = %+v, want the taken-name refusal", res)
	}
	if b, _ := os.ReadFile(foreign); string(b) != "SOMEONE ELSE" {
		t.Errorf("the foreign subtitle = %q, want it untouched", b)
	}
	// Refused before anything is recorded: a foreign name must not enter the records at all (review #869).
	if len(journal.added) != 0 {
		t.Errorf("added = %+v, want nothing recorded for a foreign name", journal.added)
	}
}

// TestRunRefusedSwapPutsTheEarlierSubtitleBack: a replace trashes an earlier subtitle of ours, then the swap refuses.
// The subtitle goes back to its name with its record, the trash no longer holds it, and the message is a plain sentence
// (the review's "put it back on a refused swap").
func TestRunRefusedSwapPutsTheEarlierSubtitleBack(t *testing.T) {
	root, src, info, rev := srtRun(t)
	rev = setReplace(t, rev)
	earlier := filepath.Join(root, "clip.eng.srt")
	if err := os.WriteFile(earlier, []byte("EDITED-BY-HAND"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("EARLIER"))
	rec := safety.Sidecar{JobID: 7, Path: earlier, Bytes: 7, Hash: hex.EncodeToString(sum[:])}
	journal := &fakeJournal{recs: []safety.Sidecar{rec}}
	m := &srtWriting{Media: &fake.Media{Probes: map[string]media.Info{src: info}}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	env := newEnv(m, sf, &recorder{}, rev, nil)
	env.Replacer = &fakeReplacer{err: safety.ErrSwapFailed}
	env.Sidecars = journal
	res := flow.Run(context.Background(), runInput(t, root, src), env)
	if res.Outcome != flow.OutcomeFatal || res.Reason != "replace.failed" {
		t.Fatalf("result = %+v, want fatal replace.failed", res)
	}
	if strings.Contains(res.Message, "safety:") || !strings.Contains(res.Message, "put back") {
		t.Errorf("message = %q, want the plain sentence", res.Message)
	}
	if b, err := os.ReadFile(earlier); err != nil || string(b) != "EDITED-BY-HAND" {
		t.Errorf("clip.eng.srt = %q (%v), want the earlier subtitle back at its name", b, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".syp-trash", "clip.eng.7.srt")); !os.IsNotExist(err) {
		t.Errorf("the earlier subtitle is still in the trash: %v", err)
	}
	restored := false
	for _, r := range journal.added {
		if r.Path == earlier && r.JobID == 7 {
			restored = true
		}
	}
	if !restored {
		t.Errorf("added = %+v, want the earlier record back", journal.added)
	}
}

// stopJournal is a journal whose Sidecars call number "at" stops the run: the stop lands between the earlier subtitle's
// move to the trash and the placing of the new subtitles.
type stopJournal struct {
	*fakeJournal
	calls  int
	at     int
	cancel func()
}

func (s *stopJournal) Sidecars() ([]safety.Sidecar, error) {
	s.calls++
	if s.calls == s.at {
		s.cancel()
	}
	return s.fakeJournal.Sidecars()
}

// movingReplacer places the new file, then reports a stop: the swap happened and the run was stopped after it.
type movingReplacer struct{ cancel func() }

func (m movingReplacer) Swap(ctx context.Context, p safety.SwapPlan) (safety.SwapResult, error) {
	if err := os.Rename(p.Staged, p.Final); err != nil {
		return safety.SwapResult{}, err
	}
	m.cancel()
	return safety.SwapResult{}, context.Canceled
}

// earlierSubtitleRun is a replace over an earlier subtitle of ours that was edited by hand since we wrote it.
func earlierSubtitleRun(t *testing.T) (root, src string, info media.Info, rev flow.Flow, earlier string, rec safety.Sidecar) {
	t.Helper()
	root, src, info, rev = srtRun(t)
	rev = setReplace(t, rev)
	earlier = filepath.Join(root, "clip.eng.srt")
	if err := os.WriteFile(earlier, []byte("EDITED-BY-HAND"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("EARLIER"))
	rec = safety.Sidecar{JobID: 7, Path: earlier, Bytes: 7, Hash: hex.EncodeToString(sum[:])}
	return root, src, info, rev, earlier, rec
}

// TestRunStopBeforePlacementRestoresTheEarlierSubtitle (review #869): a stop after the earlier subtitle moved to the
// trash and before anything is placed puts it back with its record.
func TestRunStopBeforePlacementRestoresTheEarlierSubtitle(t *testing.T) {
	root, src, info, rev, earlier, rec := earlierSubtitleRun(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	journal := &stopJournal{fakeJournal: &fakeJournal{recs: []safety.Sidecar{rec}}, at: 2, cancel: cancel}
	m := &srtWriting{Media: &fake.Media{Probes: map[string]media.Info{src: info}}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	env := newEnv(m, sf, &recorder{}, rev, nil)
	env.Replacer = &fakeReplacer{}
	env.Sidecars = journal
	res := flow.Run(ctx, runInput(t, root, src), env)
	if res.Outcome != flow.OutcomeCancelled {
		t.Fatalf("result = %+v, want cancelled", res)
	}
	if b, err := os.ReadFile(earlier); err != nil || string(b) != "EDITED-BY-HAND" {
		t.Errorf("clip.eng.srt = %q (%v), want the earlier subtitle back at its name", b, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".syp-trash", "clip.eng.7.srt")); !os.IsNotExist(err) {
		t.Errorf("the earlier subtitle is still in the trash: %v", err)
	}
	restored := false
	for _, r := range journal.added {
		if r.Path == earlier && r.JobID == 7 {
			restored = true
		}
	}
	if !restored {
		t.Errorf("added = %+v, want the earlier record back", journal.added)
	}
}

// TestRunStopAfterPlacementKeepsTheNewSubtitles (review #869): the swap placed the new file, then the run was stopped:
// the new subtitles stay with it.
func TestRunStopAfterPlacementKeepsTheNewSubtitles(t *testing.T) {
	root, src, info, rev, earlier, rec := earlierSubtitleRun(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	journal := &stopJournal{fakeJournal: &fakeJournal{recs: []safety.Sidecar{rec}}, at: 0, cancel: cancel}
	m := &srtWriting{Media: &fake.Media{Probes: map[string]media.Info{src: info}}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	env := newEnv(m, sf, &recorder{}, rev, nil)
	env.Replacer = movingReplacer{cancel: cancel}
	env.Sidecars = journal
	res := flow.Run(ctx, runInput(t, root, src), env)
	if res.Outcome != flow.OutcomeCancelled {
		t.Fatalf("result = %+v, want cancelled", res)
	}
	if b, err := os.ReadFile(earlier); err != nil || string(b) == "EDITED-BY-HAND" || len(b) == 0 {
		t.Errorf("clip.eng.srt = %q (%v), want the new subtitles kept with the new file", b, err)
	}
}

// TestRunNoReplacerRestoresTheEarlierSubtitle (review #869, P3): a worker without the replace journal keeps the
// original and puts the earlier subtitle back.
func TestRunNoReplacerRestoresTheEarlierSubtitle(t *testing.T) {
	root, src, info, rev, earlier, rec := earlierSubtitleRun(t)
	journal := &fakeJournal{recs: []safety.Sidecar{rec}}
	m := &srtWriting{Media: &fake.Media{Probes: map[string]media.Info{src: info}}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	env := newEnv(m, sf, &recorder{}, rev, nil)
	env.Sidecars = journal
	res := flow.Run(context.Background(), runInput(t, root, src), env)
	if res.Outcome != flow.OutcomeFatal || res.Reason != "replace.unavailable" {
		t.Fatalf("result = %+v, want fatal replace.unavailable", res)
	}
	if b, err := os.ReadFile(earlier); err != nil || string(b) != "EDITED-BY-HAND" {
		t.Errorf("clip.eng.srt = %q (%v), want the earlier subtitle back at its name", b, err)
	}
}
