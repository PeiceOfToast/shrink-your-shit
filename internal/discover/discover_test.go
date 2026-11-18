// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package discover

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grandcat/zeroconf"
)

// fakeBrowse answers with the given entries, then returns err.
func fakeBrowse(calls *atomic.Int32, err error, entries ...*zeroconf.ServiceEntry) browseFunc {
	return func(ctx context.Context, out chan<- *zeroconf.ServiceEntry) error {
		calls.Add(1)
		go func() {
			defer close(out)
			for _, e := range entries {
				out <- e
			}
			<-ctx.Done()
		}()
		return err
	}
}

func finder(b browseFunc) *Finder {
	return &Finder{browse: b, window: 200 * time.Millisecond, keep: time.Minute}
}

func TestServersListsEachAnswerOnceWithItsVersion(t *testing.T) {
	var calls atomic.Int32
	office := &zeroconf.ServiceEntry{ServiceRecord: zeroconf.ServiceRecord{Instance: "Office"}, Port: 8484,
		AddrIPv4: []net.IP{net.ParseIP("192.168.1.20")}, Text: []string{"version=1.4.0"}}
	dupe := *office
	noAddr := &zeroconf.ServiceEntry{ServiceRecord: zeroconf.ServiceRecord{Instance: "Ghost"}, Port: 8484}
	v6 := &zeroconf.ServiceEntry{ServiceRecord: zeroconf.ServiceRecord{Instance: "Shed"}, Port: 9000,
		AddrIPv6: []net.IP{net.ParseIP("fe80::1")}}
	f := finder(fakeBrowse(&calls, nil, office, &dupe, noAddr, v6))

	got := f.Servers()
	want := []Found{
		{Name: "Office", Address: "192.168.1.20", Port: 8484, Version: "1.4.0"},
		{Name: "Shed", Address: "fe80::1", Port: 9000, Version: ""},
	}
	if len(got) != len(want) {
		t.Fatalf("servers %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("server %d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestServersIsEmptyNotNilWhenNothingAnswers(t *testing.T) {
	var calls atomic.Int32
	got := finder(fakeBrowse(&calls, nil)).Servers()
	if got == nil || len(got) != 0 {
		t.Fatalf("servers %#v, want an empty list", got)
	}
}

func TestServersIsEmptyWhenMulticastIsUnavailable(t *testing.T) {
	var calls atomic.Int32
	got := finder(fakeBrowse(&calls, errors.New("no suitable interface"))).Servers()
	if got == nil || len(got) != 0 {
		t.Fatalf("servers %#v, want an empty list", got)
	}
}

func TestServersReusesAnAnswerBriefly(t *testing.T) {
	var calls atomic.Int32
	f := finder(fakeBrowse(&calls, nil))
	f.Servers()
	f.Servers()
	if n := calls.Load(); n != 1 {
		t.Errorf("browses %d, want 1 (the second call uses the kept answer)", n)
	}
	f.keep = 0
	f.Servers()
	if n := calls.Load(); n != 2 {
		t.Errorf("browses %d, want 2 once the answer is stale", n)
	}
}

// A browse that fails before it starts (the no-multicast case: NewResolver errors) never closes its entries. Each scan
// must still leave no goroutine behind.
func TestFailingBrowseLeavesNoGoroutineBehind(t *testing.T) {
	failing := func(ctx context.Context, out chan<- *zeroconf.ServiceEntry) error {
		return errors.New("no suitable interface")
	}
	f := finder(failing)
	before := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		if got := f.scan(); len(got) != 0 {
			t.Fatalf("scan %d found %+v, want nothing", i, got)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Errorf("goroutines before 5 failing scans %d, after %d: each scan leaks one", before, after)
	}
}

// Answers from the network are bounded: a port that is not a port is dropped, a long name or version is cut to 128
// characters, and a browse keeps at most 64 servers.
func TestAnswersFromTheNetworkAreBounded(t *testing.T) {
	var calls atomic.Int32
	long := strings.Repeat("n", 300)
	entries := []*zeroconf.ServiceEntry{
		{ServiceRecord: zeroconf.ServiceRecord{Instance: "Ghost"}, Port: 0, AddrIPv4: []net.IP{net.ParseIP("192.168.1.9")}},
		{ServiceRecord: zeroconf.ServiceRecord{Instance: "Huge"}, Port: 70000, AddrIPv4: []net.IP{net.ParseIP("192.168.1.10")}},
		{ServiceRecord: zeroconf.ServiceRecord{Instance: long}, Port: 8484, AddrIPv4: []net.IP{net.ParseIP("192.168.1.11")},
			Text: []string{"version=" + long}},
	}
	for i := 0; i < 70; i++ {
		entries = append(entries, &zeroconf.ServiceEntry{ServiceRecord: zeroconf.ServiceRecord{Instance: fmt.Sprintf("S%d", i)},
			Port: 8484, AddrIPv4: []net.IP{net.ParseIP(fmt.Sprintf("192.168.2.%d", i+1))}})
	}
	got := finder(fakeBrowse(&calls, nil, entries...)).Servers()

	if len(got) != 64 {
		t.Fatalf("kept %d servers, want 64 (the cap)", len(got))
	}
	for _, s := range got {
		if s.Port == 0 || s.Address == "192.168.1.9" || s.Address == "192.168.1.10" {
			t.Errorf("kept an answer with a bad port: %+v", s)
		}
	}
	first := got[0]
	if len([]rune(first.Name)) != 128 || len([]rune(first.Version)) != 128 {
		t.Errorf("name %d and version %d characters, want both cut to 128", len([]rune(first.Name)), len([]rune(first.Version)))
	}
}

// The browse closes entries when its window ends. A closed channel must end the reader, not feed it nil answers
// (that panicked in fromEntry). Run repeatedly: the close and the reader race.
func TestClosedEntriesEndTheReaderWithoutPanicking(t *testing.T) {
	var calls atomic.Int32
	for i := 0; i < 20; i++ {
		f := finder(fakeBrowse(&calls, nil, &zeroconf.ServiceEntry{ServiceRecord: zeroconf.ServiceRecord{Instance: "Office"},
			Port: 8484, AddrIPv4: []net.IP{net.ParseIP("192.168.1.20")}}))
		if got := f.scan(); len(got) != 1 {
			t.Fatalf("run %d found %+v, want the one server", i, got)
		}
	}
}

// Known reads the last browse's list: it never browses, and it does not wait on a browse in progress.
func TestKnownReadsTheLastBrowseWithoutBrowsing(t *testing.T) {
	var calls atomic.Int32
	office := &zeroconf.ServiceEntry{ServiceRecord: zeroconf.ServiceRecord{Instance: "Office"}, Port: 8484,
		AddrIPv4: []net.IP{net.ParseIP("192.168.1.20")}}
	f := finder(fakeBrowse(&calls, nil, office))
	if got := f.Known(); len(got) != 0 || calls.Load() != 0 {
		t.Fatalf("before any browse: %+v, browses %d", got, calls.Load())
	}
	f.Servers()
	if got := f.Known(); len(got) != 1 || got[0].Name != "Office" || calls.Load() != 1 {
		t.Errorf("after a browse: %+v, browses %d, want Office and 1 browse", got, calls.Load())
	}
}

func TestKnownDoesNotWaitOnABrowseInProgress(t *testing.T) {
	started := make(chan struct{})
	slow := func(ctx context.Context, out chan<- *zeroconf.ServiceEntry) error {
		close(started)
		defer close(out)
		<-ctx.Done()
		return nil
	}
	f := finder(slow)
	done := make(chan struct{})
	go func() { f.Servers(); close(done) }()
	<-started
	answer := make(chan []Found, 1)
	go func() { answer <- f.Known() }()
	select {
	case <-answer:
	case <-time.After(time.Second):
		t.Fatal("Known waited on a browse in progress")
	}
	<-done
}
