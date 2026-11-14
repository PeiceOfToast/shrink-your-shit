// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"log"
	"os"
	"sync"

	"github.com/shrink-your-shit/shrink-your-shit/internal/discover"
)

// announcer makes this install findable on the local network while its role is server, so a worker's join page can
// list it (E4). It is only used when the web UI is reachable from the network: a server that listens on this computer
// alone is not one a worker can reach, so it does not announce itself.
type announcer struct {
	on      bool // the web UI listens beyond this computer
	port    int
	version string

	mu   sync.Mutex
	stop func()
}

// SetRole takes the install's saved role: at startup, and after each change. Only "server" announces.
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
	stop, err := discover.Announce(name, a.port, a.version)
	if err != nil {
		log.Printf("discover: this server can't announce itself on the network; workers type its address instead: %v", err)
		return
	}
	a.stop = stop
	log.Printf("discover: announcing this server on the network as %q", name)
}
