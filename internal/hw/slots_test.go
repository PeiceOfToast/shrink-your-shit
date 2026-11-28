// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package hw

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The graphics card's places are one count for the whole process (L-HW-8).

// With one place, a second job waits until the first gives it back, and the count stays 1 while it waits.
func TestGraphicsSlotLimitHoldsTheSecondJob(t *testing.T) {
	ctx := context.Background()
	before := Running()
	held, err := TakeSlot(ctx, 1)
	if err != nil || held == nil {
		t.Fatalf("first place: %v %v", held, err)
	}
	defer held.Release() // however the test ends, the card is free again
	if n := Running(); n != before+1 {
		t.Fatalf("places while the first place is held: %d, want %d", n, before+1)
	}

	taken := make(chan *Slot, 1)
	go func() {
		s, err := TakeSlot(ctx, 1)
		if err != nil {
			taken <- nil
			return
		}
		taken <- s
	}()
	select {
	case <-taken:
		t.Fatal("a second place was taken with the limit at 1")
	case <-time.After(100 * time.Millisecond):
	}
	if n := Running(); n != before+1 {
		t.Errorf("places while the second job waits: %d, want %d", n, before+1)
	}

	held.Release() // the first job's card encode ends: the second takes the place
	select {
	case s := <-taken:
		if s == nil {
			t.Fatal("the second place failed after the first was given back")
		}
		s.Release()
	case <-time.After(2 * time.Second):
		t.Fatal("the second job never got the place the first gave back")
	}
	if n := Running(); n != before {
		t.Errorf("places after both released: %d, want %d", n, before)
	}
}

// A job waiting for a place stops waiting when the job is stopped.
func TestAGraphicsSlotWaitEndsWhenTheJobStops(t *testing.T) {
	before := Running()
	held, err := TakeSlot(context.Background(), 1)
	if err != nil || held == nil {
		t.Fatalf("first place: %v %v", held, err)
	}
	defer held.Release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := TakeSlot(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting ended with %v, want context.Canceled", err)
	}
	if n := Running(); n != before+1 {
		t.Errorf("places while the first is held: %d, want %d", n, before+1)
	}
}

// Giving a place back twice is harmless: it is counted once, and the count never goes below where it started.
func TestGivingAPlaceBackTwiceChangesTheCountOnce(t *testing.T) {
	before := Running()
	slot, err := TakeSlot(context.Background(), -1)
	if err != nil || slot == nil {
		t.Fatalf("place: %v %v", slot, err)
	}
	if n := Running(); n != before+1 {
		t.Fatalf("places while held: %d, want %d", n, before+1)
	}
	slot.Release()
	slot.Release()
	if n := Running(); n != before {
		t.Errorf("places after two releases: %d, want %d", n, before)
	}
}

// Zero places means no card encode: the caller is told why and counts nothing.
func TestZeroPlacesTakeNothing(t *testing.T) {
	before := Running()
	slot, err := TakeSlot(context.Background(), 0)
	if !errors.Is(err, ErrNoGraphicsSlots) || slot != nil {
		t.Fatalf("TakeSlot(0) = %v, %v; want ErrNoGraphicsSlots and no place", slot, err)
	}
	if n := Running(); n != before {
		t.Errorf("places with the limit at 0: %d, want %d", n, before)
	}
}
