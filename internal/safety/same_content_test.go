// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// shiftTime moves a file's time by -8 ms, the move a CIFS rename was measured to make.
func shiftTime(t *testing.T, path string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	shifted := st.ModTime().Add(-8 * time.Millisecond)
	if err := os.Chtimes(path, shifted, shifted); err != nil {
		t.Fatal(err)
	}
}

// TestSameContentIgnoresTheTime: the same bytes with a moved time are the same content; the full identity says they
// are not, which is the difference between the new file's question and the original's.
func TestSameContentIgnoresTheTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(path, []byte(newBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := identify(path)
	if err != nil {
		t.Fatal(err)
	}
	shiftTime(t, path)
	if !sameContent(path, id) {
		t.Error("sameContent = false after a time-only move, want true")
	}
	if matches(path, id) {
		t.Error("matches = true after a time-only move, want false (the full identity keeps the mtime)")
	}
}

// TestSameContentComparesTheBytes: another file of the same size is not the same content, and neither is a file of
// another size.
func TestSameContentComparesTheBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(path, []byte(newBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := identify(path)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other.mkv")
	if err := os.WriteFile(other, []byte(origBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	if sameContent(other, id) {
		t.Error("sameContent = true for another file of the same size, want false")
	}
	if err := os.WriteFile(other, []byte(newBytes+"more"), 0o644); err != nil {
		t.Fatal(err)
	}
	if sameContent(other, id) {
		t.Error("sameContent = true for a file of another size, want false")
	}
}

// TestSwapKeepsTheNewFileWhoseRenameMovedItsTime: on a share the rename into the name moves the new file's observed
// mtime, and the swap still succeeds: the file at its name is ours by its content.
func TestSwapKeepsTheNewFileWhoseRenameMovedItsTime(t *testing.T) {
	f := newSwapFixture(t, "")
	hookAfterFinalMoved = func() { shiftTime(t, f.final) }
	t.Cleanup(func() { hookAfterFinalMoved = func() {} })
	res, err := Swap(context.Background(), f.j, f.plan(21, 7))
	if err != nil {
		t.Fatalf("swap: %v (the moved time is not a change)", err)
	}
	if got := readFile(t, res.Final); got != newBytes {
		t.Errorf("the new file at its name = %q, want its own bytes", got)
	}
	if got := readFile(t, res.Trash); got != origBytes {
		t.Errorf("the trash copy = %q, want the original", got)
	}
}

// TestRecoverySettlesASwapWhoseRenameMovedItsTime: a crash between the new file's rename and the settle, on a share
// whose rename moved the new file's time, is still a finished swap: the row is done and the original is not put back.
func TestRecoverySettlesASwapWhoseRenameMovedItsTime(t *testing.T) {
	f := newSwapFixture(t, "")
	f.final = filepath.Join(f.lib, "movie.mp4") // another extension, so the original's name stays free
	trash := filepath.Join(f.lib, TrashDir, "movie.13.mkv")
	if err := os.Mkdir(filepath.Dir(trash), 0o755); err != nil {
		t.Fatal(err)
	}
	row := journalRow(t, f, 13, trash)
	// A stale copy in the row's holding folder from an earlier restore must not make the finished swap look unfinished.
	holding := holdingPath(filepath.Dir(trash), row.ID, f.final)
	if err := os.MkdirAll(filepath.Dir(holding), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(holding, []byte(newBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.original, trash); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.staged, f.final); err != nil {
		t.Fatal(err)
	}
	shiftTime(t, f.final)
	rep, err := Recover(context.Background(), f.j)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done != 1 {
		t.Errorf("report = %+v, want one done", rep)
	}
	if exists(f.original) {
		t.Errorf("recovery put the original back over the new file at %s", f.final)
	}
	if got := readFile(t, f.final); got != newBytes {
		t.Errorf("the new file = %q, want it left at its name", got)
	}
	if got := readFile(t, trash); got != origBytes {
		t.Errorf("the trash copy = %q, want the original still there", got)
	}
}

// TestRestoreWorksWhenTheRenamesMovedTheTimes: a person's restore on a share, where the converted file's observed time
// moved when it was renamed into place, puts the original back and removes the converted file.
func TestRestoreWorksWhenTheRenamesMovedTheTimes(t *testing.T) {
	f, id := doneSwap(t)
	shiftTime(t, f.final)
	if err := Restore(context.Background(), f.j, id); err != nil {
		t.Fatalf("restore: %v (the moved time is not a change)", err)
	}
	if got := readFile(t, f.original); got != origBytes {
		t.Errorf("the original = %q, want its own bytes back", got)
	}
	if stateOf(t, f, id) != StateRolledBack {
		t.Errorf("state = %s, want rolled_back", stateOf(t, f, id))
	}
	if _, err := os.Stat(filepath.Join(f.lib, TrashDir, holdingDir)); !os.IsNotExist(err) {
		t.Errorf("the holding folder is still there after a restore: %v", err)
	}
}

// TestRestoreChecksTheConvertedFileAgainBeforeItMoves: a time moved in the window between the restore's checks does
// not stop it; a content change there would (TestRestoreRefusesAChangedConvertedFile).
func TestRestoreChecksTheConvertedFileAgainBeforeItMoves(t *testing.T) {
	f, id := doneSwap(t)
	hookRestoreStep = func(step int) {
		if step == 1 {
			shiftTime(t, f.final)
		}
	}
	t.Cleanup(func() { hookRestoreStep = func(int) {} })
	if err := Restore(context.Background(), f.j, id); err != nil {
		t.Fatalf("restore: %v (the moved time is not a change)", err)
	}
	if got := readFile(t, f.original); got != origBytes {
		t.Errorf("the original = %q, want its own bytes back", got)
	}
}

// TestRestoreCrashPartWaySettlesACopyWhoseRenameMovedItsTime: a crash between the converted file moving aside and the
// original coming back, on a share whose rename moved the copy's time, is undone by recovery: the converted file
// returns to its name and the row is done again.
func TestRestoreCrashPartWaySettlesACopyWhoseRenameMovedItsTime(t *testing.T) {
	f, id := doneSwap(t)
	crashAt(t, 2)
	restoreUntilCrash(f, id)
	r, _, err := f.j.Entry(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	shiftTime(t, holdingPath(filepath.Dir(r.Trash), id, r.Final))
	rep, err := Recover(context.Background(), f.j)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Attention != 0 {
		t.Errorf("report = %+v, want no attention for a moved time", rep)
	}
	if got := readFile(t, f.final); got != newBytes {
		t.Errorf("the converted file = %q at its name, want its own bytes", got)
	}
	if stateOf(t, f, id) != StateDone {
		t.Errorf("state = %s, want done", stateOf(t, f, id))
	}
}

// TestRestoreCrashAfterTheOriginalIsBackRemovesTheCopy: recovery's last check on the converted copy before it removes
// it is the copy's content; a time the share moved in that window does not leave the copy behind for a person.
func TestRestoreCrashAfterTheOriginalIsBackRemovesTheCopy(t *testing.T) {
	f, id := doneSwap(t)
	crashAt(t, 3)
	restoreUntilCrash(f, id)
	r, _, err := f.j.Entry(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	hookBeforeHoldingRemoval = func() { shiftTime(t, holdingPath(filepath.Dir(r.Trash), id, r.Final)) }
	t.Cleanup(func() { hookBeforeHoldingRemoval = func() {} })
	rep, err := Recover(context.Background(), f.j)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Attention != 0 || rep.RolledBack != 1 {
		t.Errorf("report = %+v, want one rolled back and nothing for a person", rep)
	}
	if _, err := os.Stat(filepath.Join(f.lib, TrashDir, holdingDir)); !os.IsNotExist(err) {
		t.Errorf("the holding folder is still there after recovery: %v", err)
	}
	if got := readFile(t, f.original); got != origBytes {
		t.Errorf("the original = %q, want its own bytes back", got)
	}
	if stateOf(t, f, id) != StateRolledBack {
		t.Errorf("state = %s, want rolled_back", stateOf(t, f, id))
	}
}

// lookAlike puts the original's bytes at the original's name with another time: a file only the full identity tells
// apart from the original.
func lookAlike(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(origBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

// TestRecoveryLeavesALookAlikeAndSettlesDone: a byte-identical look-alike at the original's old name does not make a
// finished swap look unfinished. The new file is at its name and the original is in the trash, so the row is done and
// the stranger is left where it is; content identity for the original would wrongly report attention here.
func TestRecoveryLeavesALookAlikeAndSettlesDone(t *testing.T) {
	f := newSwapFixture(t, "")
	f.final = filepath.Join(f.lib, "movie.mp4") // another extension, so the original's name is free for the look-alike
	trash := filepath.Join(f.lib, TrashDir, "movie.15.mkv")
	if err := os.Mkdir(filepath.Dir(trash), 0o755); err != nil {
		t.Fatal(err)
	}
	journalRow(t, f, 15, trash)
	if err := os.Rename(f.original, trash); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.staged, f.final); err != nil {
		t.Fatal(err)
	}
	lookAlike(t, f.original)

	rep, err := Recover(context.Background(), f.j)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done != 1 || rep.Attention != 0 {
		t.Errorf("report = %+v, want the finished swap done and the look-alike left alone", rep)
	}
	if got := readFile(t, f.original); got != origBytes {
		t.Errorf("the look-alike = %q, want it left where it is", got)
	}
	if got := readFile(t, trash); got != origBytes {
		t.Errorf("the original in the trash = %q, want it still there", got)
	}
	if got := readFile(t, f.final); got != newBytes {
		t.Errorf("the new file = %q, want it at its name", got)
	}
}

// TestRecoveryRemovesNothingBesideALookAlike: a byte-identical look-alike at the original's name, where a restore
// stopped after the original was supposed to be back, is not the original. Recovery must not remove the converted copy
// on its strength: the row waits for a person and the copy stays.
func TestRecoveryRemovesNothingBesideALookAlike(t *testing.T) {
	f, id := doneSwapAs(t, true)
	crashAt(t, 3)
	restoreUntilCrash(f, id)
	lookAlike(t, f.original)
	r, _, err := f.j.Entry(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	holding := holdingPath(filepath.Dir(r.Trash), id, r.Final)

	rep, err := Recover(context.Background(), f.j)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Attention != 1 || rep.RolledBack != 0 {
		t.Errorf("report = %+v, want the row left for a person", rep)
	}
	if got := readFile(t, holding); got != newBytes {
		t.Errorf("the converted copy = %q, want it kept while a look-alike sits at the original's name", got)
	}
	if got := readFile(t, f.original); got != origBytes {
		t.Errorf("the look-alike = %q, want it left where it is", got)
	}
	if stateOf(t, f, id) != StateAttention {
		t.Errorf("state = %s, want attention", stateOf(t, f, id))
	}
}
