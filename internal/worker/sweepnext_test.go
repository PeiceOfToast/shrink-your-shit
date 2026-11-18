// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"os"
	"path/filepath"
	"testing"
)

// #163 QA: a conversion killed mid-encode leaves its staging folder beside the original (a replace);
// at start it goes, but only ours: named for that file, holding only our temp file.
func TestSweepNextTo(t *testing.T) {
	dir := t.TempDir()
	movie := filepath.Join(dir, "Movie.mkv")
	other := filepath.Join(dir, "Movie.2.mkv") // ".Movie.2.<digits>" is this one's, not Movie.mkv's
	mk := func(name string, files ...string) string {
		p := filepath.Join(dir, name)
		os.Mkdir(p, 0o755)
		for _, f := range files {
			os.WriteFile(filepath.Join(p, f), []byte("x"), 0o644)
		}
		return p
	}
	ours := mk(".Movie.12345.syp-tmp", "new.mkv")
	notOurs := mk(".Movie.555.syp-tmp", "new.mkv", "holiday.jpg")
	otherFiles := mk(".Movie.2.777.syp-tmp", "new.mkv")
	unknown := mk(".Unknown.42.syp-tmp", "new.mkv")
	said := SweepNextTo([]string{movie})
	if _, err := os.Stat(ours); !os.IsNotExist(err) || len(said) != 1 {
		t.Errorf("ours: %v %v", err, said)
	}
	for _, p := range []string{notOurs, otherFiles, unknown} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed", p)
		}
	}
	if said := SweepNextTo([]string{movie, other}); len(said) != 1 {
		t.Errorf("Movie.2.mkv's own: %v", said)
	}
}
