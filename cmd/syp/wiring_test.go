// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/settings"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// openWiringStore is a migrated store with the starter workflow saved.
func openWiringStore(t *testing.T) (*store.DB, *flow.Store) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db, flow.Open(db)
}

// S1: a run's flow carries "my languages" as they are in the settings when the run starts. Red-first: without the
// lookup in loadRevisionFor the audio step keeps no languages and the test fails.
func TestRunLooksUpMyLanguagesWhenItStarts(t *testing.T) {
	ctx := context.Background()
	db, flows := openWiringStore(t)
	wf, _, err := flows.CreateFromStarter(ctx, "shrink-videos", "Shrink")
	if err != nil {
		t.Fatal(err)
	}
	info, err := flows.Draft(ctx, wf)
	if err != nil {
		t.Fatal(err)
	}
	mine := info.Flow
	for i := range mine.Steps {
		if mine.Steps[i].Type == "audio.tracks" {
			mine.Steps[i].Settings = json.RawMessage(`{"allLanguages":false,"languages":[]}`)
		}
	}
	rev, err := flows.Publish(ctx, wf, &mine, flow.ValidateContext{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := settings.Patch(ctx, db, settings.Options{}, map[string]json.RawMessage{
		"general": json.RawMessage(`{"languages":["fra","ita"]}`),
	}); err != nil {
		t.Fatal(err)
	}
	f, err := loadRevisionFor(flows, db)(ctx, wf, rev)
	if err != nil {
		t.Fatal(err)
	}
	var audio struct {
		Languages []string `json:"languages"`
	}
	for _, st := range f.Steps {
		if st.Type == "audio.tracks" {
			if err := json.Unmarshal(st.Settings, &audio); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(audio.Languages) != 2 || audio.Languages[0] != "fra" || audio.Languages[1] != "ita" {
		t.Errorf("the run's audio languages = %v, want the settings' [fra ita]", audio.Languages)
	}
}

// S1: the slots a new worker starts with are processing.parallelJobs, clamped to 1 to 8. Red-first: without the read in
// startSlots the value stays 1 and the test fails.
func TestWorkerStartsWithParallelJobs(t *testing.T) {
	ctx := context.Background()
	db, _ := openWiringStore(t)
	if got := startSlots(ctx, db); got != 1 {
		t.Errorf("unset slots = %d, want 1", got)
	}
	if _, _, err := settings.Patch(ctx, db, settings.Options{}, map[string]json.RawMessage{
		"processing": json.RawMessage(`{"parallelJobs":3}`),
	}); err != nil {
		t.Fatal(err)
	}
	if got := startSlots(ctx, db); got != 3 {
		t.Errorf("slots = %d, want the stored 3", got)
	}
}
