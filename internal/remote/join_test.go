// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fakeJoin is a server that asks the person nothing: it answers the join with secret "s3cret", then gives
// each poll the next of states ("waiting", "allowed", "denied", "expired", or "gone" for a dropped request).
// The last state repeats.
func fakeJoin(t *testing.T, states ...string) *httptest.Server {
	t.Helper()
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-SYP-Worker") != "1.2.3; api=1" {
			w.WriteHeader(426)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/worker/v1/join":
			w.WriteHeader(202)
			w.Write([]byte(`{"id":"j-1","expiresAt":"2026-11-14T03:00:00Z","secret":"s3cret"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/worker/v1/join/j-1":
			if r.Header.Get("Authorization") != "Bearer s3cret" {
				w.WriteHeader(404)
				w.Write([]byte(`{"error":{"code":"join_request_not_found","message":"There is no such join request. Nothing was changed."}}`))
				return
			}
			i := min(int(polls.Add(1))-1, len(states)-1)
			switch st := states[i]; st {
			case "allowed":
				w.Write([]byte(`{"state":"allowed","workerId":"w-9","name":"Office PC","token":"tok"}`))
			case "gone":
				w.WriteHeader(404)
				w.Write([]byte(`{"error":{"code":"join_request_not_found","message":"There is no such join request. Nothing was changed."}}`))
			default:
				w.Write([]byte(`{"state":"` + st + `"}`))
			}
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	old := JoinEvery
	JoinEvery = time.Millisecond
	t.Cleanup(func() { JoinEvery = old })
	return srv
}

// A worker asks to join, waits while the person has not answered, and gets its token once they allow it.
func TestJoinWaitsThenReturnsTheAllowedState(t *testing.T) {
	srv := fakeJoin(t, "waiting", "waiting", "allowed")
	c := &Client{Server: srv.URL, Version: "1.2.3"}
	st, err := c.Join(context.Background(), "Office PC", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if st.Token != "tok" || st.WorkerID != "w-9" || st.Name != "Office PC" || st.Server != srv.URL {
		t.Errorf("state %+v", st)
	}
}

func TestJoinDeniedAndExpiredEndWithTheirErrors(t *testing.T) {
	c := func(srv *httptest.Server) *Client { return &Client{Server: srv.URL, Version: "1.2.3"} }
	if _, err := c(fakeJoin(t, "waiting", "denied")).Join(context.Background(), "Office PC", "linux"); !errors.Is(err, ErrJoinDenied) {
		t.Errorf("denied: %v", err)
	}
	if _, err := c(fakeJoin(t, "expired")).Join(context.Background(), "Office PC", "linux"); !errors.Is(err, ErrJoinExpired) {
		t.Errorf("expired: %v", err)
	}
}

// A request the server dropped (at its cap, or after a restart) says so, and says to run the command again.
func TestJoinDroppedRequestSaysSo(t *testing.T) {
	srv := fakeJoin(t, "waiting", "gone")
	_, err := (&Client{Server: srv.URL, Version: "1.2.3"}).Join(context.Background(), "Office PC", "linux")
	if err == nil || err.Error() != "The server no longer has this join request (it restarted or dropped it). Run the command again." {
		t.Errorf("dropped: %v", err)
	}
}

// Ctrl+C (the context ends) stops the wait without a token.
func TestJoinStopsWhenTheContextEnds(t *testing.T) {
	srv := fakeJoin(t, "waiting")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := (&Client{Server: srv.URL, Version: "1.2.3"}).Join(ctx, "Office PC", "linux")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("want the context's end, got %v", err)
	}
}
