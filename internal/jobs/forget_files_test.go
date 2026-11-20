// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A workflow change forgets the file records under the library, but keeps the record of a file whose newest job
// is done: it converted fine, so it is not converted again. A failed file loses its record and is decided again.
func TestWorkflowChangeKeepsRecordsOfConvertedFiles(t *testing.T) {
	s := open(t)
	root := realDir(t, filepath.Join(t.TempDir(), "media"))
	l, _ := s.AddLibrary("A", filepath.Join(root, "a"), rules.SliderTemplate(3), true)
	done, failed := filepath.Join(l.Path, "done.mkv"), filepath.Join(l.Path, "failed.mkv")
	s.PutFile(File{Path: done, Size: 1})
	s.PutFile(File{Path: failed, Size: 1})
	id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: done, SizeBefore: 1})
	s.Claim("w")
	if err := s.Complete(id, filepath.Join(l.Path+" (shrunk)", "done.mkv"), 1); err != nil {
		t.Fatal(err)
	}
	id, _ = s.Enqueue(Job{LibraryID: l.ID, Path: failed, SizeBefore: 1})
	s.Claim("w")
	if err := s.Fail(id, "convert_failed", "broke"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSlider(l.ID, 2, ""); err != nil {
		t.Fatal(err)
	}
	if f, _ := s.GetFile(done); f == nil {
		t.Error("a converted file lost its record: the next scan would queue it again")
	}
	if f, _ := s.GetFile(failed); f != nil {
		t.Error("a failed file keeps its record: it would not be decided again")
	}
}
