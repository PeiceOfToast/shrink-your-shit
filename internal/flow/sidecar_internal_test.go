// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// listStore is the sidecar record a run writes, in memory.
type listStore struct {
	recs []safety.Sidecar
}

func (l *listStore) AddSidecars(list []safety.Sidecar) error {
	l.recs = append(l.recs, list...)
	return nil
}
func (l *listStore) FinishSidecars(int64) error          { return nil }
func (l *listStore) Sidecars() ([]safety.Sidecar, error) { return l.recs, nil }

func (l *listStore) DropSidecar(jobID int64, path string) error {
	for i, c := range l.recs {
		if c.JobID == jobID && c.Path == path {
			l.recs = append(l.recs[:i], l.recs[i+1:]...)
			return nil
		}
	}
	return nil
}

// TestSidecarLinkedButStagedNameLeftCountsAsPlaced: the no-replace fallback links the .srt into
// place and then cannot remove the staged name. The file is in place and ours, so it counts as
// placed and keeps its record; the staging folder's cleanup removes the leftover name.
func TestSidecarLinkedButStagedNameLeftCountsAsPlaced(t *testing.T) {
	dir := t.TempDir()
	stage := filepath.Join(dir, ".movie.syp-tmp")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	stagedSrt := encode.StagedSidecar(stage, 0)
	if err := os.WriteFile(stagedSrt, []byte("subtitles"), 0o600); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(stage, "out.mkv")
	if err := os.WriteFile(video, []byte("the new file"), 0o600); err != nil {
		t.Fatal(err)
	}
	renameSidecar = func(oldpath, newpath string) error {
		if err := os.Link(oldpath, newpath); err != nil {
			return err
		}
		return &fsxErrNotRemoved{}
	}
	t.Cleanup(func() { renameSidecar = fsx.RenameNoReplace })

	store := &listStore{}
	e := &engine{in: RunInput{JobID: 7}, env: Env{Sidecars: store}}
	plan := encode.Plan{Output: filepath.Join(dir, "movie-shrunk.mkv"), SidecarDir: stage}
	extracts := []encode.StreamPlan{{Index: 3, Action: encode.StreamExtract, Part: "eng"}}
	placed, res := e.placeSidecars(plan, extracts, safety.Staged{Folder: stage, Path: video})
	if res.Outcome != "" {
		t.Fatalf("result = %+v, want the file counted as placed", res)
	}
	if len(placed) != 1 || len(store.recs) != 1 {
		t.Fatalf("placed = %+v records = %+v, want the .srt placed and recorded", placed, store.recs)
	}
	if _, err := os.Stat(encode.SrtName(plan.Output, "eng")); err != nil {
		t.Errorf("the linked .srt should be in place: %v", err)
	}
}

// fsxErrNotRemoved is what the link fallback returns when the staged name cannot be removed.
type fsxErrNotRemoved struct{}

func (*fsxErrNotRemoved) Error() string        { return "linked, staged name left" }
func (*fsxErrNotRemoved) Is(target error) bool { return target == fsx.ErrNotRemoved }
