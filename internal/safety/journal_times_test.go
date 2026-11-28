// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

var utc = time.UTC

func day(d int, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, utc) }

// placeInTrash sets a swap's trashed_at, and left_at when left is non-nil, as the journal would have written them.
func placeInTrash(t *testing.T, f swapFixture, id int64, trashed time.Time, left *time.Time) {
	t.Helper()
	var l sql.NullInt64
	if left != nil {
		l = sql.NullInt64{Int64: left.Unix(), Valid: true}
	}
	if err := f.j.db.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE replace_journal SET trashed_at = ?, left_at = ? WHERE id = ?`, trashed.Unix(), l, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// savingOf is the row's saving, original minus new, clamped at 0: what the journal holds for the trash.
func savingOf(t *testing.T, f swapFixture, id int64) int64 {
	t.Helper()
	var orig, now int64
	if err := f.j.db.Read().QueryRow(`SELECT orig_size, new_size FROM replace_journal WHERE id = ?`, id).Scan(&orig, &now); err != nil {
		t.Fatal(err)
	}
	if orig-now < 0 {
		return 0
	}
	return orig - now
}

func bytesOn(t *testing.T, got []DaySaving, d string) int64 {
	t.Helper()
	for _, x := range got {
		if x.Day == d {
			return x.Bytes
		}
	}
	t.Fatalf("no day %s in %v", d, got)
	return 0
}

func inTrash(t *testing.T, f swapFixture, from, to time.Time) []DaySaving {
	t.Helper()
	got, err := f.j.InTrashSaving(context.Background(), from, to, utc)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A copy is counted on every day it is in the trash at the day's end.
func TestInTrashSavingCountsACopyOnEachDayItIsIn(t *testing.T) {
	f, id := doneSwap(t)
	placeInTrash(t, f, id, day(10, 10), nil)
	want := savingOf(t, f, id)
	got := inTrash(t, f, day(10, 0), day(12, 0))
	for _, d := range []string{"2026-11-10", "2026-11-11", "2026-11-12"} {
		if b := bytesOn(t, got, d); b != want {
			t.Errorf("%s = %d, want %d", d, b, want)
		}
	}
}

// In and out on the same day: the copy is not in the trash at that day's end.
func TestInTrashSavingInAndOutOnTheSameDayIsZeroAtTheEnd(t *testing.T) {
	f, id := doneSwap(t)
	placeInTrash(t, f, id, day(10, 9), nil)
	f.j.now = func() time.Time { return day(10, 17) }
	if err := f.j.SetState(context.Background(), id, StateRolledBack); err != nil {
		t.Fatal(err)
	}
	got := inTrash(t, f, day(10, 0), day(10, 0))
	if b := bytesOn(t, got, "2026-11-10"); b != 0 {
		t.Errorf("2026-11-10 = %d, want 0: in and out the same day", b)
	}
}

// A copy in on one day and out on the next counts on the first day only.
func TestInTrashSavingAcrossDays(t *testing.T) {
	f, id := doneSwap(t)
	placeInTrash(t, f, id, day(10, 23), nil)
	f.j.now = func() time.Time { return day(12, 1) }
	if err := f.j.SetState(context.Background(), id, StatePurged); err != nil {
		t.Fatal(err)
	}
	want := savingOf(t, f, id)
	got := inTrash(t, f, day(10, 0), day(12, 0))
	if bytesOn(t, got, "2026-11-10") != want || bytesOn(t, got, "2026-11-11") != want {
		t.Errorf("the copy was in the trash at the end of 10 and 11: %v", got)
	}
	if b := bytesOn(t, got, "2026-11-12"); b != 0 {
		t.Errorf("2026-11-12 = %d, want 0 after the purge", b)
	}
}

// A restore is a copy leaving the trash too: it counts until the day it is restored.
func TestInTrashSavingRestoreLeavesTheTrash(t *testing.T) {
	f, id := doneSwap(t)
	placeInTrash(t, f, id, day(10, 10), nil)
	f.j.now = func() time.Time { return day(11, 9) }
	if err := f.j.SetState(context.Background(), id, StateRolledBack); err != nil {
		t.Fatal(err)
	}
	want := savingOf(t, f, id)
	got := inTrash(t, f, day(10, 0), day(11, 0))
	if bytesOn(t, got, "2026-11-10") != want {
		t.Errorf("2026-11-10 = %d, want %d", bytesOn(t, got, "2026-11-10"), want)
	}
	if b := bytesOn(t, got, "2026-11-11"); b != 0 {
		t.Errorf("2026-11-11 = %d, want 0 after the restore", b)
	}
}

// The M3 revert: a purged row that returns to done is in the trash again, and left_at is cleared.
func TestPurgeRevertClearsLeftAt(t *testing.T) {
	f, id := doneSwap(t)
	placeInTrash(t, f, id, day(10, 10), nil)
	f.j.now = func() time.Time { return day(11, 9) }
	if err := f.j.SetState(context.Background(), id, StatePurged); err != nil {
		t.Fatal(err)
	}
	f.j.now = func() time.Time { return day(12, 9) }
	if err := f.j.SetState(context.Background(), id, StateDone); err != nil {
		t.Fatal(err)
	}
	var left sql.NullInt64
	var trashed int64
	if err := f.j.db.Read().QueryRow(`SELECT trashed_at, left_at FROM replace_journal WHERE id = ?`, id).Scan(&trashed, &left); err != nil {
		t.Fatal(err)
	}
	if left.Valid {
		t.Errorf("left_at = %d after the revert, want NULL", left.Int64)
	}
	if trashed != day(10, 10).Unix() {
		t.Errorf("trashed_at = %d, want the first entry %d (the revert keeps it)", trashed, day(10, 10).Unix())
	}
	want := savingOf(t, f, id)
	got := inTrash(t, f, day(12, 0), day(12, 0))
	if b := bytesOn(t, got, "2026-11-12"); b != want {
		t.Errorf("2026-11-12 after the revert = %d, want %d", b, want)
	}
}

// Upgrade from 14: a done row gets trashed_at from updated_at; a row that had already left gets none and is skipped.
func TestUpgradeFromFourteenBackfillsDoneRowsOnly(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	insert := `INSERT INTO replace_journal (job_id, original, trash, staged, final, state,
		orig_file_id, orig_size, orig_mtime, orig_ctime, orig_hash,
		new_file_id, new_size, new_mtime, new_hash, created_at, updated_at)
		VALUES (?, '/lib/x.mkv', '/lib/.syp-trash/x', '/lib/x.staged', '/lib/x.mkv', ?,
		'1:1', 100, 1, 1, 'h1', '2:2', 40, 1, 'h2', 1, ?)`
	if err := db.Tx(ctx, func(tx *sql.Tx) error {
		for _, s := range []string{
			`ALTER TABLE replace_journal DROP COLUMN trashed_at`,
			`ALTER TABLE replace_journal DROP COLUMN left_at`,
			`PRAGMA user_version = 14`,
		} {
			if _, err := tx.Exec(s); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(insert, 1, "done", 1_700_000_000); err != nil {
			return err
		}
		_, err := tx.Exec(insert, 2, "purged", 1_700_000_500)
		return err
	}); err != nil {
		db.Close()
		t.Fatalf("make a schema 14 journal: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("upgrade from 14: %v", err)
	}
	var doneTrashed, purgedTrashed sql.NullInt64
	if err := db.Read().QueryRow(`SELECT trashed_at FROM replace_journal WHERE job_id = 1`).Scan(&doneTrashed); err != nil {
		t.Fatal(err)
	}
	if err := db.Read().QueryRow(`SELECT trashed_at FROM replace_journal WHERE job_id = 2`).Scan(&purgedTrashed); err != nil {
		t.Fatal(err)
	}
	if !doneTrashed.Valid || doneTrashed.Int64 != 1_700_000_000 {
		t.Errorf("done row trashed_at = %v, want its updated_at 1700000000", doneTrashed)
	}
	if purgedTrashed.Valid {
		t.Errorf("purged row trashed_at = %d, want NULL (its entry time was lost)", purgedTrashed.Int64)
	}
	j := NewJournal(db)
	got, err := j.InTrashSaving(ctx, time.Unix(1_700_000_000, 0), time.Unix(1_700_000_000, 0), utc)
	if err != nil {
		t.Fatal(err)
	}
	if b := bytesOn(t, got, "2023-11-14"); b != 60 {
		t.Errorf("the done copy's saving on its day = %d, want 60 (only the done row counts)", b)
	}
}
