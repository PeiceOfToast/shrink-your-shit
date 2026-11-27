// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package events

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// Query hands back each row's id, in insertion order, so a stream can resume from the last id it sent.
func TestQuerySetsRowIDsInOrder(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	bus, err := New(Options{DataDir: dir, DB: db, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bus.Close(context.Background()) })
	for i := 0; i < 3; i++ {
		if err := bus.Emit(Event{Kind: KindNote, Level: LevelInfo, Source: "test", Message: "line"}); err != nil {
			t.Fatal(err)
		}
	}
	var got []Event
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, _, err = bus.Query(context.Background(), Filter{}, Page{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	for i, e := range got {
		if e.RowID == 0 || (i > 0 && e.RowID <= got[i-1].RowID) {
			t.Errorf("event %d row id = %d; want a non-zero id after %d", i, e.RowID, got[max(i-1, 0)].RowID)
		}
	}
}
