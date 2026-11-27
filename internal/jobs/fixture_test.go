// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// Test-only stand-in for R1-3's migration 4 (libraries and files). It has only the columns jobs reads. Swap it for
// the real migration when R1-3 merges.
func init() {
	store.Register(store.Migration{Version: 4, Name: "fixture libraries and files", Up: func(tx *sql.Tx) error {
		for _, s := range []string{
			`CREATE TABLE libraries (id TEXT PRIMARY KEY, paused INTEGER NOT NULL DEFAULT 0)`,
			`CREATE TABLE files (id TEXT PRIMARY KEY, library_id TEXT NOT NULL)`,
		} {
			if _, err := tx.Exec(s); err != nil {
				return err
			}
		}
		return nil
	}})
}

// testEnv is a migrated store, a bus and a queue whose clock the test controls.
type testEnv struct {
	db  *store.DB
	bus *events.Bus
	q   *Queue
	clk *fakeClock
}

type fakeClock struct{ ms int64 }

func (c *fakeClock) now() time.Time { return fromMS(c.ms) }

func (c *fakeClock) advance(d time.Duration) { c.ms += d.Milliseconds() }

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	clk := &fakeClock{ms: 1_800_000_000_000}
	bus, err := events.New(events.Options{Clock: clk.now, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bus.Close(context.Background()) })
	return &testEnv{db: db, bus: bus, q: New(Options{DB: db, Bus: bus, Clock: clk.now}), clk: clk}
}

// exec runs one write in its own transaction.
func (e *testEnv) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if err := e.db.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(q, args...)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// addLibrary inserts a library row, paused or not.
func (e *testEnv) addLibrary(t *testing.T, id string, paused bool) {
	t.Helper()
	e.exec(t, `INSERT INTO libraries (id, paused) VALUES (?, ?)`, id, b2i(paused))
}

// setPaused flips a library's paused flag.
func (e *testEnv) setPaused(t *testing.T, id string, paused bool) {
	t.Helper()
	e.exec(t, `UPDATE libraries SET paused = ? WHERE id = ?`, b2i(paused), id)
}

// enqueue is Enqueue with a fixed workflow, failing the test on error or on an already-open file.
func (e *testEnv) enqueue(t *testing.T, file, library string) int64 {
	t.Helper()
	id, open, err := e.q.Enqueue(context.Background(), file, library, "wf-1", "/media/"+file+".mkv", "test")
	if err != nil {
		t.Fatal(err)
	}
	if open {
		t.Fatalf("enqueue %s: unexpectedly already open", file)
	}
	return id
}

// stateOf reads a job's raw state.
func (e *testEnv) stateOf(t *testing.T, id int64) State {
	t.Helper()
	var s string
	if err := e.db.Read().QueryRow(`SELECT state FROM jobs WHERE id = ?`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return State(s)
}

// setState writes a state directly. Only tests use it, to reach a state no verb reaches on its own.
func (e *testEnv) setState(t *testing.T, id int64, s State) {
	t.Helper()
	e.exec(t, `UPDATE jobs SET state = ? WHERE id = ?`, string(s), id)
}

func (e *testEnv) countJobs(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.db.Read().QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
