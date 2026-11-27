// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

const (
	origBytes = "ORIGINAL-CONTENT-0123456789"
	newBytes  = "NEW-CONTENT-ABCDEFGHIJKLMNOPQ"
)

// openStore opens the store and brings it to the current schema, as the app does at start.
func openStore(t *testing.T, dir string) *store.DB {
	t.Helper()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open the store: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate the store: %v", err)
	}
	return db
}

// swapFixture is a library folder with an original video and a staged new file beside it, and a journal.
type swapFixture struct {
	lib, original, staged, final string
	j                            *Journal
	dbDir                        string
}

func newSwapFixture(t *testing.T, keepDir string) swapFixture {
	t.Helper()
	lib := t.TempDir()
	dbDir := filepath.Join(t.TempDir(), "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := swapFixture{lib: lib, dbDir: dbDir}
	f.original = filepath.Join(lib, "movie.mkv")
	f.final = filepath.Join(lib, "movie.mkv")
	staging := filepath.Join(lib, ".staging")
	if err := os.Mkdir(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	f.staged = filepath.Join(staging, "out.mkv")
	if err := os.WriteFile(f.original, []byte(origBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.staged, []byte(newBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	db := openStore(t, dbDir)
	t.Cleanup(func() { _ = db.Close() })
	f.j = NewJournal(db)
	return f
}

func (f swapFixture) plan(jobID int64, keep int) SwapPlan {
	return SwapPlan{JobID: jobID, Original: f.original, Staged: f.staged, Final: f.final, KeepDays: keep}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

// TestTrashNamesNeverCollide: "<stem>.<jobid><ext>", then ".2", ".3", skipping names that exist.
func TestTrashNamesNeverCollide(t *testing.T) {
	dir := t.TempDir()
	first, err := trashPath(dir, "/lib/movie.mkv", 42)
	if err != nil || filepath.Base(first) != "movie.42.mkv" {
		t.Fatalf("first name = %q, %v; want movie.42.mkv", first, err)
	}
	if err := os.WriteFile(first, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	second, _ := trashPath(dir, "/lib/movie.mkv", 42)
	if filepath.Base(second) != "movie.42.2.mkv" {
		t.Errorf("second name = %q, want movie.42.2.mkv", second)
	}
	if err := os.WriteFile(second, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	third, _ := trashPath(dir, "/lib/movie.mkv", 42)
	if filepath.Base(third) != "movie.42.3.mkv" {
		t.Errorf("third name = %q, want movie.42.3.mkv", third)
	}
}

// TestTrashFolderNotCreatableKeepsTheOriginal: a trash folder that cannot be made keeps the job's original in place,
// and nothing moves.
func TestTrashFolderNotCreatableKeepsTheOriginal(t *testing.T) {
	f := newSwapFixture(t, "")
	if err := os.WriteFile(filepath.Join(f.lib, TrashDir), []byte("a file, not a folder"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Swap(context.Background(), f.j, f.plan(1, 1))
	if !errors.Is(err, ErrNoTrash) {
		t.Fatalf("error = %v, want ErrNoTrash", err)
	}
	if got := readFile(t, f.original); got != origBytes {
		t.Errorf("original = %q, want it untouched", got)
	}
	if got := readFile(t, f.staged); got != newBytes {
		t.Errorf("staged = %q, want it untouched", got)
	}
}

// TestReadOnlyLibraryMovesNothing: a folder the app cannot write in moves nothing (L-SAFE-6).
func TestReadOnlyLibraryMovesNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("read-only folders are not modelled by mode bits on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes to a read-only folder, so the refusal is not tried")
	}
	f := newSwapFixture(t, "")
	if err := os.Chmod(f.lib, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.lib, 0o755) })
	_, err := Swap(context.Background(), f.j, f.plan(1, 1))
	if !errors.Is(err, ErrReadOnlyLibrary) {
		t.Fatalf("error = %v, want ErrReadOnlyLibrary", err)
	}
	if got := readFile(t, f.original); got != origBytes {
		t.Errorf("original = %q, want it untouched", got)
	}
}

// TestTheOriginalIsKept: with keep days, the original is in the trash under its name, and the new file has the name.
func TestTheOriginalIsKept(t *testing.T) {
	f := newSwapFixture(t, "")
	res, err := Swap(context.Background(), f.j, f.plan(7, 30))
	if err != nil {
		t.Fatalf("swap: %v", err)
	}
	if res.Purged {
		t.Error("the trash copy was purged with keep 30")
	}
	if got := readFile(t, res.Trash); got != origBytes {
		t.Errorf("trash = %q, want the original", got)
	}
	if filepath.Base(res.Trash) != "movie.7.mkv" {
		t.Errorf("trash name = %q, want movie.7.mkv", filepath.Base(res.Trash))
	}
	if got := readFile(t, f.final); got != newBytes {
		t.Errorf("name = %q, want the new file", got)
	}
}

// TestKeepZeroDaysDeletesTheTrashCopy: keep 0 removes the trash copy right after a verified swap (L-SAFE-6).
func TestKeepZeroDaysDeletesTheTrashCopy(t *testing.T) {
	f := newSwapFixture(t, "")
	res, err := Swap(context.Background(), f.j, f.plan(8, 0))
	if err != nil {
		t.Fatalf("swap: %v", err)
	}
	if !res.Purged {
		t.Error("keep 0 did not purge the trash copy")
	}
	if _, err := os.Lstat(res.Trash); !os.IsNotExist(err) {
		t.Errorf("trash copy still exists: %v", err)
	}
	if got := readFile(t, f.final); got != newBytes {
		t.Errorf("name = %q, want the new file", got)
	}
}

// TestSourceChangedWhileReplacingIsKept: an original edited after it was read is kept; nothing is swapped (L-SAFE-5).
func TestSourceChangedWhileReplacingIsKept(t *testing.T) {
	f := newSwapFixture(t, "")
	afterIdentify = func(path string) {
		if path == f.original {
			_ = os.WriteFile(path, []byte(origBytes+"-EDITED"), 0o644)
		}
	}
	t.Cleanup(func() { afterIdentify = func(string) {} })
	_, err := Swap(context.Background(), f.j, f.plan(2, 1))
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("error = %v, want ErrSourceChanged", err)
	}
	if got := readFile(t, f.original); got != origBytes+"-EDITED" {
		t.Errorf("original = %q, want the edit kept", got)
	}
}

// TestEditKeepingSizeAndTimeIsKept: an edit that keeps the size and the mtime still moves the change time, so it is
// kept (L-SAFE-5). Linux reads the change time.
func TestEditKeepingSizeAndTimeIsKept(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the change time is read on Linux")
	}
	f := newSwapFixture(t, "")
	st, err := os.Stat(f.original)
	if err != nil {
		t.Fatal(err)
	}
	afterIdentify = func(path string) {
		if path != f.original {
			return
		}
		same := strings.Repeat("x", len(origBytes))
		_ = os.WriteFile(path, []byte(same), 0o644)
		_ = os.Chtimes(path, st.ModTime(), st.ModTime())
	}
	t.Cleanup(func() { afterIdentify = func(string) {} })
	_, err = Swap(context.Background(), f.j, f.plan(3, 1))
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("error = %v, want ErrSourceChanged for a same-size, same-mtime edit", err)
	}
}

// TestHardLinkedSourceIsSkipped: a file with more than one name is shared, so it is not replaced.
func TestHardLinkedSourceIsSkipped(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("hard links are checked on Linux here")
	}
	f := newSwapFixture(t, "")
	if err := os.Link(f.original, filepath.Join(f.lib, "other-name.mkv")); err != nil {
		t.Skipf("this file system has no hard links: %v", err)
	}
	_, err := Swap(context.Background(), f.j, f.plan(4, 1))
	if !errors.Is(err, ErrSharedSource) {
		t.Fatalf("error = %v, want ErrSharedSource", err)
	}
}

// TestAFailedPutBackIsNotSaidToHaveHappened: when the new file cannot be placed and the original cannot come back,
// the row is "attention", the original stays in the trash, and the error says it was not put back.
func TestAFailedPutBackIsNotSaidToHaveHappened(t *testing.T) {
	f := newSwapFixture(t, "")
	hookAfterOriginalMoved = func() {
		// A stranger takes the name, so neither the new file nor the original can go there.
		_ = os.WriteFile(f.original, []byte("STRANGER"), 0o644)
	}
	t.Cleanup(func() { hookAfterOriginalMoved = func() {} })
	_, err := Swap(context.Background(), f.j, f.plan(5, 1))
	if !errors.Is(err, ErrPutBackFailed) {
		t.Fatalf("error = %v, want ErrPutBackFailed", err)
	}
	rows, _ := f.j.Moving(context.Background())
	if len(rows) != 0 {
		t.Errorf("a row is still moving: %+v", rows)
	}
	if got := readFile(t, f.original); got != "STRANGER" {
		t.Errorf("the stranger at the name = %q, want it left alone", got)
	}
	if got := readFile(t, filepath.Join(f.lib, TrashDir, "movie.5.mkv")); got != origBytes {
		t.Errorf("the original in the trash = %q, want it kept", got)
	}
}

// journalRow writes a moving row for the fixture's original and staged files, as a crash after step 4 would leave it.
func journalRow(t *testing.T, f swapFixture, jobID int64, trash string) Row {
	t.Helper()
	orig, err := identify(f.original)
	if err != nil {
		t.Fatal(err)
	}
	neu, err := identify(f.staged)
	if err != nil {
		t.Fatal(err)
	}
	r := Row{JobID: jobID, Original: f.original, Trash: trash, Staged: f.staged, Final: f.final, Orig: orig, New: neu}
	id, err := f.j.Begin(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	r.ID = id
	return r
}

// TestRecoveryNeedsAttentionWhenBothAreGone: neither file can be shown, so the row is attention and never purged.
func TestRecoveryNeedsAttentionWhenBothAreGone(t *testing.T) {
	f := newSwapFixture(t, "")
	r := journalRow(t, f, 9, filepath.Join(f.lib, TrashDir, "movie.9.mkv"))
	if err := os.Remove(f.original); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.staged); err != nil {
		t.Fatal(err)
	}
	rep, err := Recover(context.Background(), f.j)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Attention != 1 {
		t.Errorf("report = %+v, want one attention", rep)
	}
	var state string
	if err := f.j.db.Read().QueryRow(`SELECT state FROM replace_journal WHERE id = ?`, r.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != StateAttention {
		t.Errorf("state = %q, want attention (never purged)", state)
	}
}

// TestRecoveryLeavesAStrangerAtTheName: a stranger's file at the original's name is left alone; the original waits in
// the trash and the row is attention.
func TestRecoveryLeavesAStrangerAtTheName(t *testing.T) {
	f := newSwapFixture(t, "")
	trash := filepath.Join(f.lib, TrashDir, "movie.10.mkv")
	if err := os.Mkdir(filepath.Dir(trash), 0o755); err != nil {
		t.Fatal(err)
	}
	journalRow(t, f, 10, trash)
	if err := os.Rename(f.original, trash); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.original, []byte("STRANGER"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(context.Background(), f.j); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, f.original); got != "STRANGER" {
		t.Errorf("the stranger = %q, want it left alone", got)
	}
	if got := readFile(t, trash); got != origBytes {
		t.Errorf("the original in the trash = %q, want it still there", got)
	}
}

// TestRecoveryChecksTheOriginalsTime: a file at the name that is not the original by its mtime is not taken as the
// original, so recovery does not put it back as one.
func TestRecoveryChecksTheOriginalsTime(t *testing.T) {
	f := newSwapFixture(t, "")
	trash := filepath.Join(f.lib, TrashDir, "movie.11.mkv")
	if err := os.Mkdir(filepath.Dir(trash), 0o755); err != nil {
		t.Fatal(err)
	}
	journalRow(t, f, 11, trash)
	if err := os.Rename(f.original, trash); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.original, []byte(origBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(f.original, old, old); err != nil {
		t.Fatal(err)
	}
	rep, err := Recover(context.Background(), f.j)
	if err != nil {
		t.Fatal(err)
	}
	if rep.RolledBack != 0 || rep.Attention != 1 {
		t.Errorf("report = %+v, want the look-alike at the name left alone for a person", rep)
	}
}

// TestRecoveryNeverOverwritesTheOldName: a new file with a different extension is placed under its own name; when the
// swap reached that point, recovery settles it as done and does not put the original back over the old name.
func TestRecoveryNeverOverwritesTheOldName(t *testing.T) {
	f := newSwapFixture(t, "")
	f.final = filepath.Join(f.lib, "movie.mp4")
	trash := filepath.Join(f.lib, TrashDir, "movie.12.mkv")
	if err := os.Mkdir(filepath.Dir(trash), 0o755); err != nil {
		t.Fatal(err)
	}
	journalRow(t, f, 12, trash)
	if err := os.Rename(f.original, trash); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.staged, f.final); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(context.Background(), f.j); err != nil {
		t.Fatal(err)
	}
	if exists(f.original) {
		t.Errorf("recovery put the original back over the old name %s", f.original)
	}
	if got := readFile(t, f.final); got != newBytes {
		t.Errorf("new file = %q, want it at its name", got)
	}
}

// crashSteps is the number of numbered steps in a swap; a kill after each one is a case.
const crashSteps = 7

// TestCrashAtEveryStepRecovers is the kill test (L-SAFE-8): the swap runs in a child process that writes a step marker
// at each crash point and then waits. The parent reads the marker for the step it wants and kills the child. The
// marker, not the kill's exit status, is what the test checks, so it reads the same on every OS (review #865). Recovery
// then leaves the original and the new file each in one valid place: never neither, never overwritten.
func TestCrashAtEveryStepRecovers(t *testing.T) {
	if os.Getenv(crashChildEnv) != "" {
		t.Skip("this is the child process")
	}
	for step := 1; step <= crashSteps; step++ {
		t.Run("kill after step "+strconv.Itoa(step), func(t *testing.T) {
			f := newSwapFixture(t, "")
			mark := filepath.Join(t.TempDir(), "step")
			cmd := exec.Command(os.Args[0], "-test.run=^TestSwapCrashChild$")
			cmd.Env = append(os.Environ(),
				crashChildEnv+"=1",
				"SYP_CRASH_STEP="+strconv.Itoa(step),
				"SYP_CRASH_MARK="+mark,
				"SYP_CRASH_DB="+f.dbDir,
				"SYP_CRASH_ORIGINAL="+f.original,
				"SYP_CRASH_STAGED="+f.staged,
				"SYP_CRASH_FINAL="+f.final,
			)
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			// The child opens the same store, so the parent lets go of it first (the store is one writer).
			_ = f.j.db.Close()
			if err := cmd.Start(); err != nil {
				t.Fatalf("start the child: %v", err)
			}
			reached := waitForMarker(mark, strconv.Itoa(step), 60*time.Second)
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			if !reached {
				t.Fatalf("the child did not reach step %d:\n%s", step, out.String())
			}

			db := openStore(t, f.dbDir)
			defer db.Close()
			if _, err := Recover(context.Background(), NewJournal(db)); err != nil {
				t.Fatalf("recover: %v", err)
			}
			assertOneOfEach(t, f, step)
		})
	}
}

// waitForMarker polls the marker file until it holds want, or the time runs out.
func waitForMarker(path, want string, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && string(b) == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// assertOneOfEach checks the two files after recovery: the original is in one of its two places and the new file in
// one of its two, and the pair is one of the two states a swap can settle in.
func assertOneOfEach(t *testing.T, f swapFixture, step int) {
	t.Helper()
	trash := filepath.Join(f.lib, TrashDir, "movie.1.mkv")
	matchesDir, _ := filepath.Glob(filepath.Join(f.lib, TrashDir, "*"))
	for _, m := range matchesDir {
		if readFile(t, m) == origBytes {
			trash = m
		}
	}
	origAtName := readFile(t, f.original) == origBytes
	origInTrash := readFile(t, trash) == origBytes
	newAtFinal := readFile(t, f.final) == newBytes
	newStaged := readFile(t, f.staged) == newBytes
	if origAtName == origInTrash {
		t.Fatalf("step %d: the original is at its name=%v and in the trash=%v; want exactly one", step, origAtName, origInTrash)
	}
	if newAtFinal == newStaged {
		t.Fatalf("step %d: the new file is at its name=%v and staged=%v; want exactly one", step, newAtFinal, newStaged)
	}
	rolledBack := origAtName && newStaged
	settled := origInTrash && newAtFinal
	if !rolledBack && !settled {
		t.Fatalf("step %d: the pair is neither rolled back nor settled: original at name=%v trash=%v, new at name=%v staged=%v",
			step, origAtName, origInTrash, newAtFinal, newStaged)
	}
}

// crashChildEnv marks the child process of TestCrashAtEveryStepRecovers.
const crashChildEnv = "SYP_SWAP_CRASH_CHILD"

// TestSwapCrashChild is the child: it runs one swap and is killed after the step it was given. It runs only in the
// child process.
func TestSwapCrashChild(t *testing.T) {
	if os.Getenv(crashChildEnv) == "" {
		t.Skip("runs only as the child of TestCrashAtEveryStepRecovers")
	}
	step, err := strconv.Atoi(os.Getenv("SYP_CRASH_STEP"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(os.Getenv("SYP_CRASH_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	crashHook = func(s int) {
		if s == step {
			if err := os.WriteFile(os.Getenv("SYP_CRASH_MARK"), []byte(strconv.Itoa(s)), 0o644); err != nil {
				os.Exit(3)
			}
			time.Sleep(time.Minute) // the parent kills this process while it waits here
			os.Exit(4)
		}
	}
	defer func() { crashHook = nil }()
	plan := SwapPlan{JobID: 99, Original: os.Getenv("SYP_CRASH_ORIGINAL"), Staged: os.Getenv("SYP_CRASH_STAGED"),
		Final: os.Getenv("SYP_CRASH_FINAL"), KeepDays: 1}
	_, err = Swap(context.Background(), NewJournal(db), plan)
	fmt.Fprintf(os.Stderr, "swap returned %v before the kill at step %d\n", err, step)
}

// TestAStrangerAtTheNameIsNotLost: a file put at the original's name in the window between the source check and the
// move is not the original. It is moved, identified as a stranger after the move, put back with a no-replace rename,
// and the row waits for a person; nothing is replaced or lost (review #865).
func TestAStrangerAtTheNameIsNotLost(t *testing.T) {
	f := newSwapFixture(t, "")
	aside := f.original + ".aside"
	hookBeforeOriginalMove = func() {
		// Our original leaves the name, and a stranger takes it, both in the window the check does not cover.
		if err := os.Rename(f.original, aside); err != nil {
			t.Fatalf("move the original aside in the test: %v", err)
		}
		if err := os.WriteFile(f.original, []byte("STRANGER"), 0o644); err != nil {
			t.Fatalf("write the stranger in the test: %v", err)
		}
	}
	t.Cleanup(func() { hookBeforeOriginalMove = func() {} })
	_, err := Swap(context.Background(), f.j, f.plan(13, 1))
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("error = %v, want ErrSourceChanged for a stranger at the name", err)
	}
	if got := readFile(t, f.original); got != "STRANGER" {
		t.Errorf("the stranger at the name = %q, want it put back and kept", got)
	}
	if got := readFile(t, aside); got != origBytes {
		t.Errorf("the original set aside = %q, want it untouched", got)
	}
	if _, err := os.Lstat(filepath.Join(f.lib, TrashDir, "movie.13.mkv")); !os.IsNotExist(err) {
		t.Errorf("the stranger is still in the trash: %v", err)
	}
	rows, _ := f.j.Moving(context.Background())
	if len(rows) != 0 {
		t.Errorf("a row is still moving: %+v", rows)
	}
}
