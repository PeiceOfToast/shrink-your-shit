// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// TestFinishSidecarsKeepsNames is L-AUD-25's finished job: the job keeps the names of the .srt
// files it saved, and their records go.
func TestFinishSidecarsKeepsNames(t *testing.T) {
	e := newEnv(t)
	e.addLibrary(t, 1, false)
	id := e.enqueue(t, 1, 1)
	j := SidecarJournal{DB: e.db}
	recs := []safety.Sidecar{
		{JobID: id, Path: "/media/movie.eng.srt", Bytes: 10, MTime: 5, Hash: "h1",
			VideoPath: "/media/movie-shrunk.mkv", VideoBytes: 100, VideoHash: "v"},
		{JobID: id, Path: "/media/movie.ger.srt", Bytes: 11, MTime: 6, Hash: "h2",
			VideoPath: "/media/movie-shrunk.mkv", VideoBytes: 100, VideoHash: "v"},
	}
	if err := j.AddSidecars(recs); err != nil {
		t.Fatal(err)
	}
	left, err := j.Sidecars()
	if err != nil || len(left) != 2 || left[0].Path != recs[0].Path || left[1].Hash != "h2" {
		t.Fatalf("records = %+v, %v, want the two added", left, err)
	}
	if err := j.FinishSidecars(id); err != nil {
		t.Fatal(err)
	}
	names, err := j.SubtitleFiles(id)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"movie.eng.srt", "movie.ger.srt"}; !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
	if left, _ := j.Sidecars(); len(left) != 0 {
		t.Errorf("records left after the place: %+v", left)
	}

	// A later run that saved none clears the earlier list.
	if err := j.FinishSidecars(id); err != nil {
		t.Fatal(err)
	}
	if names, _ := j.SubtitleFiles(id); len(names) != 0 {
		t.Errorf("names = %v, want none after a run that saved none", names)
	}
}

// TestDropSidecarForgetsOne: a file that was never placed, or is not ours, leaves no record.
func TestDropSidecarForgetsOne(t *testing.T) {
	e := newEnv(t)
	e.addLibrary(t, 1, false)
	id := e.enqueue(t, 1, 1)
	j := SidecarJournal{DB: e.db}
	a, b := filepath.Join("/media", "a.srt"), filepath.Join("/media", "b.srt")
	if err := j.AddSidecars([]safety.Sidecar{
		{JobID: id, Path: a, Hash: "h"},
		{JobID: id, Path: b, Hash: "h"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := j.DropSidecar(id, a); err != nil {
		t.Fatal(err)
	}
	left, _ := j.Sidecars()
	if len(left) != 1 || left[0].Path != b {
		t.Errorf("records = %+v, want only b.srt", left)
	}
}

// TestSubtitleFilesForAJobWithoutThem: a job from before the names were kept, or one with none, is
// an empty list and not an error.
func TestSubtitleFilesForAJobWithoutThem(t *testing.T) {
	e := newEnv(t)
	e.addLibrary(t, 1, false)
	id := e.enqueue(t, 1, 1)
	names, err := SidecarJournal{DB: e.db}.SubtitleFiles(id)
	if err != nil || len(names) != 0 {
		t.Errorf("names = %v, %v, want none", names, err)
	}
}
