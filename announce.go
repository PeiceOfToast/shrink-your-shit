// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"log"
	"os"
	"sync"

	"github.com/shrink-your-shit/shrink-your-shit/internal/discover"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/web"
)

// announcer makes this install findable on the local network while it is a server, so a worker's join page can
// list it (E4). It is only used when the web UI is reachable from the network: a server that listens on this computer
// alone is not one a worker can reach, so it does not announce itself.
type announcer struct {
	on      bool // the web UI listens beyond this computer
	port    int
	version string
	// announce is discover.Announce; tests replace it so they never touch the network.
	announce func(name string, port int, version string) (func(), error)

	mu   sync.Mutex
	stop func()
}

// announceRole is the role the announcer goes by at startup: the saved role, or for an install that predates saved
// roles (no saved role, but a library exists) "server", the same as the web UI reads it. RoleChanged passes the new
// role directly, since a role change always saves one.
func announceRole(store *jobs.Store) string {
	if role := web.SavedRole(store); role != "" {
		return role
	}
	libs, err := store.Libraries()
	if err == nil && len(libs) > 0 {
		return "server"
	}
	return ""
}

// SetRole takes the install's role: at startup, and after each change. Only "server" announces.
func (a *announcer) SetRole(role string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if role != "server" || !a.on {
		if a.stop != nil {
			a.stop()
			a.stop = nil
		}
		return
	}
	if a.stop != nil {
		return
	}
	name, _ := os.Hostname()
	if name == "" {
		name = "shrink-your-shit"
	}
	announce := a.announce
	if announce == nil {
		announce = discover.Announce
	}
	stop, err := announce(name, a.port, a.version)
	if err != nil {
		log.Printf("discover: this server can't announce itself on the network; workers type its address instead: %v", err)
		return
	}
	a.stop = stop
	log.Printf("discover: announcing this server on the network as %q", name)
}
