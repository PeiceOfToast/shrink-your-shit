// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/share"
	"github.com/PeiceOfToast/shrink-your-shit/internal/share/helper"
)

// TestShareMounterUsesTheHelperSocket: the installed helper's socket is what makes this a helper
// computer, and the mounter is the client that talks to it.
func TestShareMounterUsesTheHelperSocket(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the helper and its socket are Linux-only")
	}
	path := filepath.Join(t.TempDir(), "mount.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	mounter, why := shareMounter(path)
	if why != "" {
		t.Fatalf("why = %q, want helper mode", why)
	}
	if _, ok := mounter.(*helper.Client); !ok {
		t.Fatalf("mounter = %T, want *helper.Client", mounter)
	}
}

// TestShareMounterWithoutASocketIsHostMode: no helper means the app cannot mount, and the sentence
// the page shows says exactly that.
func TestShareMounterWithoutASocketIsHostMode(t *testing.T) {
	mounter, why := shareMounter(filepath.Join(t.TempDir(), "missing.sock"))
	if _, ok := mounter.(share.Unavailable); !ok {
		t.Fatalf("mounter = %T, want share.Unavailable", mounter)
	}
	if why != share.NoHelperReason {
		t.Fatalf("why = %q, want the no-helper sentence", why)
	}
}

// TestShareMounterIgnoresAFileOfThatName: a regular file at the socket's path is not a helper. A
// dial would fail anyway, but saying host mode up front keeps the page honest.
func TestShareMounterIgnoresAFileOfThatName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mount.sock")
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if mounter, why := shareMounter(path); why == "" {
		t.Fatalf("mounter = %T, want host mode for a regular file", mounter)
	}
}
