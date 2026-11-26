// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A library whose folder is missing (a disk not mounted) is not walked whole, so its settling rows stay. They are
// forgotten only once the folder is back and the file is really gone.
func TestSettlingRowSurvivesMissingLibraryFolder(t *testing.T) {
	s, st, lib, _ := setup(t, h264Big)
	clock := time.Now()
	s.Now, s.MinAge = func() time.Time { return clock }, time.Minute
	a := filepath.Join(lib, "a.mkv")
	os.Chtimes(a, clock, clock)
	s.ScanOnce(context.Background()) // young: settling
	if r, _ := st.GetFile(a); r == nil || r.Reason != rules.SkipStillChanging {
		t.Fatalf("no settling row for a young file: %+v", r)
	}

	os.RemoveAll(lib) // the folder is missing: the walk finds nothing
	s.ScanOnce(context.Background())
	if r, _ := st.GetFile(a); r == nil {
		t.Error("settling row forgotten while its library folder is missing")
	}

	os.Mkdir(lib, 0o755) // back, and the file gone
	s.ScanOnce(context.Background())
	if r, _ := st.GetFile(a); r != nil {
		t.Errorf("settling row of a file gone from a back folder kept: %+v", r)
	}
}
