// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build windows

package web

import (
	"os"
	"testing"
)

// The one real run of WNetAddConnection2W and WNetCancelConnection2W. It runs only on CI, where the
// GitHub Windows runner is an administrator, so the admin share C$ is there to connect to.
func TestWindowsRealConnectOnCI(t *testing.T) {
	if os.Getenv("CI") == "" {
		t.Skip("runs only on CI: it connects this machine's admin share")
	}
	const unc = `\\localhost\C$`
	err := winConnectSys(unc, "", "")
	if err != nil {
		we, ok := err.(*winError)
		if !ok || (we.Code != 1219 && we.Code != 85) { // 1219/85: already connected with other credentials
			t.Fatalf("winConnectSys(%s): %v", unc, err)
		}
	}
	defer func() {
		if err := winDisconnectSys(unc); err != nil {
			t.Errorf("winDisconnectSys(%s): %v", unc, err)
		}
	}()
	if !statWithin(unc+`\`, shareStatTimeout) {
		t.Fatalf("%s is connected but does not answer", unc)
	}
}
