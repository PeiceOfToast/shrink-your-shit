// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return cardsTemplate(), 0, nil }
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
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return cardsTemplate(), 0, nil }
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
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tm, 0, nil }
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
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tm, 0, nil }
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
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tm, 0, nil }
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
	w3.Template = func(jobs.Job) (rules.Template, int64, error) { return tm3, 0, nil }
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
	w2.Template = func(jobs.Job) (rules.Template, int64, error) { return tm2, 0, nil }
	told := false
	w2.OnDone = func(jobs.Job, string) { told = true }
	w2.RunOne(context.Background())
	if j := s2.job(); j.Status != jobs.Done || !told {
		t.Errorf("%s: global tell ran %v", j.Status, told)
	}
}

// FB2b: the lookup card runs before the conversion through what main wires in, and what it sets names the new
// file; the command card runs after the save with the saved file's path, and its output is published to the
// cards after it.
func TestLookupAndCommandCardsAreWired(t *testing.T) {
	s := newScenarioNamed(t, "Harrow.Lane.2019.Remux.mkv")
	tm := cardsTemplate()
	(*tm.Branches)[0].Before = append([]rules.Card{{NodeID: "look", Type: workflow.TypeLookupDownloader, Name: "Ask the downloader for details", Config: map[string]any{}}}, (*tm.Branches)[0].Before...)
	(*tm.Branches)[0].Before[1].Config["value"] = "{{title_clean}} {{year}}" // the variable the name template uses comes from the lookup
	(*tm.Branches)[0].After = []rules.Card{
		{NodeID: "cmd", Type: workflow.TypeCommand, Name: "Run a command", Config: map[string]any{"program": "after", "arguments": `"{{output_path}}"`}},
		{NodeID: "note", Type: workflow.TypeNote, Name: "Add a note to the log", Config: map[string]any{"text": "the program said {{command_output}}"}},
	}
	w := s.worker(Beside)
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tm, 0, nil }
	var ran []string
	w.Cards = func(jobs.Job, string) flow.Env {
		return flow.Env{
			Lookup: func(string) (map[string]string, string, error) {
				return map[string]string{"title_clean": "Harrow Lane", "year": "2019", "season": "", "episode": "", "original_language": "English"}, "Radarr: Harrow Lane (2019)", nil
			},
			Exec: func(program string, args []string, dir string, timeout time.Duration, onLine func(string)) (string, error) {
				ran = append(ran, program+" "+strings.Join(args, " "))
				return "ok from the program\n", nil
			},
		}
	}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	j := s.job()
	if j.Status != jobs.Done || !strings.HasSuffix(j.OutputPath, "Harrow.Lane.2019.Remux.Harrow Lane 2019.mkv") {
		t.Fatalf("%s %q %q", j.Status, j.Error, j.OutputPath)
	}
	if len(ran) != 1 || ran[0] != "after "+j.OutputPath {
		t.Errorf("command: %v", ran)
	}
	if !noteOnTimeline(s, "Radarr: Harrow Lane (2019)") || !noteOnTimeline(s, "Note: “the program said ok from the program”") || !noteOnTimeline(s, "Ran after (exit 0)") {
		steps, _ := s.store.Steps(j.ID)
		t.Errorf("timeline: %+v", steps)
	}
	// the switch off: no Exec, the card is a warning, the job stays done
	s2 := newScenarioNamed(t, "Harrow.Lane.2019.Remux.mkv")
	w2 := s2.worker(Beside)
	w2.Template = func(jobs.Job) (rules.Template, int64, error) { return tm, 0, nil }
	w2.Cards = func(jobs.Job, string) flow.Env { // the downloader answers, but no Exec: the switch is off
		return flow.Env{Lookup: func(string) (map[string]string, string, error) {
			return map[string]string{"title_clean": "Harrow Lane", "year": "2019"}, "Radarr: Harrow Lane (2019)", nil
		}}
	}
	w2.RunOne(context.Background())
	j2 := s2.job()
	steps, _ := s2.store.Steps(j2.ID)
	warned := false
	for _, st := range steps {
		if st.Step == "after" && !st.OK && strings.Contains(st.Words, "Settings > Safety") {
			warned = true
		}
	}
	if j2.Status != jobs.Done || !warned {
		t.Errorf("switch off: %s, warned %v: %+v", j2.Status, warned, steps)
	}
}

