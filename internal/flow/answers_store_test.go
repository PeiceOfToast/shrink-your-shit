// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
)

// TestSetupMadeStarterWorkflowFitsTheGuide: a workflow setup made from a guided starter carries
// its answers, so both the editor and the list report it as one the guide can edit (ui-shop 19:28Z).
func TestSetupMadeStarterWorkflowFitsTheGuide(t *testing.T) {
	s, ctx := workflowStore(t)
	id, _, err := s.CreateFromStarter(ctx, "shrink-videos", "Shrink")
	if err != nil {
		t.Fatalf("create the setup workflow: %v", err)
	}
	info, err := s.Draft(ctx, id)
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if info.Answers == nil {
		t.Fatal("the setup-made workflow has no answers; want it to fit the guide")
	}
	rebuilt, problems := flow.FromAnswers(info.Answers, flow.DefaultAnswers())
	if len(problems) > 0 {
		t.Fatalf("FromAnswers on the stored answers = %+v", problems)
	}
	if _, ok := flow.ToAnswers(rebuilt); !ok {
		t.Fatal("the stored answers do not rebuild a flow the guide can say")
	}
	items, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Answers == nil {
		t.Fatalf("list = %+v; want the one row to carry the answers", items)
	}
}

// TestSavingADraftKeepsOrClearsTheAnswers: every save re-reads the answers from the flow it saved.
// A flow the guide can say keeps the answers; an edit it cannot say clears them; saving it back
// brings them back. Publishing keeps the published flow's answers.
func TestSavingADraftKeepsOrClearsTheAnswers(t *testing.T) {
	s, ctx := workflowStore(t)
	id, _, err := s.CreateFromStarter(ctx, "shrink-videos", "Shrink")
	if err != nil {
		t.Fatalf("create the setup workflow: %v", err)
	}
	guided := flowWithQuality(t, "Shrink", 4)
	if _, err := s.SaveDraft(ctx, id, guided, 0); err != nil {
		t.Fatalf("save a guided draft: %v", err)
	}
	info, err := s.Draft(ctx, id)
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if info.Answers == nil {
		t.Fatal("a flow the guide can say cleared its answers")
	}

	edited := guided
	edited.Steps = append([]flow.StepRef(nil), guided.Steps...)
	edited.Steps[0].Settings = mergeJSON(t, guided.Steps[0].Settings, `{"rateControl":"vbr"}`)
	if _, err := s.SaveDraft(ctx, id, edited, info.Rev); err != nil {
		t.Fatalf("save the edited draft: %v", err)
	}
	info, err = s.Draft(ctx, id)
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if info.Answers != nil {
		t.Fatalf("answers = %#v; want none for a flow the guide cannot say", info.Answers)
	}

	if _, err := s.SaveDraft(ctx, id, guided, info.Rev); err != nil {
		t.Fatalf("save the guided draft again: %v", err)
	}
	if info, err = s.Draft(ctx, id); err != nil || info.Answers == nil {
		t.Fatalf("draft after saving it back = %+v, %v; want the answers back", info, err)
	}
	rev, err := s.Publish(ctx, id, nil, flow.ValidateContext{})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if rev != 2 {
		t.Fatalf("published revision = %d, want 2", rev)
	}
	if info, err = s.Draft(ctx, id); err != nil || info.Answers == nil {
		t.Fatalf("draft after publish = %+v, %v; want the published flow's answers", info, err)
	}
}

