// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

var utc = time.UTC

func day(d int, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, utc) }

// wantSaving is origBytes (27 bytes) minus smallNew (5 bytes): the saving the in-trash tests must see, not zero.
const smallNew = "small"

const wantSaving = 22

// swapAt is a real swap at the given time: the journal's clock is set before Swap, so the row's entry time (trashed_at)
// is written by the swap itself. The new file is smallNew unless content says otherwise.
func swapAt(t *testing.T, content string, at time.Time) (swapFixture, int64) {
	t.Helper()
	f := newSwapFixture(t, "")
	f.j.now = func() time.Time { return at }
	if err := os.WriteFile(f.staged, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Swap(context.Background(), f.j, f.plan(7, 7)); err != nil {
		t.Fatalf("swap: %v", err)
	}
	return f, rowIDFor(t, f)
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

// The fixture must give the real saving, so a wrong figure cannot hide behind a clamp to zero.
func requireSaving(t *testing.T, f swapFixture, id int64) {
	t.Helper()
	if got := savingOf(t, f, id); got != wantSaving {
		t.Fatalf("the fixture's saving = %d, want %d (the tests need a nonzero figure)", got, wantSaving)
	}
}

// A copy is counted on every day it is in the trash at the day's end: the saving, not zero.
func TestInTrashSavingCountsACopyOnEachDayItIsIn(t *testing.T) {
	f, id := swapAt(t, smallNew, day(10, 10))
	requireSaving(t, f, id)
	got := inTrash(t, f, day(10, 0), day(12, 0))
	for _, d := range []string{"2026-11-10", "2026-11-11", "2026-11-12"} {
		if b := bytesOn(t, got, d); b != wantSaving {
			t.Errorf("%s = %d, want %d", d, b, wantSaving)
		}
	}
}

// In and out on the same day: zero at that day's end, although the copy was in the trash during the day.
func TestInTrashSavingInAndOutOnTheSameDayIsZeroAtTheEnd(t *testing.T) {
	f, id := swapAt(t, smallNew, day(10, 9))
	requireSaving(t, f, id)
	f.j.now = func() time.Time { return day(10, 17) }
	if err := Restore(context.Background(), f.j, id); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got := inTrash(t, f, day(10, 0), day(10, 0))
	if b := bytesOn(t, got, "2026-11-10"); b != 0 {
		t.Errorf("2026-11-10 = %d, want 0: in and out the same day", b)
	}
}

// Purged on the 12th: counted on the 10th and 11th at the saving, then zero.
func TestInTrashSavingPurgeLeavesTheTrash(t *testing.T) {
	f, id := swapAt(t, smallNew, day(10, 23))
	requireSaving(t, f, id)
	f.j.now = func() time.Time { return day(12, 1) }
	rep, err := SweepTrash(context.Background(), f.j, time.Now().Add(8*24*time.Hour), 7)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Purged != 1 {
		t.Fatalf("purged = %d, want 1 (the sweep must purge the copy)", rep.Purged)
	}
	got := inTrash(t, f, day(10, 0), day(12, 0))
	if bytesOn(t, got, "2026-11-10") != wantSaving || bytesOn(t, got, "2026-11-11") != wantSaving {
		t.Errorf("the copy was in the trash at the end of 10 and 11: %v", got)
	}
	if b := bytesOn(t, got, "2026-11-12"); b != 0 {
		t.Errorf("2026-11-12 = %d, want 0 after the purge", b)
	}
}

// Restored on the 11th: counted on the 10th, then zero.
func TestInTrashSavingRestoreLeavesTheTrash(t *testing.T) {
	f, id := swapAt(t, smallNew, day(10, 10))
	requireSaving(t, f, id)
	f.j.now = func() time.Time { return day(11, 9) }
	if err := Restore(context.Background(), f.j, id); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got := inTrash(t, f, day(10, 0), day(11, 0))
	if b := bytesOn(t, got, "2026-11-10"); b != wantSaving {
		t.Errorf("2026-11-10 = %d, want %d", b, wantSaving)
	}
	if b := bytesOn(t, got, "2026-11-11"); b != 0 {
		t.Errorf("2026-11-11 = %d, want 0 after the restore", b)
	}
}

// The M3 revert, as after a crash: a purged row whose file stayed returns to done on recovery. left_at is cleared,
// trashed_at keeps the first entry, and the copy counts in the trash again at the saving.
func TestPurgeRevertClearsLeftAt(t *testing.T) {
	f, id := swapAt(t, smallNew, day(10, 10))
	requireSaving(t, f, id)
	f.j.now = func() time.Time { return day(11, 9) }
	if err := f.j.SetState(context.Background(), id, StatePurged); err != nil {
		t.Fatal(err)
	}
	f.j.now = func() time.Time { return day(12, 9) }
	if _, err := Recover(context.Background(), f.j); err != nil {
		t.Fatal(err)
	}
	if stateOf(t, f, id) != StateDone {
		t.Fatalf("state = %s after recovery, want done", stateOf(t, f, id))
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
		t.Errorf("trashed_at = %d, want the first entry %d", trashed, day(10, 10).Unix())
	}
	got := inTrash(t, f, day(10, 0), day(12, 0))
	if b := bytesOn(t, got, "2026-11-11"); b != wantSaving {
		t.Errorf("2026-11-11 (purged that day) = %d, want %d: the revert puts it back in the trash", b, wantSaving)
	}
	if b := bytesOn(t, got, "2026-11-12"); b != wantSaving {
		t.Errorf("2026-11-12 after the revert = %d, want %d", b, wantSaving)
	}
}

// Clamp: a new file bigger than the original saves nothing, so the figure is zero by the clamp, not by a missing row.
func TestInTrashSavingClampsANegativeSavingToZero(t *testing.T) {
	big := make([]byte, 40)
	for i := range big {
		big[i] = 'x'
	}
	f, id := swapAt(t, string(big), day(10, 10))
	var orig, now int64
	if err := f.j.db.Read().QueryRow(`SELECT orig_size, new_size FROM replace_journal WHERE id = ?`, id).Scan(&orig, &now); err != nil {
		t.Fatal(err)
	}
	if now <= orig {
		t.Fatalf("new %d is not bigger than the original %d; the clamp test needs a bigger new file", now, orig)
	}
	got := inTrash(t, f, day(10, 0), day(10, 0))
	if b := bytesOn(t, got, "2026-11-10"); b != 0 {
		t.Errorf("2026-11-10 = %d, want 0: a negative saving clamps to zero", b)
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
