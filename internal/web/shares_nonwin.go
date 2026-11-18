// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !windows

package web

import "errors"

// Windows connects its own shares (shares_win.go). Elsewhere the helper does, so these are never called.
var errNotWindows = errors.New("shares are connected by Windows only on Windows")

func winConnectSys(unc, user, password string) error { return errNotWindows }

func winDisconnectSys(unc string) error { return errNotWindows }
