// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/flow"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// cardsTemplate: Library files → "Is it a remux?" (the name contains remux) → Yes: Set a variable
// {{profile}}, Add a note, then the conversion whose new file is named after {{profile}}; No: Stop as failed.
func cardsTemplate() rules.Template {
	s := rules.DefaultSelectors(3)
	s.Ignore = []string{}
	s.AfterConverting, s.OutputName = "beside", "{{title}}.{{profile}}" // the name template applies when the new file sits beside the original
	then := rules.Template{Kind: rules.KindSelectors, Selectors: &s, CompilerVersion: rules.CompilerVersion}
	q := rules.Question{Kind: "name", Pattern: "remux", Match: "contains"}
	q.Name = "Is it a remux?"
	before := []rules.Card{
		{NodeID: "set", Type: workflow.TypeSetVariable, Name: "Set a variable", Config: map[string]any{"name": "profile", "value": "remux"}},
		{NodeID: "note", Type: workflow.TypeNote, Name: "Add a note to the log", Config: map[string]any{"text": "Converting {{title}} as {{profile}}"}},
	}
	// the No path has a note of its own before it stops, so the log says why (pre-review finding 2)
	why := []rules.Card{{NodeID: "why", Type: workflow.TypeNote, Name: "Say why", Config: map[string]any{"text": "{{title}} is not a remux"}}}
	return rules.Template{Kind: rules.KindBranches, CompilerVersion: rules.CompilerVersion, Branches: &[]rules.Branch{
		{Checks: []rules.BranchCheck{{NodeID: "q", Port: "match", Check: q}}, Before: before, Then: &then, Path: []string{"in", "q", "set", "note", "conv", "verify", "save"}},
		{Checks: []rules.BranchCheck{{NodeID: "q", Port: "else", Check: q}}, Before: why, End: "Not a remux: nothing to gain", Fail: true, Path: []string{"in", "q", "why", "fail"}},
	}}
}

// noteOnTimeline reports whether a card's words are on the job's timeline.
func noteOnTimeline(s *scenario, words string) bool {
	steps, _ := s.store.Steps(s.id)
	for _, st := range steps {
		if st.Step == "branch" && strings.Contains(st.Words, words) {
			return true
		}
	}
	return false
}

// FB1c: a path's cards run before the conversion: the question is answered from the file's name, the
// variable a card sets names the new file, the note is in the job log, and each card is on the timeline.
func TestCardsRunBeforeTheConversion(t *testing.T) {
	s := newScenarioNamed(t, "Harrow.Lane.2019.Remux.mkv")
	w := s.worker(Beside)
	w.Template = func(jobs.Job) (rules.Template, error) { return cardsTemplate(), nil }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	j := s.job()
	if j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	if !strings.HasSuffix(j.OutputPath, "Harrow.Lane.2019.Remux.remux.mkv") {
		t.Errorf("the new file's name should use the variable the card set: %q", j.OutputPath)
	}
	steps, _ := s.store.Steps(j.ID)
	var words []string
	for _, st := range steps {
		if st.Step == "branch" {
			words = append(words, st.NodeID+": "+st.Words)
		}
	}
	joined := strings.Join(words, "\n")
	for _, want := range []string{"set: Set a variable → Set {{profile}} = “remux”", "note: Add a note to the log → Note: “Converting Harrow.Lane.2019.Remux as remux”"} {
		if !strings.Contains(joined, want) {
			t.Errorf("timeline lacks %q:\n%s", want, joined)
		}
	}
	lines, _ := s.store.LogLines(j.ID)
	found := false
	for _, l := range lines {
		if strings.Contains(l.Text, "Converting Harrow.Lane.2019.Remux as remux") {
			found = true
		}
	}
	if !found {
		t.Error("the note is not in the job log")
	}
}

// FB1c: the "Stop as failed" end records the job as failed with the card's words (its capital kept), the cards
// before it have run (the note says why), nothing is encoded, and the original's bytes and date are untouched.
func TestStopAsFailedEndsTheRun(t *testing.T) {
	s := newScenarioNamed(t, "Harrow.Lane.2019.WEB.mkv")
	before, _ := os.Stat(s.src)
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, error) { return cardsTemplate(), nil }
	w.RunOne(context.Background())
	j := s.job()
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "Not a remux: nothing to gain") || !strings.Contains(j.Error, "original is untouched") {
		t.Errorf("%s %q", j.Status, j.Error)
	}
	if s.count("-c:v") != 0 {
		t.Error("nothing should have been encoded")
	}
	if !noteOnTimeline(s, "Harrow.Lane.2019.WEB is not a remux") {
		t.Error("the note before Stop as failed did not run")
	}
	after, _ := os.Stat(s.src)
	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("the original changed: %d/%v → %d/%v", before.Size(), before.ModTime(), after.Size(), after.ModTime())
	}
	s.assertOriginalSafe()
}

// FB1c: "Leave the file alone" keeps recording a skipped file (not a failure), and the cards before it run too.
func TestLeaveAloneStaysSkipped(t *testing.T) {
	s := newScenarioNamed(t, "Harrow.Lane.2019.WEB.mkv")
	tm := cardsTemplate()
	(*tm.Branches)[1].Fail, (*tm.Branches)[1].End = false, "Not a remux: left for later."
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, error) { return tm, nil }
	w.RunOne(context.Background())
	rec, _ := s.store.GetFile(s.src)
	if rec == nil || rec.Reason != rules.SkipLeftByWorkflow || !strings.Contains(rec.Decision, "not a remux: left for later") {
		t.Errorf("skipped: %+v", rec)
	}
	if _, err := s.store.Get(s.id); err == nil { // a skipped file has no job row left, so it can't be a failed job
		t.Error("a left-alone file is not a job that failed")
	}
	if !noteOnTimeline(s, "Harrow.Lane.2019.WEB is not a remux") {
		t.Error("the note before Leave the file alone did not run")
	}
	s.assertOriginalSafe()
}

