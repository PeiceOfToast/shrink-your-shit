// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package fsx

import "testing"

// A local temporary folder is not a network share. The true cases are not tested: CI has no network
// mount, and mounting one needs root on the machine.
func TestIsNetworkOnALocalFolderIsFalse(t *testing.T) {
	for _, dir := range []string{t.TempDir(), "."} {
		net, kind, err := IsNetwork(dir)
		if err != nil {
			t.Fatalf("IsNetwork(%s): %v", dir, err)
		}
		if net {
			t.Errorf("IsNetwork(%s) = true (%s), want false for a local folder", dir, kind)
		}
	}
}
