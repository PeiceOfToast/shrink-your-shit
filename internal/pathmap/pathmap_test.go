// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package pathmap

import (
	"path/filepath"
	"testing"
)

// orch-lead 15:17Z (e): prefix-safe, and cleaned on both kinds of worker path.
func TestMap(t *testing.T) {
	root := filepath.FromSlash("/media")
	if filepath.VolumeName(t.TempDir()) != "" { // Windows server
		root = `C:\media`
	}
	in := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	for _, c := range []struct {
		server, worker, file, want string
		ok                         bool
	}{
		{root, `\\nas\movies`, in("Film/a.mkv"), `\\nas\movies\Film\a.mkv`, true},
		{root, `\\nas\movies\`, in("a.mkv"), `\\nas\movies\a.mkv`, true},
		{root, `D:\Movies`, in("x/y.mkv"), `D:\Movies\x\y.mkv`, true},
		{root, `D:/Movies/./old/..`, in("y.mkv"), `D:\Movies\y.mkv`, true},
		{root, `/mnt/movies/`, in("Film/a.mkv"), `/mnt/movies/Film/a.mkv`, true},
		{root, `/mnt/movies`, root, `/mnt/movies`, true},
		{root, `/mnt/movies`, root + "2" + string(filepath.Separator) + "a.mkv", "", false}, // /media2 is not /media
		{root, `/mnt/movies`, in("../etc/passwd"), "", false},
		{root + string(filepath.Separator), `/mnt/movies`, in("a.mkv"), `/mnt/movies/a.mkv`, true},
	} {
		got, ok := Map(c.file, c.server, c.worker)
		if got != c.want || ok != c.ok {
			t.Errorf("Map(%q, %q, %q) = %q %v, want %q %v", c.file, c.server, c.worker, got, ok, c.want, c.ok)
		}
	}
	for in, want := range map[string]string{`\\nas\\movies\\`: `\\nas\movies`, `D:`: `D:\`, `D:\`: `D:\`, `D:\a\..\..`: `D:\`, "/a//b/": "/a/b"} {
		if got := CleanWorker(in); got != want {
			t.Errorf("CleanWorker(%q) = %q, want %q", in, got, want)
		}
	}
}