// taggingStore is the scenario's store plus the tags a store offers once jobs carry them (FB2c): the worker
// reaches them through the JobTagger interface, so the card works as soon as the column exists.
type taggingStore struct {
	*jobs.Store
	tags map[int64][]string
}

func (s *taggingStore) JobTags(id int64) ([]string, error) { return s.tags[id], nil }
func (s *taggingStore) SetJobTags(id int64, add, remove []string) ([]string, error) {
	s.tags[id] = rules.ApplyTags(s.tags[id], add, remove)
	return s.tags[id], nil
}

// FB2c: the record's tags route the file (Has a tag), a Tag the file card before the conversion changes them and
// publishes {{tags}} for the name, and one after the save changes them again; a store without tags leaves the
// card a failure before the conversion (nothing was encoded) and a warning after.
func TestTagCardsReadAndWriteTheRecord(t *testing.T) {
	s := newScenarioNamed(t, "Harrow.Lane.2019.Remux.mkv")
	store := &taggingStore{Store: s.store, tags: map[int64][]string{s.id: {"needs review"}}}
	tm := cardsTemplate()
	q := rules.Question{Kind: "tag", Tags: "needs review"}
	q.Name = "Needs review?"
	(*tm.Branches)[0].Checks = []rules.BranchCheck{{NodeID: "q", Port: "match", Check: q}}
	(*tm.Branches)[1].Checks = []rules.BranchCheck{{NodeID: "q", Port: "else", Check: q}}
	(*tm.Branches)[0].Before = []rules.Card{
		{NodeID: "tag", Type: workflow.TypeTag, Name: "Tag the file", Config: map[string]any{"add": "converting", "remove": "needs review"}},
		{NodeID: "set", Type: workflow.TypeSetVariable, Name: "Set a variable", Config: map[string]any{"name": "profile", "value": "{{tags}}"}},
	}
	(*tm.Branches)[0].After = []rules.Card{{NodeID: "tag2", Type: workflow.TypeTag, Name: "Tag the file", Config: map[string]any{"add": "converted", "remove": "converting"}}}
	w := s.worker(Beside)
	w.Queue = store
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tm, 0, nil }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	j := s.job()
	if j.Status != jobs.Done || !strings.HasSuffix(j.OutputPath, "Harrow.Lane.2019.Remux.converting.mkv") {
		t.Fatalf("%s %q %q", j.Status, j.Error, j.OutputPath)
	}
	if got := strings.Join(store.tags[s.id], ","); got != "converted" {
		t.Errorf("the record's tags after: %q", got)
	}
	if !noteOnTimeline(s, "Tags: “converting”") || !noteOnTimeline(s, "Tags: “converted”") {
		steps, _ := s.store.Steps(j.ID)
		t.Errorf("timeline: %+v", steps)
	}
	// the same workflow on a store without tags: the question answers No (no tags), the file stops as failed
	s2 := newScenarioNamed(t, "Harrow.Lane.2019.Remux.mkv")
	w2 := s2.worker(Beside)
	w2.Template = func(jobs.Job) (rules.Template, int64, error) { return tm, 0, nil }
	w2.RunOne(context.Background())
	if j := s2.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, "Not a remux") {
		t.Errorf("no tags in the store: %s %q", j.Status, j.Error)
	}
	// the real store carries tags (migration 57, #545): a Tag card on the plain store changes the record itself,
	// and the file is converted (review2-542: the compile-time check in worker.go keeps the two together)
	s4 := newScenarioNamed(t, "Harrow.Lane.2019.Remux.mkv")
	tm4 := cardsTemplate()
	(*tm4.Branches)[0].Before = append([]rules.Card{{NodeID: "tag", Type: workflow.TypeTag, Name: "Tag the file", Config: map[string]any{"add": "converting, {{title}}"}}}, (*tm4.Branches)[0].Before...)
	w4 := s4.worker(Beside)
	w4.Template = func(jobs.Job) (rules.Template, int64, error) { return tm4, 0, nil }
	if _, err := w4.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := s4.job(); j.Status != jobs.Done {
		t.Errorf("a Tag card on the real store: %s %q", j.Status, j.Error)
	}
	if tags, err := s4.store.JobTags(s4.id); err != nil || !rules.HasTag(tags, "converting") || !rules.HasTag(tags, "Harrow.Lane.2019.Remux") {
		t.Errorf("the record's tags after the card: %v %v", tags, err)
	}
	if !noteOnTimeline(s4, "Tags: “converting, Harrow.Lane.2019.Remux”") {
		steps, _ := s4.store.Steps(s4.id)
		t.Errorf("timeline: %+v", steps)
	}
	// a queue without tags (not this store) still makes the card say so and go on: the flow test covers the card;
	// here the wrapper hides the store's tags
	s6 := newScenarioNamed(t, "Harrow.Lane.2019.Remux.mkv")
	w6 := s6.worker(Beside)
	w6.Queue = &noTagsQueue{Store: s6.store}
	w6.Template = func(jobs.Job) (rules.Template, int64, error) { return tm4, 0, nil }
	if _, err := w6.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := s6.job(); j.Status != jobs.Done || !noteOnTimeline(s6, "Tags not kept") {
		steps, _ := s6.store.Steps(s6.id)
		t.Errorf("a queue without tags: %s %q %+v", j.Status, j.Error, steps)
	}
}