// TestGuidedSaveWritesTheAnswersAndKeepsTheName: the guided page saves answers without the
// editor's baseRev; the draft becomes the flow they make, and the workflow keeps its own name and
// description, as the mock's putAnswers does.
func TestGuidedSaveWritesTheAnswersAndKeepsTheName(t *testing.T) {
	s, ctx := workflowStore(t)
	id, _, err := s.CreateFromStarter(ctx, "shrink-videos", "Shrink")
	if err != nil {
		t.Fatalf("create the setup workflow: %v", err)
	}
	answers := answersWith(map[string]any{
		"quality": 5, "useGpu": false, "audio": "as_is", "allLanguages": true,
		"subtitleLanguages": []string{},
	})
	f, problems := flow.FromAnswers(answers, flow.DefaultAnswers())
	if len(problems) > 0 {
		t.Fatalf("FromAnswers = %+v", problems)
	}
	if rev, err := s.SaveGuidedDraft(ctx, id, f); err != nil || rev != 1 {
		t.Fatalf("guided save = rev %d, %v; want 1", rev, err)
	}
	info, err := s.Draft(ctx, id)
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	want, err := json.Marshal(flow.NormalizeAnswers(answers, flow.DefaultAnswers()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(flow.NormalizeAnswers(info.Answers, flow.DefaultAnswers()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stored answers =\n %s\nwant\n %s", got, want)
	}
	w, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if w.Name != "Shrink" || w.Description == "" {
		t.Errorf("workflow = %q, %q; the guided save must keep its own name and description", w.Name, w.Description)
	}
	if rev, err := s.Publish(ctx, id, nil, flow.ValidateContext{}); err != nil || rev != 2 {
		t.Fatalf("publish = rev %d, %v; want 2", rev, err)
	}
	if info, err = s.Draft(ctx, id); err != nil || info.Answers == nil {
		t.Fatalf("draft after publish = %+v, %v; want the answers kept", info, err)
	}
}

// TestDuplicateCopiesTheAnswers: the copy fits the guide exactly when its source does.
func TestDuplicateCopiesTheAnswers(t *testing.T) {
	s, ctx := workflowStore(t)
	id, _, err := s.CreateFromStarter(ctx, "shrink-videos", "Shrink")
	if err != nil {
		t.Fatalf("create the setup workflow: %v", err)
	}
	copyRow, err := s.Duplicate(ctx, id)
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	info, err := s.Draft(ctx, copyRow.ID)
	if err != nil {
		t.Fatalf("copy's draft: %v", err)
	}
	if info.Answers == nil {
		t.Fatal("the copy lost the source's answers")
	}
	src, err := s.Draft(ctx, id)
	if err != nil {
		t.Fatalf("source's draft: %v", err)
	}
	if !reflect.DeepEqual(info.Answers, src.Answers) {
		t.Errorf("copy's answers = %#v, want the source's %#v", info.Answers, src.Answers)
	}
}

// TestRestoreDerivesTheAnswers: restoring an old guided revision makes it the current one, and the
// answers follow the restored flow rather than what the row said before.
func TestRestoreDerivesTheAnswers(t *testing.T) {
	s, ctx := workflowStore(t)
	id, _, err := s.CreateFromStarter(ctx, "shrink-videos", "Shrink")
	if err != nil {
		t.Fatalf("create the setup workflow: %v", err)
	}
	// Picking "Copy" in Audio's Advanced codec is a flow the guide has no answer for, and it
	// still validates: the engine copies the track either way.
	edited := flowWithQuality(t, "Shrink", 4)
	edited.Steps = append([]flow.StepRef(nil), edited.Steps...)
	edited.Steps[1].Settings = mergeJSON(t, edited.Steps[1].Settings, `{"codec":"copy"}`)
	if _, err := s.SaveDraft(ctx, id, edited, 0); err != nil {
		t.Fatalf("save the edited flow: %v", err)
	}
	if _, err := s.Publish(ctx, id, nil, flow.ValidateContext{}); err != nil {
		t.Fatalf("publish the edited flow: %v", err)
	}
	if info, err := s.Draft(ctx, id); err != nil || info.Answers != nil {
		t.Fatalf("draft after publishing the edited flow = %+v, %v; want no answers", info, err)
	}
	if rev, err := s.Restore(ctx, id, 1); err != nil || rev != 3 {
		t.Fatalf("restore = rev %d, %v; want 3", rev, err)
	}
	info, err := s.Draft(ctx, id)
	if err != nil {
		t.Fatalf("draft after restore: %v", err)
	}
	if info.Answers == nil {
		t.Fatal("restoring the starter revision did not bring the answers back")
	}
	var stored float64
	raw, err := json.Marshal(info.Answers["quality"])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &stored); err != nil || stored != 3 {
		t.Errorf("restored quality = %v, %v; want the starter's 3", stored, err)
	}
}
