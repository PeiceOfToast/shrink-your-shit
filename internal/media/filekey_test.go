// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// FileKey says which file a name is, from a directory listing's FileInfo too (on Windows that FileInfo
// carries no file index, which made the two-look rule never see a file as unchanged: CI on #54). It is
// stable for one file and differs for another.
func TestFileKeyFromAListing(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.mkv"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.mkv"), []byte("1"), 0o644)
	keys := func() map[string]string {
		out := map[string]string{}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			fi, _ := e.Info()
			k, ok := FileKey(filepath.Join(dir, e.Name()), fi)
			if !ok {
				if runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
					t.Fatalf("%s: no key on %s", e.Name(), runtime.GOOS)
				}
				t.Skip("this system does not say")
			}
			out[e.Name()] = k
		}
		return out
	}
	first, second := keys(), keys()
	if first["a.mkv"] == "" || first["a.mkv"] != second["a.mkv"] {
		t.Errorf("not stable: %q %q", first["a.mkv"], second["a.mkv"])
	}
	if first["a.mkv"] == first["b.mkv"] {
		t.Error("two files have one key")
	}
}
