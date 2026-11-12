// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Review 697332b P2: at start, the staging folders a killed server left in the folders for new files are
// removed (only ours: the exact name, holding only our temp file and subtitles); a folder with the same
// kind of name holding anything else, and other hidden folders, are left alone.
func TestSweepStaging(t *testing.T) {
	out := t.TempDir()
	mk := func(rel string, files ...string) string {
		dir := filepath.Join(out, rel)
		os.MkdirAll(dir, 0o755)
		for _, f := range files {
			os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)
		}
		return dir
	}
	ours := mk(filepath.Join("Action", ".Movie.123.syp-tmp"), "new.mkv", "sub.2.srt")
	ours2 := mk(".Other.7.syp-tmp") // empty: the server died before ffmpeg wrote
	foreign := mk(".Movie.5.syp-tmp", "notes.txt")
	hidden := mk(".config", "new.mkv")
	keep := filepath.Join(out, "Action", "Movie.mkv")
	os.WriteFile(keep, []byte("a finished file"), 0o644)
	said := SweepStaging([]string{out})
	for _, gone := range []string{ours, ours2} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s is still there", gone)
		}
	}
	for _, kept := range []string{foreign, hidden, keep} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was removed", kept)
		}
	}
	if len(said) != 2 {
		t.Errorf("said %v", said)
	}
}

// The folders swept: each output-first library's folder beside it, and one folder for all.
func TestOutputFolders(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	parent := t.TempDir()
	lib := filepath.Join(parent, "movies")
	os.Mkdir(lib, 0o755)
	sel := rules.DefaultSelectors(3)
	sel.AfterConverting, sel.FolderMode = "folder", rules.NextToLibrary
	l, _ := st.AddLibrary("Movies", lib, sel.Template(), true)
	st.AddLibrary("Replace", t.TempDir(), rules.SliderTemplate(3), true) // replace: no folder
	if got := OutputFolders(st); len(got) != 1 || got[0] != l.Path+" (shrunk)" {
		t.Errorf("%v", got)
	}
}
