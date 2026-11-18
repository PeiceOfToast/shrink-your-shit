// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build windows

package jobs

import (
	"bytes"
	"testing"
)

// D3b: the share key is wrapped by DPAPI on disk and comes back unchanged for the same person.
func TestShareKeyDPAPIRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{7}, ShareKeySize)
	wrapped, err := wrapShareKey(key)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if bytes.Equal(wrapped, key) {
		t.Fatal("the key is stored unwrapped")
	}
	back, err := unwrapShareKey(wrapped)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if !bytes.Equal(back, key) {
		t.Fatalf("round trip changed the key")
	}
}
