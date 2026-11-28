// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/settings"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// floorStore is a migrated store with the flow store on it.
func floorStore(t *testing.T) (*store.DB, *Store) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db, Open(db)
}

// replaceFlow is the guide's flow with the original replaced.
func replaceFlow(t *testing.T) Flow {
	t.Helper()
	a := DefaultAnswers()
	a["original"] = "replace"
	f, problems := FromAnswers(a, DefaultAnswers())
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	return f
}

func setKeep(t *testing.T, db *store.DB, days int) {
	t.Helper()
	body := map[string]json.RawMessage{"processing": json.RawMessage(`{"keepOriginalsDays":` + itoa(days) + `}`)}
	if _, _, err := settings.Patch(context.Background(), db, settings.Options{}, body); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// blockedOn asserts err is a publish refusal on the save step's where field with the K17 sentence.
func blockedOn(t *testing.T, err error, what string) {
	t.Helper()
	var blocked *PublishBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("%s: err = %v, want a publish refusal", what, err)
	}
	p := blocked.Problem
	if p.StepID != "save" || p.Field != "where" || p.Message != "Keep originals for at least 7 days in Settings before replacing them." {
		t.Errorf("%s: problem = %+v", what, p)
	}
}

// Publish path 1: a replace publish is refused while the keep is below the floor. Red-first: without the check it
// publishes.
func TestPublishRefusesReplaceBelowTheFloor(t *testing.T) {
	db, fs := floorStore(t)
	ctx := context.Background()
	id, _, err := fs.Create(ctx, replaceFlow(t), "test")
	if err != nil {
		t.Fatal(err)
	}
	setKeep(t, db, 0)
	f := replaceFlow(t)
	_, err = fs.Publish(ctx, id, &f, ValidateContext{})
	blockedOn(t, err, "publish")
}

// Publish path 2: a starter (the guide's saved answers at setup) that replaces is refused the same way.
func TestStarterRefusesReplaceBelowTheFloor(t *testing.T) {
	db, fs := floorStore(t)
	ctx := context.Background()
	setKeep(t, db, 0)
	a := DefaultAnswers()
	a["original"] = "replace"
	err := db.Tx(ctx, func(tx *sql.Tx) error {
		_, _, err := fs.CreateFromStarterTx(ctx, tx, "shrink-videos", "Shrink", a)
		return err
	})
	blockedOn(t, err, "starter")
}

// Publish path 3: restoring a revision that replaces is refused while the keep is below the floor.
func TestRestoreRefusesReplaceBelowTheFloor(t *testing.T) {
	db, fs := floorStore(t)
	ctx := context.Background()
	f := replaceFlow(t)
	id, _, err := fs.Create(ctx, f, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Publish(ctx, id, &f, ValidateContext{}); err != nil {
		t.Fatalf("publishing with the default keep: %v", err)
	}
	setKeep(t, db, 0)
	_, err = fs.Restore(ctx, id, 1)
	blockedOn(t, err, "restore")
}

// K17 rule 3: with no replace workflow, keeping 0 days still saves and publishes.
func TestNoReplaceWorkflowKeepsZeroDays(t *testing.T) {
	db, fs := floorStore(t)
	ctx := context.Background()
	setKeep(t, db, 0)
	f := Flow{}
	a := DefaultAnswers()
	a["original"] = "next_to_original"
	f, problems := FromAnswers(a, DefaultAnswers())
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	id, _, err := fs.Create(ctx, f, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Publish(ctx, id, &f, ValidateContext{}); err != nil {
		t.Errorf("publish with keep 0 and no replace: %v", err)
	}
}

// Rule 1's predicate: a live published replace is in use; a deleted one is not.
func TestReplaceInUseSeesOnlyLivePublishedReplaces(t *testing.T) {
	_, fs := floorStore(t)
	ctx := context.Background()
	f := replaceFlow(t)
	id, _, err := fs.Create(ctx, f, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Publish(ctx, id, &f, ValidateContext{}); err != nil {
		t.Fatal(err)
	}
	if in, err := fs.ReplaceInUse(ctx); err != nil || !in {
		t.Fatalf("in use = %v, %v; want true", in, err)
	}
	if err := fs.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if in, err := fs.ReplaceInUse(ctx); err != nil || in {
		t.Errorf("after delete, in use = %v, %v; want false", in, err)
	}
}
