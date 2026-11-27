// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package fsx

import "testing"

// networkPathKind classifies a Windows path without the OS, so it is tested here on every platform.
func TestNetworkPathKind(t *testing.T) {
	for _, c := range []struct {
		path     string
		wantKind string
		wantAsk  bool
	}{
		{`\\server\share\data`, "network share", false},
		{`//server/share/data`, "network share", false},
		{`\\?\UNC\server\share\data`, "network share", false},
		{`\\?\C:\data`, "", false},
		{`//?/C:/data`, "", false},
		{`C:\data`, "", true},
		{`Z:\`, "", true},
	} {
		kind, ask := networkPathKind(c.path)
		if kind != c.wantKind || ask != c.wantAsk {
			t.Errorf("networkPathKind(%q) = %q, %v; want %q, %v", c.path, kind, ask, c.wantKind, c.wantAsk)
		}
	}
}
