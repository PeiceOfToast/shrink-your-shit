// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

var opts = Options{Version: "test-1.0", TrashPath: ".syp-trash"}

func openDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	return db
}

// body is a PATCH body from a JSON text.
func body(t *testing.T, text string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func refusalOf(t *testing.T, err error) *Refusal {
	t.Helper()
	r, ok := AsRefusal(err)
	if !ok {
		t.Fatalf("error = %v, want a refusal", err)
	}
	return r
}

// With nothing saved, the document is page 11's defaults.
func TestDefaultsWhenNothingIsSaved(t *testing.T) {
	db := openDB(t)
	d, err := Load(context.Background(), db, opts)
	if err != nil {
		t.Fatal(err)
	}
	if d.Processing.KeepOriginalsDays != 7 || d.Processing.ParallelJobs != 1 || d.Processing.QueueOrder != "newest" ||
		d.Processing.LowSpaceGB != 10 || d.BackupKeep != 10 || d.Logs.JobDays != 30 || d.Logs.SystemDays != 14 ||
		d.Logs.RawDays != 7 || d.General.Version != "test-1.0" || d.Processing.TrashPath != ".syp-trash" {
		t.Errorf("defaults = %+v", d)
	}
}

// A saved change is what the next read returns, and each changed field is one change.
func TestPatchSavesAndReturnsTheDocument(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	d, changes, err := Patch(ctx, db, opts, body(t, `{"general":{"serverName":"Den"},"processing":{"keepOriginalsDays":30}}`))
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if d.General.ServerName != "Den" || d.Processing.KeepOriginalsDays != 30 {
		t.Errorf("the patched document = %+v", d)
	}
	if len(changes) != 2 {
		t.Errorf("changes = %d, want 2", len(changes))
	}
	again, err := Load(ctx, db, opts)
	if err != nil || again.Processing.KeepOriginalsDays != 30 || again.General.ServerName != "Den" {
		t.Errorf("the next read = %+v, %v", again, err)
	}
	// The same values again change nothing.
	_, changes, err = Patch(ctx, db, opts, body(t, `{"processing":{"keepOriginalsDays":30}}`))
	if err != nil || len(changes) != 0 {
		t.Errorf("a same-value patch: changes = %d, %v; want none", len(changes), err)
	}
}

// All or nothing: one refused field saves nothing, not even the valid ones in the same patch.
func TestOneBadFieldSavesNothing(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	_, _, err := Patch(ctx, db, opts, body(t, `{"processing":{"parallelJobs":3,"keepOriginalsDays":400}}`))
	r := refusalOf(t, err)
	if r.Code != "invalid_setting" || r.Field != "processing.keepOriginalsDays" {
		t.Fatalf("refusal = %+v, want invalid_setting on keepOriginalsDays", r)
	}
	d, _ := Load(ctx, db, opts)
	if d.Processing.ParallelJobs != 1 {
		t.Errorf("parallelJobs = %d after a refused patch, want the default 1 (nothing saved)", d.Processing.ParallelJobs)
	}
}

// Each range and choice of page 11 refuses with its field and its sentence.
func TestRangesAndChoicesRefuse(t *testing.T) {
	cases := []struct {
		name, patch, field, message string
	}{
		{"parallel jobs low", `{"processing":{"parallelJobs":0}}`, "processing.parallelJobs", "Pick from 1 to 8 files at once."},
		{"parallel jobs high", `{"processing":{"parallelJobs":9}}`, "processing.parallelJobs", "Pick from 1 to 8 files at once."},
		{"keep days high", `{"processing":{"keepOriginalsDays":366}}`, "processing.keepOriginalsDays", "Keep originals from 0 to 365 days."},
		{"keep days low", `{"processing":{"keepOriginalsDays":-1}}`, "processing.keepOriginalsDays", "Keep originals from 0 to 365 days."},
		{"low space", `{"processing":{"lowSpaceGB":0}}`, "processing.lowSpaceGB", "Pick a limit from 1 to 1000 GB."},
		{"scan time", `{"processing":{"scanSchedule":"25:00"}}`, "processing.scanSchedule", "Use a time like 02:00, or leave it empty for no daily scan."},
		{"queue order", `{"processing":{"queueOrder":"random"}}`, "processing.queueOrder", "Pick newest, oldest, largest or smallest."},
		{"language code", `{"general":{"languages":["english"]}}`, "general.languages", "Use three-letter codes like eng, ger, fra."},
		{"time format", `{"general":{"timeFormat":"25h"}}`, "general.timeFormat", "Pick 24-hour or 12-hour time."},
		{"date format", `{"general":{"dateFormat":"x"}}`, "general.dateFormat", "Pick a date format from the list."},
		{"accent", `{"appearance":{"accent":"blue"}}`, "appearance.accent", "Use a colour like #3b82f6."},
		{"theme", `{"appearance":{"theme":"neon"}}`, "appearance.theme", "Pick Match system, Dark or Light."},
		{"job log days", `{"logs":{"jobDays":5}}`, "logs.jobDays", "Pick 7, 30, 90 days or forever."},
		{"system log days", `{"logs":{"systemDays":3}}`, "logs.systemDays", "Pick a number of days from the list."},
		{"raw days choice", `{"logs":{"rawDays":60}}`, "logs.rawDays", "Pick 7, 30, 90 days or forever."},
		{"raw longer than job", `{"logs":{"jobDays":30,"rawDays":90}}`, "logs.rawDays", "Raw tool output can't be kept longer than the job log it belongs to."},
		{"backups low", `{"backupKeep":0}`, "backupKeep", "Keep from 1 to 50 backups."},
		{"backups high", `{"backupKeep":51}`, "backupKeep", "Keep from 1 to 50 backups."},
		{"wrong kind", `{"processing":{"parallelJobs":"two"}}`, "processing.parallelJobs", "Use a value of the right kind for processing.parallelJobs."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := openDB(t)
			_, _, err := Patch(context.Background(), db, opts, body(t, c.patch))
			r := refusalOf(t, err)
			if r.Code != "invalid_setting" && r.Code != "unknown_setting" {
				t.Errorf("code = %s", r.Code)
			}
			if r.Field != c.field || r.Message != c.message {
				t.Errorf("refusal = %q %q, want %q %q", r.Field, r.Message, c.field, c.message)
			}
		})
	}
}

