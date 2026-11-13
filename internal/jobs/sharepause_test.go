// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// D4b: a library paused by its share (an outage) takes no job until the share answers; the person's own
// pause (started) is a separate thing and survives an outage and its recovery.
func TestSharePauseStopsClaimsUntilResumed(t *testing.T) {
	s := open(t)
	s.SetPaused(false)
	mount := t.TempDir()
	nas, _ := s.AddLibrary("NAS", filepath.Join(mount, "Movies"), rules.SliderTemplate(3), true)
	other, _ := s.AddLibrary("Local", t.TempDir(), rules.SliderTemplate(3), true)
	job, _ := s.Enqueue(Job{Library: nas.Path, LibraryID: nas.ID, Path: filepath.Join(nas.Path, "a.mkv")})
	localJob, _ := s.Enqueue(Job{Library: other.Path, LibraryID: other.ID, Path: filepath.Join(other.Path, "b.mkv")})

	paused, err := s.PauseLibrariesForShare(7, mount)
	if err != nil || len(paused) != 1 || paused[0] != nas.ID {
		t.Fatalf("paused %v %v, want only NAS %d", paused, err, nas.ID)
	}
	if l, _ := s.GetLibrary(nas.ID); l.SharePausedBy != 7 || l.SharePausedSince == "" || !l.Started {
		t.Fatalf("share pause not recorded, or the person's start changed: %+v", l)
	}
	if j, err := s.Claim("w"); err != nil || j.ID != localJob {
		t.Fatalf("claimed %d %v, want the local library's %d", j.ID, err, localJob)
	}
	if _, err := s.Claim("w"); err != ErrNoJob {
		t.Errorf("a share-paused library's job was taken: %v", err)
	}

	if ids, err := s.ResumeSharePause(7); err != nil || len(ids) != 1 {
		t.Fatalf("resume %v %v", ids, err)
	}
	if j, err := s.Claim("w"); err != nil || j.ID != job {
		t.Errorf("after the share answers: %d %v, want %d", j.ID, err, job)
	}
}

// The person's own pause is never changed by a share: after an outage and its recovery the library is
// still paused by the person.
func TestSharePauseLeavesThePersonsPause(t *testing.T) {
	s := open(t)
	s.SetPaused(false)
	mount := t.TempDir()
	nas, _ := s.AddLibrary("NAS", filepath.Join(mount, "Movies"), rules.SliderTemplate(3), true)
	if err := s.StopLibrary(nas.ID); err != nil {
		t.Fatal(err)
	}
	s.PauseLibrariesForShare(7, mount)
	s.ResumeSharePause(7)
	if l, _ := s.GetLibrary(nas.ID); l.Started || l.SharePausedBy != 0 {
		t.Errorf("after an outage and recovery: started=%v sharePausedBy=%d, want paused by the person only", l.Started, l.SharePausedBy)
	}
}

// Deleting a share does not leave its libraries paused by nothing.
func TestDeleteShareClearsItsPause(t *testing.T) {
	s := open(t)
	mount := t.TempDir()
	nas, _ := s.AddLibrary("NAS", filepath.Join(mount, "Movies"), rules.SliderTemplate(3), true)
	s.PauseLibrariesForShare(7, mount)
	if err := s.DeleteShare(7); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.GetLibrary(nas.ID); l.SharePausedBy != 0 {
		t.Errorf("still share-paused by %d after the share was deleted", l.SharePausedBy)
	}
}

// D4c: a running job whose share stopped answering goes back in the queue (share_dropped, no try counted), and
// its library is paused by the share at once.
func TestShareDroppedRequeuesAndPausesTheLibrary(t *testing.T) {
	s := open(t)
	s.SetPaused(false)
	mount := t.TempDir()
	nas, _ := s.AddLibrary("NAS", filepath.Join(mount, "Movies"), rules.SliderTemplate(3), true)
	job, _ := s.Enqueue(Job{Library: nas.Path, LibraryID: nas.ID, Path: filepath.Join(nas.Path, "a.mkv")})
	if _, err := s.Claim("w"); err != nil {
		t.Fatal(err)
	}
	if err := s.ShareDropped(job, 7, mount, "Put back in the queue: NAS stopped answering."); err != nil {
		t.Fatal(err)
	}
	j, _ := s.Get(job)
	var code string
	s.db.QueryRow(`SELECT error_code FROM jobs WHERE id=?`, job).Scan(&code)
	if j.Status != Queued || code != ReasonShareDropped || j.Attempts != 0 {
		t.Errorf("want queued with share_dropped and no try counted, got %s %+v", code, j)
	}
	if l, _ := s.GetLibrary(nas.ID); l.SharePausedBy != 7 {
		t.Errorf("library not paused by its share: %+v", l)
	}
	if _, err := s.Claim("w"); err != ErrNoJob {
		t.Errorf("the file was taken again while its share is down: %v", err)
	}
}
