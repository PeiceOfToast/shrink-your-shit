package events

import (
	"context"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func jobNote(job int64, msg string) Event {
	return Event{JobID: job, Kind: KindNote, Level: LevelInfo, Message: msg}
}

func closeBus(t *testing.T, b *Bus) {
	t.Helper()
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDBSinkWritesQueriesAndSeeds(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	b, err := New(Options{Clock: clockFn(), DB: db})
	if err != nil {
		t.Fatal(err)
	}
	// Job 7 runs to its end, so its counter is dropped and the next run re-seeds it from the table.
	for _, m := range []string{"a", "b", "c"} {
		if err := b.Emit(jobNote(7, m)); err != nil {
			t.Fatal(err)
		}
	}
	end := jobNote(7, "done")
	end.Kind = KindRunEnd
	if err := b.Emit(end); err != nil {
		t.Fatal(err)
	}
	// Job 8 is still running when the bus closes.
	if err := b.Emit(jobNote(8, "x")); err != nil {
		t.Fatal(err)
	}
	if err := b.Emit(jobNote(8, "y")); err != nil {
		t.Fatal(err)
	}
	closeBus(t, b)

	// Pages of two, oldest first: job 7 has seq 1..4.
	page1, p, err := b.Query(ctx, Filter{JobID: 7}, Page{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	page2, p, err := b.Query(ctx, Filter{JobID: 7}, Page{Limit: 2, Cursor: p.Cursor})
	if err != nil {
		t.Fatal(err)
	}
	page3, _, err := b.Query(ctx, Filter{JobID: 7}, Page{Limit: 2, Cursor: p.Cursor})
	if err != nil {
		t.Fatal(err)
	}
	got := append(append([]Event{}, page1...), page2...)
	if len(got) != 4 || len(page3) != 0 {
		t.Fatalf("job 7 pages: got %d events then %d, want 4 then 0", len(got), len(page3))
	}
	for i, e := range got {
		if e.Seq != int64(i+1) {
			t.Fatalf("job 7 event %d has seq %d, want %d", i, e.Seq, i+1)
		}
	}
	if got[3].Kind != KindRunEnd || got[0].Message != "a" {
		t.Fatalf("job 7 order or content wrong: first %q, last kind %q", got[0].Message, got[3].Kind)
	}

	// A new bus on the same database continues each job's seq from the table.
	b2, err := New(Options{Clock: clockFn(), DB: db})
	if err != nil {
		t.Fatal(err)
	}
	if err := b2.Emit(jobNote(7, "again")); err != nil {
		t.Fatal(err)
	}
	if err := b2.Emit(jobNote(8, "z")); err != nil {
		t.Fatal(err)
	}
	closeBus(t, b2)

	again, _, err := b2.Query(ctx, Filter{JobID: 7}, Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 5 || again[4].Seq != 5 {
		t.Fatalf("job 7 after restart: %d events, last seq %d; want 5 events, last seq 5", len(again), again[len(again)-1].Seq)
	}
	eight, _, err := b2.Query(ctx, Filter{JobID: 8}, Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(eight) != 3 || eight[2].Seq != 3 {
		t.Fatalf("job 8 after restart: %d events, last seq %d; want 3 events, last seq 3", len(eight), eight[len(eight)-1].Seq)
	}
}

func TestQueryWithoutDatabaseFails(t *testing.T) {
	b, err := New(Options{Clock: clockFn()})
	if err != nil {
		t.Fatal(err)
	}
	defer closeBus(t, b)
	if _, _, err := b.Query(context.Background(), Filter{}, Page{}); err == nil {
		t.Fatal("Query with no database: want an error")
	}
}
