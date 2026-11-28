// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// doneSwap runs a swap that keeps the original in the trash, and returns the fixture and the journal row id. The
// converted file takes the original's name (same extension) unless mp4 is set, when it is movie.mp4.
func doneSwap(t *testing.T) (swapFixture, int64) {
	t.Helper()
	return doneSwapAs(t, false)
}

func doneSwapAs(t *testing.T, mp4 bool) (swapFixture, int64) {
	t.Helper()
	f := newSwapFixture(t, "")
	if mp4 {
		f.final = f.lib + "/movie.mp4"
	}
	res, err := Swap(context.Background(), f.j, f.plan(7, 7))
	if err != nil {
		t.Fatalf("swap: %v", err)
	}
	if _, err := os.Stat(res.Trash); err != nil {
		t.Fatalf("the original is not in the trash: %v", err)
	}
	return f, rowIDFor(t, f)
}

func rowIDFor(t *testing.T, f swapFixture) int64 {
	t.Helper()
	var id int64
	if err := f.j.db.Read().QueryRow(`SELECT id FROM replace_journal ORDER BY id DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func stateOf(t *testing.T, f swapFixture, id int64) string {
	t.Helper()
	r, _, err := f.j.Entry(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return r.State
}

// The sweep removes a trash copy past its keep days, and the row goes to "purged".
func TestSweepPurgesAnOldTrashCopy(t *testing.T) {
	f, id := doneSwap(t)
	rep, err := SweepTrash(context.Background(), f.j, time.Now().Add(8*24*time.Hour), 7)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Purged != 1 {
		t.Fatalf("purged = %d, want 1", rep.Purged)
	}
	if stateOf(t, f, id) != StatePurged {
		t.Errorf("state = %s, want purged", stateOf(t, f, id))
	}
	if _, err := os.Stat(f.lib + "/.syp-trash"); err != nil {
		entries, _ := os.ReadDir(f.lib + "/.syp-trash")
		if len(entries) != 0 {
			t.Errorf("the trash still holds %d file(s)", len(entries))
		}
	}
}

// A copy inside its keep days stays.
func TestSweepKeepsARecentTrashCopy(t *testing.T) {
	f, id := doneSwap(t)
	rep, err := SweepTrash(context.Background(), f.j, time.Now().Add(24*time.Hour), 7)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Purged != 0 || stateOf(t, f, id) != StateDone {
		t.Errorf("purged = %d, state = %s; a copy inside its keep days must stay done", rep.Purged, stateOf(t, f, id))
	}
}

// keepOriginalsDays 0 purges an old copy at once.
func TestSweepWithKeepZeroPurgesAnOldCopy(t *testing.T) {
	f, id := doneSwap(t)
	rep, err := SweepTrash(context.Background(), f.j, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Purged != 1 || stateOf(t, f, id) != StatePurged {
		t.Errorf("purged = %d, state = %s; keep 0 must purge the old copy", rep.Purged, stateOf(t, f, id))
	}
}

// A trash copy that is no longer the recorded original is not removed by the sweep.
func TestSweepLeavesAChangedTrashCopy(t *testing.T) {
	f, id := doneSwap(t)
	r, _, _ := f.j.Entry(context.Background(), id)
	if err := os.WriteFile(r.Trash, []byte("someone else's bytes!!!!!!!"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := SweepTrash(context.Background(), f.j, time.Now().Add(30*24*time.Hour), 7)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Purged != 0 || rep.Kept != 1 {
		t.Errorf("purged = %d, kept = %d; a changed copy must be kept", rep.Purged, rep.Kept)
	}
	if _, err := os.Stat(r.Trash); err != nil {
		t.Errorf("the changed copy was removed: %v", err)
	}
}

// The sweep never touches a row in "attention", whatever its age.
func TestSweepNeverPurgesAnAttentionRow(t *testing.T) {
	f, id := doneSwap(t)
	if err := f.j.SetState(context.Background(), id, StateAttention); err != nil {
		t.Fatal(err)
	}
	r, _, _ := f.j.Entry(context.Background(), id)
	rep, err := SweepTrash(context.Background(), f.j, time.Now().Add(365*24*time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Purged != 0 || stateOf(t, f, id) != StateAttention {
		t.Errorf("purged = %d, state = %s; the sweep must leave an attention row", rep.Purged, stateOf(t, f, id))
	}
	if _, err := os.Stat(r.Trash); err != nil {
		t.Errorf("the attention row's trash copy was removed: %v", err)
	}
}

// keepOriginalsDays is read from the store, 7 when never set, and refused outside 0..365.
func TestKeepDaysFromTheStore(t *testing.T) {
	f := newSwapFixture(t, "")
	ctx := context.Background()
	if days, err := f.j.KeepDays(ctx); err != nil || days != 7 {
		t.Fatalf("unset keep = %d, %v; want 7", days, err)
	}
	if err := f.j.SetKeepDays(ctx, 30); err != nil {
		t.Fatal(err)
	}
	if days, _ := f.j.KeepDays(ctx); days != 30 {
		t.Errorf("keep = %d, want 30", days)
	}
	if err := f.j.SetKeepDays(ctx, 400); err == nil {
		t.Error("keep 400 was accepted")
	}
}

// Restore puts the original back byte for byte, removes the converted file, and the row is "rolled_back".
func TestRestoreBringsTheOriginalBack(t *testing.T) {
	f, id := doneSwap(t)
	if err := Restore(context.Background(), f.j, id); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := readFile(t, f.original); got != origBytes {
		t.Errorf("the original is %q, want its own bytes", got)
	}
	if f.final != f.original {
		if _, err := os.Stat(f.final); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the converted file is still there: %v", err)
		}
	}
	if stateOf(t, f, id) != StateRolledBack {
		t.Errorf("state = %s, want rolled_back", stateOf(t, f, id))
	}
}

// A purged original cannot be restored, and nothing changes.
func TestRestoreRefusesAGoneOriginal(t *testing.T) {
	f, id := doneSwap(t)
	if _, err := SweepTrash(context.Background(), f.j, time.Now().Add(9*24*time.Hour), 7); err != nil {
		t.Fatal(err)
	}
	if err := Restore(context.Background(), f.j, id); !errors.Is(err, ErrOriginalGone) {
		t.Fatalf("restore = %v, want ErrOriginalGone", err)
	}
	if got := readFile(t, f.final); got != newBytes {
		t.Errorf("the converted file changed: %q", got)
	}
}

// A converted file changed since the replace is refused, even one that kept its size and mtime.
func TestRestoreRefusesAChangedConvertedFile(t *testing.T) {
	f, id := doneSwap(t)
	r, _, _ := f.j.Entry(context.Background(), id)
	st, err := os.Stat(f.final)
	if err != nil {
		t.Fatal(err)
	}
	same := make([]byte, len(newBytes))
	for i := range same {
		same[i] = 'x'
	}
	if err := os.WriteFile(f.final, same, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(f.final, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := Restore(context.Background(), f.j, id); !errors.Is(err, ErrConvertedChanged) {
		t.Fatalf("restore = %v, want ErrConvertedChanged", err)
	}
	if _, err := os.Stat(r.Trash); err != nil {
		t.Errorf("the trash copy moved: %v", err)
	}
	if stateOf(t, f, id) != StateDone {
		t.Errorf("state = %s, want done after a refusal", stateOf(t, f, id))
	}
}

// Something at the original's name refuses the restore, and nothing is overwritten.
func TestRestoreRefusesATakenName(t *testing.T) {
	f, id := doneSwapAs(t, true)
	if err := os.WriteFile(f.original, []byte("a new file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Restore(context.Background(), f.j, id); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("restore = %v, want ErrNameTaken", err)
	}
	if got := readFile(t, f.original); got != "a new file" {
		t.Errorf("the file at the name was overwritten: %q", got)
	}
}

// A name taken in the window after the checks is refused by the no-replace move; the stranger's file is kept.
func TestRestoreNeverOverwritesANameTakenMeanwhile(t *testing.T) {
	f, id := doneSwapAs(t, true)
	hookRestoreStep = func(step int) {
		if step == 2 {
			_ = os.WriteFile(f.original, []byte("taken meanwhile"), 0o644)
		}
	}
	defer func() { hookRestoreStep = func(int) {} }()
	if err := Restore(context.Background(), f.j, id); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("restore = %v, want ErrNameTaken", err)
	}
	if got := readFile(t, f.original); got != "taken meanwhile" {
		t.Errorf("the stranger's file was overwritten: %q", got)
	}
	// A refused restore changes nothing: the converted file is back where it was, and the row is done again.
	if got := readFile(t, f.final); got != newBytes {
		t.Errorf("the converted file is %q after a refused restore, want its own bytes back", got)
	}
	if stateOf(t, f, id) != StateDone {
		t.Errorf("state = %s, want done after a refused restore", stateOf(t, f, id))
	}
}

// A crash before the converted file is removed leaves the swap done; recovery keeps the converted file.
func TestRestoreCrashBeforeTheRemovalRecoversToDone(t *testing.T) {
	f, id := doneSwap(t)
	crashAt(t, 1)
	restoreUntilCrash(f, id)
	if _, err := Recover(context.Background(), f.j); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, f.final); got != newBytes {
		t.Errorf("recovery touched the converted file: %q", got)
	}
	if stateOf(t, f, id) != StateDone {
		t.Errorf("state = %s, want done", stateOf(t, f, id))
	}
}

// A crash after the converted file is moved aside and before the original is back leaves the name empty. Recovery
// settles it from the files: the restore is undone, the converted file returns to its name, and the row is done.
// Nothing is lost and the name is not left empty.
func TestRestoreCrashPartWayRecoversTheConvertedFile(t *testing.T) {
	for _, mp4 := range []bool{false, true} {
		f, id := doneSwapAs(t, mp4)
		crashAt(t, 2)
		restoreUntilCrash(f, id)
		if _, err := Recover(context.Background(), f.j); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, f.final); got != newBytes {
			t.Errorf("mp4=%v: the converted file is %q at its name after recovery, want its own bytes", mp4, got)
		}
		if stateOf(t, f, id) != StateDone {
			t.Errorf("mp4=%v: state = %s, want done", mp4, stateOf(t, f, id))
		}
	}
}

// A crash after the original is back, before the restore is recorded, is finished by recovery: the original is at its
// name, the converted copy is removed, and the row is rolled back.
func TestRestoreCrashAfterTheOriginalIsBackFinishesIt(t *testing.T) {
	f, id := doneSwap(t)
	crashAt(t, 3)
	restoreUntilCrash(f, id)
	rep, err := Recover(context.Background(), f.j)
	if err != nil {
		t.Fatal(err)
	}
	// N4: the restore recovery finished names its job, so the caller can write its History line.
	if len(rep.Restored) != 1 || rep.Restored[0] != 7 {
		t.Errorf("restored jobs = %v, want [7]", rep.Restored)
	}
	if got := readFile(t, f.original); got != origBytes {
		t.Errorf("the original is %q after recovery, want its own bytes", got)
	}
	entries, _ := os.ReadDir(f.lib + "/" + TrashDir)
	for _, e := range entries {
		if readFile(t, f.lib+"/"+TrashDir+"/"+e.Name()) == newBytes {
			t.Errorf("the converted copy %s is still in the trash after the restore finished", e.Name())
		}
	}
	if stateOf(t, f, id) != StateRolledBack {
		t.Errorf("state = %s, want rolled_back", stateOf(t, f, id))
	}
}

// crashAt makes the restore stop at a step by panicking, which the test recovers from.
func crashAt(t *testing.T, step int) {
	t.Helper()
	hookRestoreStep = func(s int) {
		if s == step {
			hookRestoreStep = func(int) {}
			panic("simulated crash")
		}
	}
	t.Cleanup(func() { hookRestoreStep = func(int) {} })
}

// restoreUntilCrash runs a restore that the crash hook stops with a panic, and returns once the panic is caught.
func restoreUntilCrash(f swapFixture, id int64) {
	defer func() { _ = recover() }()
	_ = Restore(context.Background(), f.j, id)
}

// A refused restore never deletes the converted file. With the converted file at the original's own name, a name
// taken after it is moved aside leaves the converted bytes in the trash folder, not gone.
func TestRestoreRefusalKeepsTheConvertedFile(t *testing.T) {
	f, id := doneSwap(t) // same extension: the converted file is at the original's name
	hookRestoreStep = func(step int) {
		if step == 2 {
			_ = os.WriteFile(f.original, []byte("taken meanwhile"), 0o644)
		}
	}
	defer func() { hookRestoreStep = func(int) {} }()
	if err := Restore(context.Background(), f.j, id); err == nil {
		t.Fatal("the restore was not refused")
	}
	found := false
	_ = filepath.WalkDir(filepath.Join(f.lib, TrashDir), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, rerr := os.ReadFile(p); rerr == nil && string(b) == newBytes {
				found = true
			}
		}
		return nil
	})
	if !found {
		t.Errorf("the converted bytes are in no trash file: a refused restore lost the converted file")
	}
}

// N1: a successful restore leaves no copy of the converted bytes, and no holding folder, in the trash.
func TestRestoreLeavesNoHoldingCopy(t *testing.T) {
	f, id := doneSwap(t)
	if err := Restore(context.Background(), f.j, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.lib, TrashDir, holdingDir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the holding folder is still there after a restore: %v", err)
	}
}

// N2: a row a restore has claimed is not purged by a sweep; the copy stays for the restore.
func TestSweepSkipsARowBeingRestored(t *testing.T) {
	f, id := doneSwap(t)
	r, _, _ := f.j.Entry(context.Background(), id)
	// A restore takes the row after the sweep has listed it and before the sweep claims it.
	hookBeforeSweepClaim = func(got int64) {
		if got == id {
			_ = f.j.SetState(context.Background(), id, StateMoving)
		}
	}
	defer func() { hookBeforeSweepClaim = func(int64) {} }()
	rep, err := SweepTrash(context.Background(), f.j, time.Now().Add(30*24*time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Purged != 0 {
		t.Errorf("purged = %d, want 0 for a row being restored", rep.Purged)
	}
	if _, err := os.Stat(r.Trash); err != nil {
		t.Errorf("the copy a restore claimed was removed: %v", err)
	}
}

// N3: recovery touches only its own row's holding copy. A stranger file with the same identity, in another row's
// holding folder, is left where it is.
func TestRecoverTouchesOnlyItsOwnHoldingCopy(t *testing.T) {
	f, id := doneSwap(t)
	crashAt(t, 2)
	restoreUntilCrash(f, id)
	own := holdingPath(filepath.Join(f.lib, TrashDir), id, f.final)
	other := filepath.Join(f.lib, TrashDir, holdingDir, "999", filepath.Base(f.final))
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(own, other); err != nil {
		t.Skipf("hard links are not available here: %v", err)
	}
	if _, err := Recover(context.Background(), f.j); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("another row's holding copy was moved or removed: %v", err)
	}
	if got := readFile(t, f.final); got != newBytes {
		t.Errorf("the converted file is %q after recovery, want its own bytes", got)
	}
}