// read_only names a read-only field; unknown_setting names one that does not exist.
func TestReadOnlyAndUnknownNames(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	for _, text := range []string{`{"general":{"version":"9"}}`, `{"processing":{"trashPath":"/x"}}`, `{"general":{"updateAvailable":{}}}`} {
		_, _, err := Patch(ctx, db, opts, body(t, text))
		if r := refusalOf(t, err); r.Code != "read_only" {
			t.Errorf("%s: code = %s, want read_only", text, r.Code)
		}
	}
	for _, text := range []string{`{"processing":{"nosuch":1}}`, `{"weather":{"x":1}}`} {
		_, _, err := Patch(ctx, db, opts, body(t, text))
		if r := refusalOf(t, err); r.Code != "unknown_setting" {
			t.Errorf("%s: code = %s, want unknown_setting", text, r.Code)
		}
	}
}

// Rows that hold a value the document no longer reads keep the default, not an error.
func TestAStoredValueThatDoesNotParseKeepsTheDefault(t *testing.T) {
	db := openDB(t)
	if err := db.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO app_settings (key, value) VALUES ('processing.keepOriginalsDays', '"seven"')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	d, err := Load(context.Background(), db, opts)
	if err != nil || d.Processing.KeepOriginalsDays != 7 {
		t.Errorf("keep = %d, %v; want the default 7", d.Processing.KeepOriginalsDays, err)
	}
}