// noTagsQueue is the store with its tag methods hidden, as a queue from before migration 57 would look to the
// worker: the JobTagger assertion fails on it, so the cards take their no-store path.
type noTagsQueue struct{ *jobs.Store }

func (q *noTagsQueue) JobTags(int64, ...struct{}) {}

// FB4: "Convert again later" puts the job back in the queue for the card's wait, counting the try, and fails it
// with the card's words once the tries are used up; a Wait until a time of day puts it back until then, no try
// counted as failed. Nothing is encoded either way and the original is untouched.
func TestConvertAgainLaterAndWaitUntil(t *testing.T) {
	s := newScenarioNamed(t, "Harrow.Lane.2019.WEB.mkv") // not a remux: the No path
	tm := cardsTemplate()
	(*tm.Branches)[1] = rules.Branch{Checks: (*tm.Branches)[1].Checks, End: "The download never finished", Fail: true, Path: []string{"in", "q", "again"},
		Requeue: &rules.Requeue{AfterSec: 1800, MaxTries: 2}}
	w := s.worker(Beside)
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tm, 0, nil }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	j := s.job()
	if j.Status != jobs.Queued || j.Attempts != 1 || j.RetryAt == "" || !strings.Contains(j.Error, "try 1 of 2") {
		t.Fatalf("first try: %s attempts %d at %q %q", j.Status, j.Attempts, j.RetryAt, j.Error)
	}
	if s.count("-c:v") != 0 {
		t.Error("nothing should have been encoded")
	}
	s.assertOriginalSafe()
	// the last try fails with the card's words (a card that allows one try: its first try is its last)
	s3 := newScenarioNamed(t, "Harrow.Lane.2019.WEB.mkv")
	tm3 := cardsTemplate()
	(*tm3.Branches)[1] = rules.Branch{Checks: (*tm3.Branches)[1].Checks, End: "The download never finished", Fail: true, Path: []string{"in", "q", "again"},
		Requeue: &rules.Requeue{AfterSec: 1800, MaxTries: 1}}
	w3 := s3.worker(Beside)
	w3.Template = func(jobs.Job) (rules.Template, int64, error) { return tm3, 0, nil }
	w3.RunOne(context.Background())
	if j3 := s3.job(); j3.Status != jobs.Failed || !strings.Contains(j3.Error, "The download never finished. Tried 1 times") {
		t.Errorf("last try: %s %q", j3.Status, j3.Error)
	}
	// Wait until a time of day: back in the queue until then
	s2 := newScenarioNamed(t, "Harrow.Lane.2019.Remux.mkv")
	tm2 := cardsTemplate()
	(*tm2.Branches)[0].Before = append([]rules.Card{{NodeID: "w", Type: workflow.TypeWait, Name: "Wait", Config: map[string]any{"until": "02:00"}}}, (*tm2.Branches)[0].Before...)
	w2 := s2.worker(Beside)
	w2.Template = func(jobs.Job) (rules.Template, int64, error) { return tm2, 0, nil }
	w2.RunOne(context.Background())
	j2 := s2.job()
	if j2.Status != jobs.Queued || j2.RetryAt == "" || !strings.Contains(j2.Error, "Waiting until 02:00") {
		t.Errorf("wait until: %s at %q %q", j2.Status, j2.RetryAt, j2.Error)
	}
	if s2.count("-c:v") != 0 {
		t.Error("nothing should have been encoded")
	}
	steps, _ := s2.store.Steps(j2.ID)
	if len(steps) == 0 || steps[len(steps)-1].Step != "later" || !strings.Contains(steps[len(steps)-1].Words, "Waiting until 02:00") {
		t.Errorf("timeline: %+v", steps)
	}
	// review2-548 P1: when the file comes back because that wait's time came, the same card lets it through and
	// the file is converted; it is not sent to tomorrow again. The queue's way of bringing it back is RetryLater
	// with the wait's words on the job, which the worker reads; here that return is made by hand, as the queue
	// would at 02:00.
	backAt := func(at time.Time) { // the queue brings the file back at that time, as RetryLater had asked
		if err := s2.store.RetryNow(j2.ID); err != nil {
			t.Fatal(err)
		}
		if j, err := s2.store.Claim("x"); err != nil || j.ID != j2.ID {
			t.Fatalf("claim: %+v %v", j, err)
		}
		if err := s2.store.RetryLater(j2.ID, at, jobs.ReasonRetryLater, flow.WaitPrefix+"02:00 as the workflow asks"); err != nil {
			t.Fatal(err)
		}
	}
	// back at another time (as Convert again later would bring it): the Wait still holds it (review-548 item 5)
	y0 := time.Now().AddDate(0, 0, -1)
	backAt(time.Date(y0.Year(), y0.Month(), y0.Day(), 1, 30, 0, 0, time.Local)) // a fixed clock, never 02:00 by accident (review2-548 P3b)
	w2.RunOne(context.Background())
	if j := s2.job(); j.Status != jobs.Queued || !strings.HasPrefix(j.Error, flow.WaitPrefix) {
		t.Errorf("back at another time: %s %q", j.Status, j.Error)
	}
	// back at its own time: through it goes and the file is converted
	y := time.Now().AddDate(0, 0, -1)
	backAt(time.Date(y.Year(), y.Month(), y.Day(), 2, 0, 0, 0, time.Local))
	if _, err := w2.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := s2.job(); j.Status != jobs.Done {
		t.Errorf("after the wait's time: %s %q at %q", j.Status, j.Error, j.RetryAt)
	}
	if !noteOnTimeline(s2, "came back as asked") {
		steps, _ := s2.store.Steps(j2.ID)
		t.Errorf("timeline after the wait: %+v", steps)
	}
	// a person's Retry now on a waiting file: wanted now, converted (review-548 item 6)
	s5 := newScenarioNamed(t, "Harrow.Lane.2019.Remux.mkv")
	w5 := s5.worker(Beside)
	w5.Template = func(jobs.Job) (rules.Template, int64, error) { return tm2, 0, nil }
	w5.RunOne(context.Background())
	if j := s5.job(); j.Status != jobs.Queued {
		t.Fatalf("first run: %s", j.Status)
	}
	if err := s5.store.RetryNow(s5.id); err != nil {
		t.Fatal(err)
	}
	if _, err := w5.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := s5.job(); j.Status != jobs.Done || !noteOnTimeline(s5, "retried by hand") {
		steps, _ := s5.store.Steps(s5.id)
		t.Errorf("retried by hand: %s %q %+v", j.Status, j.Error, steps)
	}
}

