// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"io/fs"
	"os"

	"github.com/PeiceOfToast/shrink-your-shit/internal/share"
	"github.com/PeiceOfToast/shrink-your-shit/internal/share/helper"
)

// shareService wires the share API to this computer. With the root helper's socket in place a share
// is mounted by the helper; without it the app cannot mount anything, and GET /shares says so, while
// a person can still add a library on a folder they mounted themselves.
func shareService(store *share.Store, socket string) *share.Service {
	mounter, why := shareMounter(socket)
	svc := share.NewService(store, mounter)
	if why != "" {
		svc.HostMode(why)
	}
	return svc
}

// shareMounter answers the mounter to run with, or the sentence explaining why there is none. The
// socket has to be a socket: a stale file of that name is not a helper, and dialling it would make
// every mount wait out the connect.
func shareMounter(socket string) (share.Mounter, string) {
	if info, err := os.Stat(socket); err == nil && info.Mode()&fs.ModeSocket != 0 {
		return helper.NewClient(socket), ""
	}
	return share.Unavailable{}, share.NoHelperReason
}
