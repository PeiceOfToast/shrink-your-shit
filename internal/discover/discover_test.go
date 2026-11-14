// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package discover

import (
	"context"
	"errors"
	"net"
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
