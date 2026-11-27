package events

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

// queryFixture stores six events in job 5, seq 1..6, whose attempt, source, step and time differ, so each
// filter must pick a different subset. The messages name the events: m1..m6.
func queryFixture(t *testing.T) *Bus {
	t.Helper()
	db := openTestDB(t)
	b, err := New(Options{Clock: clockFn(), DB: db})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 27, 4, 0, 0, 0, time.UTC)
	rows := []struct {
		msg, source, step string
		attempt           int
		at                time.Duration
	}{
		{"m1", "worker", "s1", 1, 0},
		{"m2", "api", "s1", 1, 1 * time.Second},
		{"m3", "worker", "s2", 2, 2 * time.Second},
		{"m4", "api", "s2", 2, 3 * time.Second},
		{"m5", "worker", "s1", 2, 4 * time.Second},
		{"m6", "api", "s2", 1, 5 * time.Second},
	}
	for _, r := range rows {
		e := Event{
			JobID: 5, Attempt: r.attempt, Kind: KindNote, Level: LevelInfo, Message: r.msg,
			Source: r.source, StepID: r.step, TS: base.Add(r.at),
		}
		if err := b.Emit(e); err != nil {
			t.Fatal(err)
		}
	}
	closeBus(t, b)
	return b
}

func messages(evs []Event) string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Message)
	}
	return strings.Join(out, ",")
}

// Each new filter narrows the result to the events it names, and two filters together narrow further.
func TestQueryFiltersNarrow(t *testing.T) {
	b := queryFixture(t)
	base := time.Date(2026, 11, 27, 4, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		f    Filter
		want string
	}{
		{"all of job 5", Filter{JobID: 5}, "m1,m2,m3,m4,m5,m6"},
		{"attempt 2", Filter{JobID: 5, Attempt: 2}, "m3,m4,m5"},
		{"source api", Filter{JobID: 5, Source: "api"}, "m2,m4,m6"},
		{"step s2", Filter{JobID: 5, StepID: "s2"}, "m3,m4,m6"},
		{"since 1s until 3s, inclusive", Filter{JobID: 5, Since: base.Add(time.Second), Until: base.Add(3 * time.Second)}, "m2,m3,m4"},
		{"after seq 4", Filter{JobID: 5, AfterSeq: 4}, "m5,m6"},
		{"source api and attempt 2", Filter{JobID: 5, Source: "api", Attempt: 2}, "m4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, err := b.Query(context.Background(), c.f, Page{Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			if s := messages(got); s != c.want {
				t.Fatalf("got %s, want %s", s, c.want)
			}
		})
	}
}

