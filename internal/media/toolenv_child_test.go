// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeTool writes a shell script that records the LD_LIBRARY_PATH it was started with, in the file
// named by SYP_TOOLENV_OUT, then prints nothing. The three media entry points are then run against
// it, so the test sees the environment a real child gets, not a helper's.
func fakeTool(t *testing.T, dir, name string) {
	t.Helper()
	script := "#!/bin/sh\nprintf '%s' \"$LD_LIBRARY_PATH\" > \"$SYP_TOOLENV_OUT\"\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// childLibraryPath runs each media entry point against the fake tools and returns the
// LD_LIBRARY_PATH each child saw. The entry points' own errors are not the point here: the fake
// prints nothing the parsers can use, so only the child's environment is read.
func childLibraryPath(t *testing.T, which string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "ld.txt")
	t.Setenv("SYP_TOOLENV_OUT", out)
	ctx := context.Background()
	switch which {
	case "run":
		_, _ = Run(ctx, []string{"ffmpeg", "-i", "x.mkv"}, nil)
	case "probe", "frame":
		in := filepath.Join(t.TempDir(), "x.mkv")
		if err := os.WriteFile(in, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if which == "probe" {
			_, _ = Probe(ctx, in)
		} else {
			_, _ = ProbeFrameHDR(ctx, in)
		}
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("%s: the child did not run (%v)", which, err)
	}
	return string(b)
}

// TestChildrenGetTheLibFolderFirst: with the installer's lib folder present, Run, Probe and
// ProbeFrameHDR all start their child with it first on LD_LIBRARY_PATH (review2 #852 F1, F2).
func TestChildrenGetTheLibFolderFirst(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake tools are shell scripts")
	}
	defer SetToolDir("")
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib")
	if err := os.Mkdir(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeTool(t, dir, "ffmpeg")
	fakeTool(t, dir, "ffprobe")
	SetToolDir(dir)
	t.Setenv("LD_LIBRARY_PATH", "/parent/lib")

	want := lib + ":/parent/lib"
	for _, which := range []string{"run", "probe", "frame"} {
		if got := childLibraryPath(t, which); got != want {
			t.Errorf("%s: child LD_LIBRARY_PATH = %q, want %q", which, got, want)
		}
	}
}

// TestChildrenKeepTheirEnvironmentWithoutTheLibFolder: with no lib folder the child's environment
// is the service's own, untouched.
func TestChildrenKeepTheirEnvironmentWithoutTheLibFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake tools are shell scripts")
	}
	defer SetToolDir("")
	dir := t.TempDir() // no lib folder
	fakeTool(t, dir, "ffmpeg")
	fakeTool(t, dir, "ffprobe")
	SetToolDir(dir)
	t.Setenv("LD_LIBRARY_PATH", "/parent/lib")

	for _, which := range []string{"run", "probe", "frame"} {
		if got := childLibraryPath(t, which); got != "/parent/lib" {
			t.Errorf("%s: child LD_LIBRARY_PATH = %q, want the service's own %q", which, got, "/parent/lib")
		}
	}
}