// compareTemplate is cardsTemplate with "Compare the result" between the check and the save on the Yes path:
// the save on its Yes, the given end on its No.
func compareTemplate(q rules.Question, no rules.Card) rules.Template {
	tm := cardsTemplate()
	(*tm.Branches)[0].BeforeSave = []rules.Card{{NodeID: "cmp", Type: workflow.TypeCheckResult, Name: "Worth it?", Check: &q,
		Yes: []rules.Card{{NodeID: "save", Type: workflow.TypeCommit, Name: "Save the result"}}, No: []rules.Card{no}}}
	(*tm.Branches)[0].Path = []string{"in", "q", "set", "note", "conv", "verify", "cmp", "save"}
	return tm
}

// FB4b-3: a "Compare the result" before the save that answers No and leads to "Leave the file alone" keeps the
// original: the file ends skipped with its own reason and words (not failed, like a bigger file the Save step
// keeps), nothing is left beside the original, the answer is on the timeline; a Yes saves as before.
func TestCompareBeforeTheSaveKeepsTheOriginal(t *testing.T) {
	// the fake conversion writes 3 bytes for a 14-byte original: 78.6% saved
	q := rules.Question{Kind: "result", Test: "saved_at_least", Percent: 90}
	s := newScenarioNamed(t, "Harrow.Lane.2019.Remux.mkv")
	before, _ := os.ReadFile(s.src)
	w := s.worker(Beside)
	w.Template = func(jobs.Job) (rules.Template, int64, error) {
		return compareTemplate(q, rules.Card{NodeID: "leave", Type: workflow.TypeEnd, Name: "Leave the file alone", Config: map[string]any{"reason": "Not worth it"}}), 0, nil
	}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.Get(s.id); err == nil {
		t.Fatal("the job record is still there; want it gone (skipped)")
	}
	rec, _ := s.store.GetFile(s.src)
	if rec == nil || rec.Reason != rules.SkipResultNotWanted || !strings.Contains(rec.Decision, "Not worth it: the new file was compared with the original (No (78.6% saved, asked for at least 90%)), so the original was kept") {
		t.Errorf("skipped: %+v", rec)
	}
	if !noteOnTimeline(s, "Worth it? → No (78.6% saved, asked for at least 90%)") {
		steps, _ := s.store.Steps(s.id)
		t.Errorf("timeline: %+v", steps)
	}
	after, _ := os.ReadFile(s.src)
	if !bytes.Equal(before, after) {
		t.Error("the original changed")
	}
	entries, _ := os.ReadDir(s.dir)
	if len(entries) != 1 {
		t.Errorf("left in the folder: %d entries, want only the original", len(entries))
	}
	s.assertOriginalSafe()
	// Yes: saved as before, the answer on the timeline
	q.Percent = 50
	s2 := newScenarioNamed(t, "Harrow.Lane.2019.Remux.mkv")
	w2 := s2.worker(Beside)
	w2.Template = func(jobs.Job) (rules.Template, int64, error) {
		return compareTemplate(q, rules.Card{NodeID: "leave", Type: workflow.TypeEnd, Name: "Leave the file alone"}), 0, nil
	}
	if _, err := w2.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := s2.job(); j.Status != jobs.Done || !noteOnTimeline(s2, "Worth it? → Yes (78.6% saved, asked for at least 50%)") {
		t.Errorf("yes: %s %q", j.Status, j.Error)
	}
}

