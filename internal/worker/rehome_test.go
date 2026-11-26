// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// U-B: a share re-added at another mount. keptOne's library folder (s.dir) is moved to a new mount, and a library
// row names the new folder; the kept row still names the old one, as a re-add leaves it.
func movedShare(t *testing.T) (*scenario, jobs.Original, string) {
	t.Helper()
	s, o := keptOne(t)
	newRoot := filepath.Join(t.TempDir(), "mnt", "ssd", "Shows", "Arcane")
	if err := os.MkdirAll(filepath.Dir(newRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(s.dir, newRoot); err != nil {
		t.Fatal(err)
	}
	// macOS: t.TempDir is under /var, a link to /private/var. The library row stores the resolved folder (jobs.RealPath
	// through addLibrary's canonical), so the test names the new root the same way.
	newRoot, err := jobs.RealPath(newRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.AddLibraryWithoutFlow("Arcane", newRoot, false); err != nil {
		t.Fatal(err)
	}
	return s, o, newRoot
}

// later is a time after the kept row's days are up (days 0 and 7 both judge it).
func later(t *testing.T, o jobs.Original) time.Time {
	t.Helper()
	kept, err := time.Parse(time.RFC3339, o.KeptAt)
	if err != nil {
		t.Fatal(err)
	}
	return kept.Add(8 * 24 * time.Hour)
}

// Empty trash after a share re-add: the moved original is proved, re-homed and deleted, and the row names the new place.
func TestEmptyTrashFindsAMovedOriginal(t *testing.T) {
	s, o, newRoot := movedShare(t)
	movedTrash := filepath.Join(newRoot, TrashDir, filepath.Base(o.Trash))
	if _, err := os.Stat(movedTrash); err != nil {
		t.Fatalf("the original is not where the share moved it: %v", err)
	}
	rep := Purge(s.store, 0, later(t, o), nil, 0)
	if rep.Deleted != 1 {
		t.Fatalf("deleted %d, want 1; said %q; unreachable %+v", rep.Deleted, rep.Said, rep.Unreachable)
	}
	if _, err := os.Stat(movedTrash); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the moved original is still there: %v", err)
	}
	r, _ := s.store.OriginalByID(o.ID)
	if r.State != jobs.OriginalPurged {
		t.Errorf("row %s, want purged", r.State)
	}
	if len(rep.Unreachable) != 0 {
		t.Errorf("nothing should be unreachable: %+v", rep.Unreachable)
	}
}

// Re-homing alone is not deletion: before the days are up the row names the new place and the files stay.
func TestMovedRowIsRehomedButKeptUntilItsDay(t *testing.T) {
	s, o, newRoot := movedShare(t)
	kept, _ := time.Parse(time.RFC3339, o.KeptAt)
	rep := Purge(s.store, 7, kept.Add(24*time.Hour), nil, 0)
	if rep.Deleted != 0 {
		t.Fatalf("deleted %d before its day", rep.Deleted)
	}
	r, _ := s.store.OriginalByID(o.ID)
	if r.State != jobs.OriginalKept || r.Path != filepath.Join(newRoot, filepath.Base(o.Path)) {
		t.Errorf("row %s, path %s: want kept at the new place", r.State, r.Path)
	}
	if _, err := os.Stat(filepath.Join(newRoot, TrashDir, filepath.Base(o.Trash))); err != nil {
		t.Errorf("the moved original was touched before its day: %v", err)
	}
}

// A file of the same size at the moved name is not our original: nothing is re-homed or deleted.
func TestSameSizeImpostorIsNotRehomedOrDeleted(t *testing.T) {
	s, o, newRoot := movedShare(t)
	impostor := filepath.Join(newRoot, TrashDir, filepath.Base(o.Trash))
	if err := os.WriteFile(impostor, make([]byte, o.Bytes), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(impostor, time.Unix(0, o.ModTime), time.Unix(0, o.ModTime)); err != nil {
		t.Fatal(err)
	}
	rep := Purge(s.store, 0, later(t, o), nil, 0)
	if rep.Deleted != 0 {
		t.Fatalf("deleted %d: an impostor must not be deleted", rep.Deleted)
	}
	if len(rep.Unreachable) != 1 || rep.Unreachable[0].Code != CodeChanged {
		t.Errorf("unreachable %+v, want one %s", rep.Unreachable, CodeChanged)
	}
	if _, err := os.Stat(impostor); err != nil {
		t.Errorf("the impostor was touched: %v", err)
	}
	if r, _ := s.store.OriginalByID(o.ID); r.State != jobs.OriginalKept {
		t.Errorf("row %s, want kept", r.State)
	}
}

// The original is proved, but its converted file is not beside it: nothing is deleted.
func TestConvertedMissingIsNotDeleted(t *testing.T) {
	s, o, newRoot := movedShare(t)
	if err := os.Remove(filepath.Join(newRoot, filepath.Base(o.Dest()))); err != nil {
		t.Fatal(err)
	}
	rep := Purge(s.store, 0, later(t, o), nil, 0)
	if rep.Deleted != 0 {
		t.Fatalf("deleted %d without the converted file", rep.Deleted)
	}
	if len(rep.Unreachable) != 1 || rep.Unreachable[0].Code != CodeNotFound {
		t.Errorf("unreachable %+v, want one %s", rep.Unreachable, CodeNotFound)
	}
	if _, err := os.Stat(filepath.Join(newRoot, TrashDir, filepath.Base(o.Trash))); err != nil {
		t.Errorf("the original was deleted: %v", err)
	}
}

// Two places are proved ours: nothing is chosen, and both stay.
func TestTwoProvedPlacesAreAmbiguous(t *testing.T) {
	s, o, newRoot := movedShare(t)
	other := filepath.Join(t.TempDir(), "other")
	copyProved(t, s, o, newRoot, other)
	if _, err := s.store.AddLibraryWithoutFlow("Copy", other, false); err != nil {
		t.Fatal(err)
	}
	rep := Purge(s.store, 0, later(t, o), nil, 0)
	if rep.Deleted != 0 {
		t.Fatalf("deleted %d with two places", rep.Deleted)
	}
	if len(rep.Unreachable) != 1 || rep.Unreachable[0].Code != CodeAmbiguous {
		t.Errorf("unreachable %+v, want one %s", rep.Unreachable, CodeAmbiguous)
	}
	if _, err := os.Stat(filepath.Join(newRoot, TrashDir, filepath.Base(o.Trash))); err != nil {
		t.Errorf("the original was touched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(other, TrashDir, filepath.Base(o.Trash))); err != nil {
		t.Errorf("the copy was touched: %v", err)
	}
}

// copyProved copies the moved original and its converted file to dir, with the recorded times, so the copy
// passes the same proof as the first place.
func copyProved(t *testing.T, s *scenario, o jobs.Original, from, dir string) {
	t.Helper()
	pairs := [][2]string{
		{filepath.Join(from, TrashDir, filepath.Base(o.Trash)), filepath.Join(dir, TrashDir, filepath.Base(o.Trash))},
		{filepath.Join(from, filepath.Base(o.Dest())), filepath.Join(dir, filepath.Base(o.Dest()))},
	}
	for _, p := range pairs {
		if err := os.MkdirAll(filepath.Dir(p[1]), 0o755); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(p[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p[1], b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.Chtimes(pairs[0][1], time.Unix(0, o.ModTime), time.Unix(0, o.ModTime))
	os.Chtimes(pairs[1][1], time.Unix(0, o.NewMTime), time.Unix(0, o.NewMTime))
}

// Forget: a file still found is a refusal and changes nothing.
func TestForgetRefusesWhileTheFileIsFound(t *testing.T) {
	s, o := keptOne(t)
	err := Forget(s.store, o.ID, time.Now())
	var fe *ForgetError
	if !errors.As(err, &fe) || fe.Code != "file_found" {
		t.Fatalf("forget with the file present: %v", err)
	}
	if r, _ := s.store.OriginalByID(o.ID); r.State != jobs.OriginalKept {
		t.Errorf("row %s, want kept", r.State)
	}
}

// Forget: a file gone for good (and not found under any library or mount) is marked gone; nothing is deleted.
func TestForgetMarksAGoneOriginal(t *testing.T) {
	s, o := keptOne(t)
	if err := os.RemoveAll(filepath.Dir(s.trash())); err != nil {
		t.Fatal(err)
	}
	if err := Forget(s.store, o.ID, time.Now()); err != nil {
		t.Fatalf("forget a gone original: %v", err)
	}
	if r, _ := s.store.OriginalByID(o.ID); r.State != jobs.OriginalPurged {
		t.Errorf("row %s, want purged (gone)", r.State)
	}
	err := Forget(s.store, o.ID, time.Now())
	var fe *ForgetError
	if !errors.As(err, &fe) || fe.Code != "not_kept" {
		t.Errorf("a second forget: %v, want not_kept", err)
	}
}

// U-B live shape (CT 124): the library keeps its old path, the old root is gone, and only share_mounts names the
// new root. The purge must find the original through MountRoots, re-home it, and delete it once its day is up.
func TestEmptyTrashFindsAMovedOriginalByMountOnly(t *testing.T) {
	s, o := keptOne(t)
	newRoot := filepath.Join(t.TempDir(), "mnt", "ssd", "Shows", "Arcane")
	if err := os.MkdirAll(filepath.Dir(newRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(s.dir, newRoot); err != nil {
		t.Fatal(err)
	}
	sh, err := s.store.SaveShare(jobs.Share{Type: "smb", Address: "//h/x", Name: "ssd", Guest: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetShareMountStatus(sh.ID, "linux", "connected", ""); err != nil {
		t.Fatal(err)
	}
	// The store has no setter for mount_path; write it the way the share daemon records it.
	db, err := sql.Open("sqlite", s.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE share_mounts SET mount_path=? WHERE share_id=?`, newRoot, sh.ID); err != nil {
		t.Fatal(err)
	}
	movedTrash := filepath.Join(newRoot, TrashDir, filepath.Base(o.Trash))
	rep := Purge(s.store, 0, later(t, o), nil, 0)
	if rep.Deleted != 1 {
		t.Fatalf("deleted %d, want 1; said %q; unreachable %+v", rep.Deleted, rep.Said, rep.Unreachable)
	}
	if _, err := os.Stat(movedTrash); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the moved original is still there: %v", err)
	}
	r, _ := s.store.OriginalByID(o.ID)
	if r.State != jobs.OriginalPurged {
		t.Errorf("row %s, want purged", r.State)
	}
}

// U-B part 2 (CT 124): the library was deleted, so the only record of the new root is the share's mount point; the
// daemon has recorded no path. The purge must still find the moved original through MountRoots, re-home it, and delete it.
func TestEmptyTrashFindsAMovedOriginalByShareMountPointOnly(t *testing.T) {
	s, o := keptOne(t)
	libs, err := s.store.Libraries()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range libs {
		if err := s.store.DeleteLibrary(l.ID); err != nil {
			t.Fatal(err)
		}
	}
	newRoot := filepath.Join(t.TempDir(), "mnt", "ssd", "Shows", "Arcane")
	if err := os.MkdirAll(filepath.Dir(newRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(s.dir, newRoot); err != nil {
		t.Fatal(err)
	}
	newRoot, err = jobs.RealPath(newRoot)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := s.store.SaveShare(jobs.Share{Type: "smb", Address: "//h/x", Name: "ssd", Guest: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetShareMountPoint(sh.ID, newRoot); err != nil {
		t.Fatal(err)
	}
	movedTrash := filepath.Join(newRoot, TrashDir, filepath.Base(o.Trash))
	rep := Purge(s.store, 0, later(t, o), nil, 0)
	if rep.Deleted != 1 {
		t.Fatalf("deleted %d, want 1; said %q; unreachable %+v", rep.Deleted, rep.Said, rep.Unreachable)
	}
	if _, err := os.Stat(movedTrash); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the moved original is still there: %v", err)
	}
	r, _ := s.store.OriginalByID(o.ID)
	if r.State != jobs.OriginalPurged || !strings.HasPrefix(r.Path, newRoot) {
		t.Errorf("row %s at %s, want purged under %s", r.State, r.Path, newRoot)
	}
}

// Nested shape: the original's old folder ends in Shows/Arcane (so its trash path has that suffix), and the library
// is the parent of the share's new mount point, which is that same folder name under the library. The library root
// and the mount root both produce one candidate, Lib/Shows/Arcane/.syp-trash/x: the seen[] dedupe must keep it to one
// match, or the original is reported ambiguous. This fails with the dedupe disabled (review of #730).
func TestEmptyTrashFindsAMovedOriginalUnderItsOwnLibraryFolder(t *testing.T) {
	oldRoot := filepath.Join(t.TempDir(), "old", "Shows", "Arcane")
	if err := os.MkdirAll(oldRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	s := newScenarioAt(t, oldRoot, "movie.mkv")
	if _, err := s.keeping(7).RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	o, err := s.store.OriginalOfJob(s.id)
	if err != nil {
		t.Fatal(err)
	}
	libs, err := s.store.Libraries()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range libs {
		if err := s.store.DeleteLibrary(l.ID); err != nil {
			t.Fatal(err)
		}
	}
	lib, err := jobs.RealPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.AddLibraryWithoutFlow("Shows", lib, false); err != nil {
		t.Fatal(err)
	}
	newRoot := filepath.Join(lib, "Shows", "Arcane")
	if err := os.MkdirAll(filepath.Dir(newRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldRoot, newRoot); err != nil {
		t.Fatal(err)
	}
	newRoot, err = jobs.RealPath(newRoot)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := s.store.SaveShare(jobs.Share{Type: "smb", Address: "//h/x", Name: "ssd", Guest: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetShareMountPoint(sh.ID, newRoot); err != nil {
		t.Fatal(err)
	}
	movedTrash := filepath.Join(newRoot, TrashDir, filepath.Base(o.Trash))
	rep := Purge(s.store, 0, later(t, o), nil, 0)
	if rep.Deleted != 1 {
		t.Fatalf("deleted %d, want 1; said %q; unreachable %+v", rep.Deleted, rep.Said, rep.Unreachable)
	}
	if len(rep.Unreachable) != 0 {
		t.Errorf("unreachable %+v, want none: one place is ours, not ambiguous", rep.Unreachable)
	}
	if _, err := os.Stat(movedTrash); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the moved original is still there: %v", err)
	}
	if r, _ := s.store.OriginalByID(o.ID); r.State != jobs.OriginalPurged || !strings.HasPrefix(r.Path, newRoot) {
		t.Errorf("row %s at %s, want purged under %s", r.State, r.Path, newRoot)
	}
}

func TestForgetAnUnknownIDIsNotFound(t *testing.T) {
	s, _ := keptOne(t)
	if err := Forget(s.store, 999999, time.Now()); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("unknown id: %v, want ErrNotFound", err)
	}
}

// U-B: only a kept row is re-homed. A purged row is left as it was, and the write reports that it moved nothing.
func TestRehomeLeavesAPurgedRowAlone(t *testing.T) {
	s, o, newRoot := movedShare(t)
	if err := s.store.SetKeptGone(o.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	err := s.store.RehomeOriginal(o.ID, filepath.Join(newRoot, filepath.Base(o.Path)), filepath.Join(newRoot, filepath.Base(o.NewPath)), filepath.Join(newRoot, TrashDir, filepath.Base(o.Trash)))
	if !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("rehome of a purged row: err %v, want ErrNotFound", err)
	}
	r, _ := s.store.OriginalByID(o.ID)
	if r.Path != o.Path || r.State != jobs.OriginalPurged {
		t.Errorf("purged row changed: path %s state %s", r.Path, r.State)
	}
}
