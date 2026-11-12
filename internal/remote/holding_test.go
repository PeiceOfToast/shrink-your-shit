// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// #174 QA e: a worker short of room takes no work and says why: once in its log (and once when it takes
// work again), and with each heartbeat for the Workers page.
func TestHoldingBackForLowDisk(t *testing.T) {
	var mu sync.Mutex
	var beats []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.HasSuffix(r.URL.Path, "/heartbeat") {
			mu.Lock()
			beats = append(beats, string(b))
			mu.Unlock()
		}
		w.Write([]byte(`{"lease":null}`))
	}))
	defer srv.Close()
	var logged bytes.Buffer
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)
	c := &Client{Server: srv.URL, Token: "t", Version: "test"}
	free := uint64(1 << 30)
	cv := Converter{Work: t.TempDir(), MinFree: 2 << 30, FreeSpace: func(string) (uint64, error) { return free, nil }}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if did, err := c.workOne(ctx, cv); did || err != nil {
			t.Fatalf("short of room: %v %v", did, err)
		}
	}
	c.Heartbeat(ctx, 1)
	free = 5 << 30
	c.workOne(ctx, cv) // room again: it asks for work
	c.Heartbeat(ctx, 1)
	if n := strings.Count(logged.String(), "Not taking work: 1.0 GB free on "+cv.Work+", below the 2.0 GB it keeps free (--min-free-gb)."); n != 1 {
		t.Errorf("logged %d times:\n%s", n, logged.String())
	}
	if strings.Count(logged.String(), "Taking work again") != 1 {
		t.Errorf("resumed: %s", logged.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(beats) != 2 || !strings.Contains(beats[0], `"holdingBack":{"code":"low_disk","message":"Not taking work: 1.0 GB free`) || !strings.Contains(beats[1], `"holdingBack":null`) {
		t.Errorf("heartbeats %v", beats)
	}
}
