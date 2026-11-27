// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package library

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestFileInLibraryJudgesTheRealPath: a try may read a file a library owns, judged by its real path.
// A missing file still compares by where it would be (the try itself answers "could not be read"),
// and a link inside a library that points outside it is not inside the library.
func TestFileInLibraryJudgesTheRealPath(t *testing.T) {
	c, _ := newTestCatalog(t)
	root := newFolder(t)
	if _, err := c.AddLibrary(context.Background(), NewLibrary{Path: root}); err != nil {
		t.Fatalf("add the library: %v", err)
	}
	write := func(path string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("video"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	inside := write(filepath.Join(root, "holiday.mkv"))
	missing := filepath.Join(root, "sub", "not-there-yet.mkv")
	outside := write(filepath.Join(t.TempDir(), "elsewhere.mkv"))

	cases := []struct {
		name string
		file string
		want bool
	}{
		{"a file in the library", inside, true},
		{"a file in a folder of the library that is not there yet", missing, true},
		{"a file outside every library", outside, false},
	}
	link := filepath.Join(root, "link.mkv")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Logf("this Windows account cannot make a symlink, so the link case is not tried: %v", err)
		} else {
			t.Fatalf("make the link: %v", err)
		}
	} else {
		cases = append(cases, struct {
			name string
			file string
			want bool
		}{"a link in the library that points outside", link, false})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.FileInLibrary(context.Background(), tc.file)
			if err != nil {
				t.Fatalf("check %s: %v", tc.file, err)
			}
			if got != tc.want {
				t.Errorf("FileInLibrary(%s) = %v, want %v", tc.file, got, tc.want)
			}
		})
	}

	// With no libraries at all, nothing is inside one.
	empty, _ := newTestCatalog(t)
	got, err := empty.FileInLibrary(context.Background(), inside)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("a file was inside a library on an install with no libraries")
	}
}
