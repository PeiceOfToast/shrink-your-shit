// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestPlaceErrorBasic: placeError wraps an os.PathError, extracts the path.
func TestPlaceErrorBasic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("path patterns differ on Windows")
	}
	err := &os.PathError{Op: "rename", Path: "/mnt/library/new.mkv", Err: os.ErrPermission}
	w := &Worker{} // Queue is nil; replaceStep checks and returns
	result := w.placeError(1, time.Time{}, "/mnt/library/new.mkv", err)
	pe, ok := result.(*placeError)
	if !ok {
		t.Fatalf("not a placeError: %T", result)
	}
	if !strings.Contains(pe.Path, "/mnt/library") {
		t.Errorf("path should contain /mnt/library: %q", pe.Path)
	}
}

// TestPlaceErrorStaging: a path inside .syp-tmp names its parent.
func TestPlaceErrorStaging(t *testing.T) {
	err := &os.PathError{Op: "rename", Path: "/mnt/library/.syp-tmp/new.mkv", Err: os.ErrPermission}
	w := &Worker{}
	result := w.placeError(1, time.Time{}, "/mnt/library/.syp-tmp/new.mkv", err)
	pe, ok := result.(*placeError)
	if !ok {
		t.Fatalf("not a placeError: %T", result)
	}
	if pe.Path != "/mnt/library" {
		t.Errorf("staging parent should be /mnt/library: %q", pe.Path)
	}
}

// TestPlaceErrorLinkError: an os.LinkError path is extracted.
func TestPlaceErrorLinkError(t *testing.T) {
	err := &os.LinkError{Op: "rename", Old: "/tmp/orig.mkv", New: "/mnt/library/movie.mkv", Err: os.ErrPermission}
	w := &Worker{}
	result := w.placeError(1, time.Time{}, "/mnt/library/movie.mkv", err)
	pe, ok := result.(*placeError)
	if !ok {
		t.Fatalf("not a placeError: %T", result)
	}
	if !strings.Contains(pe.Path, "/mnt/library") {
		t.Errorf("path should contain /mnt/library: %q", pe.Path)
	}
}