// FB1c: a card that doesn't work stops the run before anything is encoded, naming the card.
func TestCardFailureStopsTheRun(t *testing.T) {
	s := newScenarioNamed(t, "Harrow.Lane.2019.Remux.mkv")
	tm := cardsTemplate()
	(*tm.Branches)[0].Before = append((*tm.Branches)[0].Before, rules.Card{NodeID: "later", Type: "action.later", Name: "A later card"})
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, error) { return tm, nil }
	w.RunOne(context.Background())
	j := s.job()
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "A later card didn't work") {
		t.Errorf("%s %q", j.Status, j.Error)
	}
	if s.count("-c:v") != 0 {
		t.Error("nothing should have been encoded")
	}
	s.assertOriginalSafe()
}

// FB2a: the after-save cards run once the result is in place: the checksum of the new file is published,
// the sidecar beside it carries it, the notification goes through what main wires in, a card that doesn't
// work is a warning on the timeline while the job stays done, and a tell card replaces the global tell.
func TestAfterCardsRunOnTheSavedFile(t *testing.T) {
	s := newScenarioNamed(t, "Harrow.Lane.2019.Remux.mkv")
	tm := cardsTemplate()
	(*tm.Branches)[0].After = []rules.Card{
		{NodeID: "sum", Type: workflow.TypeChecksum, Name: "Checksum the result", Config: map[string]any{"kind": "md5"}},
		{NodeID: "side", Type: workflow.TypeSidecar, Name: "Write a file beside the result", Config: map[string]any{"name": "{{title}}.{{profile}}.md5", "text": "{{checksum}} {{saved_percent}}"}},
		{NodeID: "say", Type: workflow.TypeNotify, Name: "Send a notification", Config: map[string]any{"text": "{{title}} saved {{saved_percent}}%"}},
		{NodeID: "ms", Type: workflow.TypeMediaServer, Name: "Tell the media server", Config: map[string]any{}},
	}
	w := s.worker(Beside)
	w.Template = func(jobs.Job) (rules.Template, error) { return tm, nil }
	globalTold, sent := false, ""
	w.OnDone = func(jobs.Job, string) { globalTold = true }
	w.Cards = func(jobs.Job, string) flow.Env {
		return flow.Env{Notify: func(target, kind, text string) (string, error) { sent = text; return "Sent to phone", nil }}
	}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	j := s.job()
	if j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(j.OutputPath), "Harrow.Lane.2019.Remux.remux.md5"))
	if err != nil {
		t.Fatal(err)
	}
	sum, _, _ := strings.Cut(string(b), " ")
	if len(sum) != 32 {
		t.Errorf("sidecar: %q", b)
	}
	if !strings.HasPrefix(sent, "Harrow.Lane.2019.Remux saved ") || strings.Contains(sent, "{{") {
		t.Errorf("notification: %q", sent)
	}
	if globalTold {
		t.Error("the path has a tell card, so the global tell must not run as well")
	}
	if !noteOnTimeline(s, "Checksum the result → MD5 "+sum) || !noteOnTimeline(s, "Send a notification → Sent to phone") {
		steps, _ := s.store.Steps(j.ID)
		t.Errorf("timeline: %+v", steps)
	}
	steps, _ := s.store.Steps(j.ID)
	warned := false
	for _, st := range steps {
		if st.Step == "after" && !st.OK && strings.Contains(st.Words, "Tell the media server didn't work") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("the media server card should be a warning step: %+v", steps)
	}

	// a media-server card alone: the downloaders are still told the global way, the media servers are not (per kind)
	s3 := newScenarioNamed(t, "Harrow.Lane.2019.Remux.mkv")
	tm3 := cardsTemplate()
	(*tm3.Branches)[0].After = []rules.Card{{NodeID: "ms", Type: workflow.TypeMediaServer, Name: "Tell the media server", Config: map[string]any{}}}
	w3 := s3.worker(Beside)
	w3.Template = func(jobs.Job) (rules.Template, error) { return tm3, nil }
	var told3 []string
	w3.OnDone = func(jobs.Job, string) { told3 = append(told3, "global") }
	w3.Cards = func(jobs.Job, string) flow.Env {
		return flow.Env{
			TellMediaServer: func(which string) (string, error) { told3 = append(told3, "media:"+which); return "Told Plex", nil },
			TellDownloader: func(which string) (string, error) {
				told3 = append(told3, "downloader:"+which)
				return "Told Sonarr", nil
			},
		}
	}
	w3.RunOne(context.Background())
	if j := s3.job(); j.Status != jobs.Done || strings.Join(told3, " ") != "downloader:all media:all" {
		t.Errorf("per-kind tell: %s %v", j.Status, told3)
	}
	// without a tell card the global tell runs as it always has
	s2 := newScenarioNamed(t, "Harrow.Lane.2019.Remux.mkv")
	tm2 := cardsTemplate()
	(*tm2.Branches)[0].After = []rules.Card{{NodeID: "sum", Type: workflow.TypeChecksum, Name: "Checksum the result", Config: map[string]any{}}}
	w2 := s2.worker(Beside)
	w2.Template = func(jobs.Job) (rules.Template, error) { return tm2, nil }
	told := false
	w2.OnDone = func(jobs.Job, string) { told = true }
	w2.RunOne(context.Background())
	if j := s2.job(); j.Status != jobs.Done || !told {
		t.Errorf("%s: global tell ran %v", j.Status, told)
	}
}