// Paging walks by seq: two events a page, then the next page starts after the seq of the last one.
func TestQueryPagesBySeq(t *testing.T) {
	b := queryFixture(t)
	ctx := context.Background()
	first, p, err := b.Query(ctx, Filter{JobID: 5, Source: "api"}, Page{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if s := messages(first); s != "m2,m4" || p.Cursor != 4 {
		t.Fatalf("first page %s with cursor %d; want m2,m4 with cursor 4", s, p.Cursor)
	}
	second, p, err := b.Query(ctx, Filter{JobID: 5, Source: "api"}, Page{Limit: 2, Cursor: p.Cursor})
	if err != nil {
		t.Fatal(err)
	}
	if s := messages(second); s != "m6" || p.Cursor != 6 {
		t.Fatalf("second page %s with cursor %d; want m6 with cursor 6", s, p.Cursor)
	}
	newest, _, err := b.Query(ctx, Filter{JobID: 5, Source: "api"}, Page{Limit: 2, Newest: true})
	if err != nil {
		t.Fatal(err)
	}
	if s := messages(newest); s != "m6,m4" {
		t.Fatalf("newest first %s; want m6,m4", s)
	}
}

// Progress is for live subscribers only: a progress event with a job id never reaches the table.
func TestProgressNeverReachesTheDatabase(t *testing.T) {
	db := openTestDB(t)
	b, err := New(Options{Clock: clockFn(), DB: db})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Emit(Event{JobID: 9, Kind: KindNote, Level: LevelInfo, Message: "kept"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Emit(Event{JobID: 9, Kind: KindProgress, Level: LevelInfo, Message: "progress 40%"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Emit(Event{JobID: 9, Kind: KindProgress, Level: LevelInfo, Message: "progress 80%"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Emit(Event{JobID: 9, Kind: KindNote, Level: LevelInfo, Message: "also kept"}); err != nil {
		t.Fatal(err)
	}
	closeBus(t, b)

	got, _, err := b.Query(context.Background(), Filter{JobID: 9}, Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if s := messages(got); s != "kept,also kept" {
		t.Fatalf("job 9 in the table: %s; want kept,also kept (no progress)", s)
	}
	for _, e := range got {
		if e.Kind == KindProgress {
			t.Fatalf("a progress event was stored: %+v", e)
		}
	}
}

// MinLevel keeps warn and error only; an unknown level is refused by Query and matches nothing for a subscriber.
func TestQueryMinLevelAndKind(t *testing.T) {
	db := openTestDB(t)
	b, err := New(Options{Clock: clockFn(), DB: db})
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		msg   string
		level Level
		kind  Kind
	}{
		{"i1", LevelInfo, KindNote},
		{"w1", LevelWarn, KindWarning},
		{"e1", LevelError, KindError},
		{"w2", LevelWarn, KindNote},
	}
	for _, r := range rows {
		if err := b.Emit(Event{JobID: 6, Level: r.level, Kind: r.kind, Message: r.msg}); err != nil {
			t.Fatal(err)
		}
	}
	closeBus(t, b)
	ctx := context.Background()

	got, _, err := b.Query(ctx, Filter{JobID: 6, MinLevel: LevelWarn}, Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if s := messages(got); s != "w1,e1,w2" {
		t.Fatalf("min level warn: %s; want w1,e1,w2", s)
	}
	got, _, err = b.Query(ctx, Filter{JobID: 6, Kind: KindNote}, Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if s := messages(got); s != "i1,w2" {
		t.Fatalf("kind note: %s; want i1,w2", s)
	}
	if _, _, err := b.Query(ctx, Filter{JobID: 6, MinLevel: "warning"}, Page{Limit: 100}); err == nil {
		t.Fatal("Query accepted the unknown level warning")
	}
}

// A subscriber given an unknown MinLevel gets nothing, not everything.
func TestSubscribeUnknownLevelMatchesNothing(t *testing.T) {
	f := Filter{MinLevel: "warning"}
	if f.match(Event{Level: LevelError, Kind: KindError}) {
		t.Fatal("an unknown MinLevel matched an error event")
	}
}

// After run_end the job's seq continues on the same bus, and the next event does not wait for the table: the
// store's only writer is held here, so the db sink cannot commit, and the note must still be emitted at once.
func TestSeqContinuesAfterRunEnd(t *testing.T) {
	db := openTestDB(t)
	b, err := New(Options{Clock: clockFn(), DB: db})
	if err != nil {
		t.Fatal(err)
	}
	held := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = db.Tx(context.Background(), func(*sql.Tx) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	for _, k := range []Kind{KindRunStart, KindRunEnd} {
		if err := b.Emit(Event{JobID: 7, Kind: k, Level: LevelInfo, Message: string(k)}); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() { done <- b.Emit(Event{JobID: 7, Kind: KindNote, Level: LevelInfo, Message: "after"}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("the event after run_end waited on the held writer")
	}
	close(release)
	closeBus(t, b)

	got, _, err := b.Query(context.Background(), Filter{JobID: 7}, Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var seqs []int64
	for _, e := range got {
		seqs = append(seqs, e.Seq)
	}
	if fmt.Sprint(seqs) != "[1 2 3]" {
		t.Fatalf("seqs after run_end: %v; want [1 2 3]", seqs)
	}
}

// An unscoped walk (no JobID) pages by row id, because seq is per job and per process. Two jobs and an app
// event each start at seq 1, so paging by seq would skip rows. Every event must come back once, both ways.
func TestUnscopedWalkReturnsEveryEventOnce(t *testing.T) {
	db := openTestDB(t)
	b, err := New(Options{Clock: clockFn(), DB: db})
	if err != nil {
		t.Fatal(err)
	}
	emit := func(job int64, msg string) {
		if err := b.Emit(Event{JobID: job, Kind: KindNote, Level: LevelInfo, Message: msg}); err != nil {
			t.Fatal(err)
		}
	}
	emit(5, "j5-1")
	emit(5, "j5-2")
	emit(5, "j5-3")
	emit(6, "j6-1")
	emit(0, "app-1")
	closeBus(t, b)

	for _, newest := range []bool{false, true} {
		var got []string
		page := Page{Limit: 2, Newest: newest}
		for {
			evs, next, err := b.Query(context.Background(), Filter{}, page)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, messages(evs))
			if len(evs) < page.Limit {
				break
			}
			page.Cursor = next.Cursor
		}
		seen := map[string]int{}
		total := 0
		for _, s := range got {
			for _, m := range strings.Split(s, ",") {
				if m == "" {
					continue
				}
				seen[m]++
				total++
			}
		}
		if total != 5 || len(seen) != 5 {
			t.Fatalf("newest=%v: pages %v; want 5 distinct events once each", newest, got)
		}
		for _, m := range []string{"j5-1", "j5-2", "j5-3", "j6-1", "app-1"} {
			if seen[m] != 1 {
				t.Fatalf("newest=%v: %s returned %d times; pages %v", newest, m, seen[m], got)
			}
		}
	}
}
