// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// purgeStore is a job store with nothing in it, running, and closed at the end of the test.
func purgeStore(t *testing.T) *jobs.Store {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetPaused(false) // a new store starts paused; the jobs here must be claimable
	t.Cleanup(func() { st.Close() })
	return st
}

// purgeLibrary adds a library whose folder is a t.TempDir() path.
func purgeLibrary(t *testing.T, st *jobs.Store, name string) jobs.Library {
	t.Helper()
	l, err := st.AddLibrary(name, t.TempDir(), rules.SliderTemplate(4), true)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// keptIn makes name in library l a finished job whose original (data) is kept in .syp-trash, the way a swap
// leaves it. It returns the trash path.
func keptIn(t *testing.T, st *jobs.Store, l jobs.Library, name, data string) string {
	t.Helper()
	path := filepath.Join(l.Path, name)
	if err := os.WriteFile(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	trash := filepath.Join(l.Path, ".syp-trash", name)
	if err := os.MkdirAll(filepath.Dir(trash), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trash, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(trash)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := SampleHash(trash)
	if err != nil {
		t.Fatal(err)
	}
	jid, err := st.Enqueue(jobs.Job{Library: l.Path, LibraryID: l.ID, Path: path, SizeBefore: fi.Size()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Claim("w"); err != nil {
		t.Fatal(err)
	}
	if err := st.Complete(jid, path, 3); err != nil {
		t.Fatal(err)
	}
	id, err := st.BeginSwap(jobs.Original{JobID: jid, LibraryID: l.ID, Path: path, Trash: trash, Bytes: fi.Size(), ModTime: fi.ModTime().UnixNano(), Hash: hash, NewBytes: 3, NewHash: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishSwap(id, time.Now()); err != nil {
		t.Fatal(err)
	}
	return trash
}

// purgeLater is a time past every kept original's days (7).
func purgeLater() time.Time { return time.Now().Add(30 * 24 * time.Hour) }

func present(p string) bool { _, err := os.Stat(p); return err == nil }

func TestEmptyTrashPurgesEveryLibrary(t *testing.T) {
	st := purgeStore(t)
	a, b := purgeLibrary(t, st, "A"), purgeLibrary(t, st, "B")
	ta := keptIn(t, st, a, "a1.mkv", "aaaa")
	tb := keptIn(t, st, b, "b1.mkv", "bbbbbb")

	rep := Purge(st, 7, purgeLater(), nil, 0)
	if rep.Deleted != 2 || rep.FreedBytes != 10 {
		t.Fatalf("deleted %d freed %d, want 2 and 10 (%q)", rep.Deleted, rep.FreedBytes, rep.Said)
	}
	if rep.Libraries[a.ID].Deleted != 1 || rep.Libraries[a.ID].FreedBytes != 4 || rep.Libraries[b.ID].FreedBytes != 6 {
		t.Errorf("per library %+v", rep.Libraries)
	}
	if present(ta) || present(tb) {
		t.Error("a kept original is still in .syp-trash")
	}
}

func TestEmptyTrashOneLibraryLeavesTheOthers(t *testing.T) {
	st := purgeStore(t)
	a, b := purgeLibrary(t, st, "A"), purgeLibrary(t, st, "B")
	ta := keptIn(t, st, a, "a1.mkv", "aaaa")
	tb := keptIn(t, st, b, "b1.mkv", "bbbbbb")

	rep := Purge(st, 7, purgeLater(), nil, a.ID)
	if rep.Deleted != 1 || rep.FreedBytes != 4 {
		t.Fatalf("deleted %d freed %d, want 1 and 4", rep.Deleted, rep.FreedBytes)
	}
	if present(ta) {
		t.Error("the chosen library's kept original is still there")
	}
	if !present(tb) {
		t.Error("another library's kept original was deleted")
	}
}

// A library whose share is not connected is not touched: the waiting check keeps its originals.
func TestEmptyTrashLeavesAnUnmountedLibraryAlone(t *testing.T) {
	st := purgeStore(t)
	a, b := purgeLibrary(t, st, "A"), purgeLibrary(t, st, "B")
	ta := keptIn(t, st, a, "a1.mkv", "aaaa")
	tb := keptIn(t, st, b, "b1.mkv", "bbbbbb")
	unmounted := func(p string) bool { return filepath.Dir(p) == filepath.Clean(b.Path) }

	rep := Purge(st, 7, purgeLater(), unmounted, 0)
	if rep.Deleted != 1 || rep.Libraries[b.ID].Deleted != 0 {
		t.Fatalf("deleted %d, library B %+v; want only library A's one", rep.Deleted, rep.Libraries[b.ID])
	}
	if present(ta) || !present(tb) {
		t.Error("the unmounted library's original was touched, or the connected one kept")
	}
}

// A file a queued or running job uses keeps its kept original, even when it is due.
func TestEmptyTrashKeepsAFileAnActiveJobUses(t *testing.T) {
	st := purgeStore(t)
	a := purgeLibrary(t, st, "A")
	ta := keptIn(t, st, a, "a1.mkv", "aaaa")
	if _, err := st.Enqueue(jobs.Job{Library: a.Path, LibraryID: a.ID, Path: filepath.Join(a.Path, "a1.mkv"), SizeBefore: 4}); err != nil {
		t.Fatal(err)
	}

	rep := Purge(st, 7, purgeLater(), nil, 0)
	if rep.Deleted != 0 || !present(ta) {
		t.Fatalf("deleted %d, trash exists %v: the file an active job uses must be kept (%q)", rep.Deleted, present(ta), rep.Said)
	}
}
