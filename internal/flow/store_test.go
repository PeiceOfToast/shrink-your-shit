// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow/steps"
	_ "github.com/PeiceOfToast/shrink-your-shit/internal/flow/steps"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// workflowStore is a migrated store with the workflow tables open on it.
func workflowStore(t *testing.T) (*flow.Store, context.Context) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return flow.Open(db), context.Background()
}

// flowWithQuality is the starter flow under a name, with the video step's quality changed.
func flowWithQuality(t *testing.T, name string, quality int) flow.Flow {
	t.Helper()
	s, ok := flow.StarterByID("shrink-videos")
	if !ok {
		t.Fatal("no shrink-videos starter")
	}
	f := s.Flow
	f.Name = name
	settings, err := json.Marshal(map[string]any{
		"format": "hevc", "quality": quality, "useGpu": false, "ifGpuFails": "cpu",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.Steps[0].Settings = settings
	return f
}

// settingsOf is one step's saved settings bytes, for an exact comparison.
func settingsOf(f flow.Flow, id string) flow.StepRef {
	for _, ref := range f.Steps {
		if ref.ID == id {
			return ref
		}
	}
	return flow.StepRef{}
}

// qualityOf reads the video step's quality out of a flow.
func qualityOf(t *testing.T, f flow.Flow) int {
	t.Helper()
	var set steps.VideoSettings
	if err := json.Unmarshal(f.Steps[0].Settings, &set); err != nil {
		t.Fatalf("read the video settings: %v", err)
	}
	return set.Quality
}

// Create saves a draft and publishes nothing: the editor's new workflow is not a revision until
// Publish is called.
func TestCreateSavesADraftOnly(t *testing.T) {
	s, ctx := workflowStore(t)
	f := flowWithQuality(t, "Holidays", 4)
	id, rev, err := s.Create(ctx, f, "editor")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rev != 1 {
		t.Fatalf("create returned rev %d, want the draft's 1", rev)
	}
	w, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if w.Name != "Holidays" || w.MadeWith != "editor" || w.CurrentRev != 0 {
		t.Fatalf("workflow = %+v; want Holidays by the editor, nothing published", w)
	}
	info, err := s.Draft(ctx, id)
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if info.Rev != 1 || !reflect.DeepEqual(info.Flow, f) {
		t.Fatalf("draft = rev %d, %+v; want the created flow at rev 1", info.Rev, info.Flow)
	}
	revs, err := s.Revisions(ctx, id)
	if err != nil {
		t.Fatalf("revisions: %v", err)
	}
	if len(revs) != 0 {
		t.Fatalf("revisions = %+v; want none published", revs)
	}
	items, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].ID != id || !items[0].DraftAhead || !reflect.DeepEqual(items[0].Flow, f) {
		t.Fatalf("list = %+v; want the one draft-only row", items)
	}
}

// A save from a page that read an older draft is refused with the rev to reload, and the newer
// draft is left alone.
func TestSaveDraftRefusesAStaleBaseRev(t *testing.T) {
	s, ctx := workflowStore(t)
	id, _, err := s.Create(ctx, flowWithQuality(t, "Holidays", 3), "editor")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rev, err := s.SaveDraft(ctx, id, flowWithQuality(t, "Holidays", 4), 1)
	if err != nil || rev != 2 {
		t.Fatalf("save = rev %d, %v; want 2", rev, err)
	}
	_, err = s.SaveDraft(ctx, id, flowWithQuality(t, "Holidays", 5), 1)
	var changed *flow.ChangedSinceError
	if !errors.As(err, &changed) {
		t.Fatalf("stale save = %v; want a ChangedSinceError", err)
	}
	if changed.Rev != 2 {
		t.Fatalf("changed since rev %d; want the draft's 2", changed.Rev)
	}
	info, err := s.Draft(ctx, id)
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if info.Rev != 2 || qualityOf(t, info.Flow) != 4 {
		t.Fatalf("draft = rev %d quality %d; want the first save kept", info.Rev, qualityOf(t, info.Flow))
	}
	if rev, err := s.SaveDraft(ctx, id, flowWithQuality(t, "Holidays", 5), 2); err != nil || rev != 3 {
		t.Fatalf("current save = rev %d, %v; want 3", rev, err)
	}
}

// A flow the check refuses is not published, and nothing is written: the editor fixes it first.
func TestPublishIsBlockedByTheFirstProblem(t *testing.T) {
	s, ctx := workflowStore(t)
	f := flowWithQuality(t, "Holidays", 3)
	// Two passes is a "not yet" problem: a valid choice the engine cannot act on.
	settings, err := json.Marshal(map[string]any{
		"format": "hevc", "quality": 3, "useGpu": false, "ifGpuFails": "cpu", "twoPass": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.Steps[0].Settings = settings
	id, _, err := s.Create(ctx, f, "editor")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = s.Publish(ctx, id, nil, flow.ValidateContext{Env: flow.Env{}})
	var blocked *flow.PublishBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("publish = %v; want a PublishBlockedError", err)
	}
	if !strings.Contains(blocked.Problem.Message, "Two passes") {
		t.Fatalf("blocked by %q; want the two-passes problem", blocked.Problem.Message)
	}
	w, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if w.CurrentRev != 0 {
		t.Fatalf("current rev = %d; want nothing published", w.CurrentRev)
	}
	if revs, err := s.Revisions(ctx, id); err != nil || len(revs) != 0 {
		t.Fatalf("revisions = %+v, %v; want none", revs, err)
	}
}

// Publish makes revision N+1 and never rewrites an older one: a job that ran rev 1 keeps it.
func TestPublishMakesTheNextRevisionAndLeavesTheOldOne(t *testing.T) {
	s, ctx := workflowStore(t)
	id, _, err := s.Create(ctx, flowWithQuality(t, "Holidays", 3), "editor")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rev, err := s.Publish(ctx, id, nil, flow.ValidateContext{Env: flow.Env{}})
	if err != nil || rev != 1 {
		t.Fatalf("publish = rev %d, %v; want 1", rev, err)
	}
	if _, err := s.SaveDraft(ctx, id, flowWithQuality(t, "Holidays", 5), 2); err != nil {
		t.Fatalf("save: %v", err)
	}
	rev, err = s.Publish(ctx, id, nil, flow.ValidateContext{Env: flow.Env{}})
	if err != nil || rev != 2 {
		t.Fatalf("second publish = rev %d, %v; want 2", rev, err)
	}
	first, err := s.Revision(ctx, id, 1)
	if err != nil {
		t.Fatalf("revision 1: %v", err)
	}
	if q := qualityOf(t, first.Flow); q != 3 {
		t.Fatalf("revision 1 quality %d; want the first flow's 3", q)
	}
	second, err := s.Revision(ctx, id, 2)
	if err != nil {
		t.Fatalf("revision 2: %v", err)
	}
	if q := qualityOf(t, second.Flow); q != 5 {
		t.Fatalf("revision 2 quality %d; want the draft's 5", q)
	}
	w, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if w.CurrentRev != 2 {
		t.Fatalf("current rev = %d, want 2", w.CurrentRev)
	}
	// A publish clears the draft: the editor now shows the published flow.
	info, err := s.Draft(ctx, id)
	if err != nil {
		t.Fatalf("draft after publish: %v", err)
	}
	if !reflect.DeepEqual(info.Flow, second.Flow) {
		t.Fatalf("draft after publish = %+v; want revision 2", info.Flow)
	}
	if _, err := s.Publish(ctx, id, nil, flow.ValidateContext{Env: flow.Env{}}); !errors.Is(err, flow.ErrNothingToPublish) {
		t.Fatalf("publish with no draft = %v; want ErrNothingToPublish", err)
	}
}

// Restore writes a new revision from an old one's stored flow; the old revision is unchanged.
func TestRestoreCopiesAnOldRevisionIntoANewOne(t *testing.T) {
	s, ctx := workflowStore(t)
	id, _, err := s.Create(ctx, flowWithQuality(t, "Holidays", 3), "editor")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Publish(ctx, id, nil, flow.ValidateContext{Env: flow.Env{}}); err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	if _, err := s.SaveDraft(ctx, id, flowWithQuality(t, "Holidays", 5), 2); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := s.Publish(ctx, id, nil, flow.ValidateContext{Env: flow.Env{}}); err != nil {
		t.Fatalf("publish 2: %v", err)
	}
	old, err := s.Revision(ctx, id, 1)
	if err != nil {
		t.Fatalf("revision 1: %v", err)
	}
	rev, err := s.Restore(ctx, id, 1)
	if err != nil || rev != 3 {
		t.Fatalf("restore = rev %d, %v; want 3", rev, err)
	}
	restored, err := s.Revision(ctx, id, 3)
	if err != nil {
		t.Fatalf("revision 3: %v", err)
	}
	if !reflect.DeepEqual(restored.Flow, old.Flow) {
		t.Fatalf("restored flow = %+v; want revision 1's %+v", restored.Flow, old.Flow)
	}
	still, err := s.Revision(ctx, id, 1)
	if err != nil {
		t.Fatalf("revision 1 after restore: %v", err)
	}
	if string(settingsOf(still.Flow, "video").Settings) != string(settingsOf(old.Flow, "video").Settings) {
		t.Fatal("restore rewrote revision 1")
	}
	w, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if w.CurrentRev != 3 {
		t.Fatalf("current rev = %d, want the restored 3", w.CurrentRev)
	}
	if _, err := s.Restore(ctx, id, 9); !errors.Is(err, flow.ErrRevisionNotFound) {
		t.Fatalf("restore of a missing revision = %v; want ErrRevisionNotFound", err)
	}
}

// Duplicate copies the published flow, not a draft ahead of it, under "<name> copy" as a draft.
func TestDuplicateCopiesThePublishedFlow(t *testing.T) {
	s, ctx := workflowStore(t)
	id, _, err := s.Create(ctx, flowWithQuality(t, "Holidays", 3), "editor")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Publish(ctx, id, nil, flow.ValidateContext{Env: flow.Env{}}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := s.SaveDraft(ctx, id, flowWithQuality(t, "Holidays", 5), 2); err != nil {
		t.Fatalf("save: %v", err)
	}
	copied, err := s.Duplicate(ctx, id)
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	if copied.Name != "Holidays copy" || copied.CurrentRev != 0 || copied.ID == id {
		t.Fatalf("copy = %+v; want a new draft named Holidays copy", copied)
	}
	info, err := s.Draft(ctx, copied.ID)
	if err != nil {
		t.Fatalf("copy's draft: %v", err)
	}
	if q := qualityOf(t, info.Flow); q != 3 {
		t.Fatalf("copy quality %d; want the published 3, not the draft's 5", q)
	}
}

// Delete hides the workflow and its revisions from every read; deleting it again is not found.
func TestDeleteHidesTheWorkflow(t *testing.T) {
	s, ctx := workflowStore(t)
	id, _, err := s.Create(ctx, flowWithQuality(t, "Holidays", 3), "editor")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Delete(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(ctx, id); !errors.Is(err, flow.ErrNotFound) {
		t.Fatalf("get after delete = %v; want ErrNotFound", err)
	}
	if _, err := s.Draft(ctx, id); !errors.Is(err, flow.ErrNotFound) {
		t.Fatalf("draft after delete = %v; want ErrNotFound", err)
	}
	if _, err := s.Revisions(ctx, id); !errors.Is(err, flow.ErrNotFound) {
		t.Fatalf("revisions after delete = %v; want ErrNotFound", err)
	}
	items, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("list = %+v; want the deleted workflow gone", items)
	}
	if err := s.Delete(ctx, id); !errors.Is(err, flow.ErrNotFound) {
		t.Fatalf("second delete = %v; want ErrNotFound", err)
	}
}

// A workflow setup made from a starter has no draft and is published at rev 1; its draft rev is
// 0, so the editor's first save sends baseRev 0.
func TestSetupMadeWorkflowStillWorks(t *testing.T) {
	s, ctx := workflowStore(t)
	id, rev, err := s.CreateFromStarter(ctx, "shrink-videos", "Shrink")
	if err != nil || rev != 1 {
		t.Fatalf("setup workflow = rev %d, %v; want 1", rev, err)
	}
	w, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if w.CurrentRev != 1 || w.MadeWith != "starter" {
		t.Fatalf("workflow = %+v; want the starter's published rev 1", w)
	}
	info, err := s.Draft(ctx, id)
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if info.Rev != 0 {
		t.Fatalf("draft rev = %d; want 0 for a workflow setup made", info.Rev)
	}
	items, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].DraftAhead {
		t.Fatalf("list = %+v; want one row with no draft ahead", items)
	}
	if rev, err := s.SaveDraft(ctx, id, flowWithQuality(t, "Shrink", 4), 0); err != nil || rev != 1 {
		t.Fatalf("first save = rev %d, %v; want 1", rev, err)
	}
	if rev, err := s.Publish(ctx, id, nil, flow.ValidateContext{Env: flow.Env{}}); err != nil || rev != 2 {
		t.Fatalf("publish = rev %d, %v; want 2", rev, err)
	}
	first, err := s.Revision(ctx, id, 1)
	if err != nil {
		t.Fatalf("revision 1: %v", err)
	}
	if rep := flow.Validate(first.Flow, flow.ValidateContext{Env: flow.Env{}}); !rep.OK() {
		t.Fatalf("revision 1 no longer checks: %+v", rep.Problems)
	}
}