// FB4b-3: the running-time test reads the probes' lengths (a second off on a 100-second file passes the check's
// own 2% bound), and "Stop as failed" on its No fails the job with the end's words, the original untouched.
func TestCompareBeforeTheSaveRunningTimeAndStop(t *testing.T) {
	q := rules.Question{Kind: "result", Test: "runtime_within", Seconds: 0.5}
	s := newScenarioNamed(t, "Harrow.Lane.2019.Remux.mkv")
	s.outSpec.duration = 101
	before, _ := os.Stat(s.src)
	w := s.worker(Beside)
	w.Template = func(jobs.Job) (rules.Template, int64, error) {
		return compareTemplate(q, rules.Card{NodeID: "stop", Type: workflow.TypeFail, Name: "Stop as failed", Config: map[string]any{"reason": "The length changed"}}), 0, nil
	}
	w.RunOne(context.Background())
	j := s.job()
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "The length changed (No (running time 0:01:41 against the original's 0:01:40, 1 second off, asked for within 0.5 seconds))") || !strings.Contains(j.Error, "original is untouched") {
		t.Errorf("%s %q", j.Status, j.Error)
	}
	after, _ := os.Stat(s.src)
	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Error("the original changed")
	}
	s.assertOriginalSafe()
	// within 2 seconds: saved
	q.Seconds = 2
	s2 := newScenarioNamed(t, "Harrow.Lane.2019.Remux.mkv")
	s2.outSpec.duration = 101
	w2 := s2.worker(Beside)
	w2.Template = func(jobs.Job) (rules.Template, int64, error) {
		return compareTemplate(q, rules.Card{NodeID: "stop", Type: workflow.TypeFail, Name: "Stop as failed"}), 0, nil
	}
	if _, err := w2.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := s2.job(); j.Status != jobs.Done || !noteOnTimeline(s2, "Worth it? → Yes (running time 0:01:41 against the original's 0:01:40, 1 second off, asked for within 2 seconds)") {
		t.Errorf("within: %s %q", j.Status, j.Error)
	}
}
