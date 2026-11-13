// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

// D3b: on Windows the app connects an SMB share itself, in the logged-in person's session (a deviceless
// WNetAddConnection2), so a library reads it as \\server\share. Linux and macOS keep the helper.

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/helper"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// onWindows says whether shares connect through Windows. Tests replace it.
var onWindows = func() bool { return runtime.GOOS == "windows" }

// winConnect and winDisconnect are the Windows calls (tests replace them).
var (
	winConnect    = winConnectSys
	winDisconnect = winDisconnectSys
)

// winError is a Windows network error code. Words is the system's text for it, when it has any.
type winError struct {
	Code  uint32
	Words string
}

func (e *winError) Error() string {
	if e.Words != "" {
		return e.Words
	}
	return fmt.Sprintf("Windows error %d", e.Code)
}

// winMountReason is the reason the page shows for a failed Windows connect: the same JSON as mountErrReason.
func winMountReason(err error) string {
	var we *winError
	if !errors.As(err, &we) {
		return mountErrReason(err)
	}
	code, msg := "mount_failed", "Windows could not connect the share. "+we.Words
	switch we.Code {
	case 53:
		code, msg = "host_not_found", "The server could not be found on the network."
	case 67:
		code, msg = "share_not_found", "The server has no share with that name."
	case 1326, 86:
		code, msg = "auth_failed", "The user name or password was refused."
	case 5:
		code, msg = "access_denied", "Windows refused access to the share."
	case 1231, 64:
		code, msg = "host_unreachable", "The server could not be reached."
	}
	b, _ := json.Marshal(map[string]string{"code": code, "message": strings.TrimSpace(msg)})
	return string(b)
}

// connectWindowsShare connects sh as the logged-in person. A connection Windows already holds to the same
// share (1219 or 85) counts as connected when its path answers.
func connectWindowsShare(sh jobs.Share, password string) error {
	unc := mountPointFor(sh)
	user, pw := sh.Username, password
	if sh.Guest {
		user, pw = "", ""
	}
	err := winConnect(unc, user, pw)
	var we *winError
	if err != nil && errors.As(err, &we) && (we.Code == 1219 || we.Code == 85) && shareAnswers(unc) {
		return nil
	}
	return err
}

// uncPath is \\server\share for a stored SMB address (stored with backslashes).
func uncPath(addr string) string {
	return `\\` + strings.TrimLeft(strings.ReplaceAll(addr, "/", `\`), `\`)
}

// unmountShare disconnects the share mounted at mp (its UNC path on Windows).
func unmountShare(mp string) error {
	if onWindows() {
		return winDisconnect(mp)
	}
	return helper.Unmount(mp)
}
