// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"os"
	"path/filepath"
	"testing"
)

// IsOwnOutput: the file a done job wrote, as it wrote it; a different file at that path later (another
// size or time), a job not done, or an unknown time (0) is not ours.
func TestIsOwnOutput(t *testing.T) {
	s := open(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "a.mkv")
	if err := os.WriteFile(out, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(out)
	id, _ := s.Enqueue(Job{Library: dir, Path: filepath.Join(dir, "a.mp4")})
	if own, _ := s.IsOwnOutput(out, fi.Size(), fi.ModTime().UnixNano()); own {
		t.Error("ours before the job is done")
	}
	s.Claim("w")
	if err := s.Complete(id, out, fi.Size()); err != nil {
		t.Fatal(err)
	}
	if own, err := s.IsOwnOutput(out, fi.Size(), fi.ModTime().UnixNano()); !own || err != nil {
		t.Errorf("the job's output: %v %v", own, err)
	}
	if own, _ := s.IsOwnOutput(out, fi.Size()+1, fi.ModTime().UnixNano()); own {
		t.Error("another size is ours")
	}
	if own, _ := s.IsOwnOutput(out, fi.Size(), fi.ModTime().UnixNano()+1); own {
		t.Error("another time is ours")
	}
}
