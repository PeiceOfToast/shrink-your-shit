// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package discover lets a server announce itself on the local network and lets a worker find servers that do the
// same, over mDNS / DNS-SD. Nothing here is required: when multicast is unavailable, a browse finds nothing and the
// person types the server's address instead.
package discover

import (
	"context"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

// Service is the DNS-SD service type every shrink-your-shit server announces.
const Service = "_syp._tcp"

// Found is one server a browse answered: its announced name, the address and port to reach it, and its version.
type Found struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	Version string `json:"version"`
}

// Announce answers the local network's browses for a server called name on port, running version, until stop is
// called. The version travels in the TXT record as "version=...".
func Announce(name string, port int, version string) (stop func(), err error) {
	s, err := zeroconf.Register(name, Service, "local.", port, []string{"version=" + version}, nil)
	if err != nil {
		return nil, err
	}
	return s.Shutdown, nil
}

// browseFunc sends each server that answers into entries until ctx ends, then closes entries. The real one is a
// zeroconf browse; tests pass a fake.
type browseFunc func(ctx context.Context, entries chan<- *zeroconf.ServiceEntry) error

func browseLAN(ctx context.Context, entries chan<- *zeroconf.ServiceEntry) error {
	r, err := zeroconf.NewResolver()
	if err != nil {
		return err
	}
	return r.Browse(ctx, Service, "local.", entries)
}

// Finder browses the local network for servers. Its answers are kept briefly, so a page that asks again and again
// does not start a new browse each time.
type Finder struct {
	browse browseFunc
	window time.Duration // how long one browse listens
	keep   time.Duration // how long an answer is reused

	mu    sync.Mutex
	at    time.Time
	have  bool
	found []Found

	lastMu sync.Mutex // guards last, which Known reads without waiting for a browse
	last   []Found
}

// Known returns the servers the last browse found. It never browses, and it never waits on one in progress: it is
// empty until a Servers call has finished a browse.
func (f *Finder) Known() []Found {
	f.lastMu.Lock()
	defer f.lastMu.Unlock()
	return f.last
}

// NewFinder is the Finder a server uses: a 2 s browse, answers kept for 5 s.
func NewFinder() *Finder {
	return &Finder{browse: browseLAN, window: 2 * time.Second, keep: 5 * time.Second}
}

// Servers returns the servers that answered a browse in the last few seconds. It is never nil: an empty list means
// nothing answered, or multicast is unavailable (the failure is logged, not returned).
func (f *Finder) Servers() []Found {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.have && time.Since(f.at) < f.keep {
		return f.found
	}
	f.found = f.scan()
	f.at = time.Now()
	f.have = true
	f.lastMu.Lock()
	f.last = f.found
	f.lastMu.Unlock()
	return f.found
}

// scan listens for one window and collects the servers that answered, one per name, address and port. The reader
// ends with the window, not with entries being closed: a browse that fails before it starts (no multicast) never
// closes entries, and the reader must not be left waiting on it.
func (f *Finder) scan() []Found {
	ctx, cancel := context.WithTimeout(context.Background(), f.window)
	defer cancel()
	entries := make(chan *zeroconf.ServiceEntry, 16)
	found := []Found{}
	seen := map[string]bool{}
	add := func(e *zeroconf.ServiceEntry) {
		if e == nil { // a closed entries channel yields nil: nothing more is coming
			return
		}
		srv, ok := fromEntry(e)
		if ok && !seen[srv.key()] && len(found) < maxServers {
			seen[srv.key()] = true
			found = append(found, srv)
		}
	}
	read := make(chan struct{})
	go func() {
		defer close(read)
		for {
			select {
			case e, ok := <-entries:
				if !ok { // the browse ended and closed entries
					return
				}
				add(e)
			case <-ctx.Done():
				for { // what already arrived still counts
					select {
					case e, ok := <-entries:
						if !ok {
							return
						}
						add(e)
					default:
						return
					}
				}
			}
		}
	}()
	if err := f.browse(ctx, entries); err != nil {
		log.Printf("discover: can't look for servers on the network: %v", err)
		cancel()
	}
	<-ctx.Done()
	<-read
	return found
}

func (s Found) key() string {
	return s.Name + "|" + s.Address + "|" + strconv.Itoa(s.Port)
}

// Limits on what the network may send. Answers come from strangers on the LAN, so a name or version is cut to
// maxText characters (truncated, not dropped), a port outside 1-65535 drops the answer, and a browse keeps at most
// maxServers answers.
const (
	maxText    = 128
	maxServers = 64
)

// fromEntry turns one answer into a Found. An answer with no address to reach it by, or a port that is not a port,
// is skipped.
func fromEntry(e *zeroconf.ServiceEntry) (Found, bool) {
	if e.Port < 1 || e.Port > 65535 {
		return Found{}, false
	}
	var addr string
	switch {
	case len(e.AddrIPv4) > 0:
		addr = e.AddrIPv4[0].String()
	case len(e.AddrIPv6) > 0:
		addr = e.AddrIPv6[0].String()
	default:
		addr = strings.TrimSuffix(e.HostName, ".")
	}
	if addr == "" {
		return Found{}, false
	}
	version := ""
	for _, t := range e.Text {
		if v, ok := strings.CutPrefix(t, "version="); ok {
			version = v
		}
	}
	return Found{Name: clip(e.Instance), Address: addr, Port: e.Port, Version: clip(version)}, true
}

// clip cuts s to maxText characters.
func clip(s string) string {
	if r := []rune(s); len(r) > maxText {
		return string(r[:maxText])
	}
	return s
}
