// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// seedOverview fills s with n deterministic jobs over three libraries (one nested in another), with requeues of the
// same path, every status, kept originals and outputs, and the events the overview reads. The same n always gives the
// same rows, so the overview's numbers can be compared between two builds of the code.
func seedOverview(t *testing.T, s *Store, n int) []Library {
	t.Helper()
	tmpl := rules.DefaultSelectors(3).Template()
	var libs []Library
	for _, root := range []string{"/m", "/m/sub", "/n"} {
		l, err := s.AddLibrary(filepath.Base(root), root, tmpl, true)
		if err != nil {
			t.Fatal(err)
		}
		libs = append(libs, l)
	}
	rnd := rand.New(rand.NewSource(7))
	base := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	stamp := func(d time.Duration) string { return base.Add(d).Format(time.RFC3339) }
	statuses := []Status{Done, Done, Done, Done, Done, Done, Done, Failed, Queued, Kept, Running}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		l := libs[rnd.Intn(len(libs))]
		if l.ID == libs[0].ID && rnd.Intn(4) == 0 { // by ID: on Windows AddLibrary stores the canonical path, not "/m"
			l = libs[1] // some /m files sit under /m/sub too: the same file across libraries
		}
		path := fmt.Sprintf("%s/d%d/f%d.mkv", l.Path, rnd.Intn(30), rnd.Intn(n/8+1))
		st := statuses[rnd.Intn(len(statuses))]
		before := int64(1000 + rnd.Intn(5000000))
		after := int64(0)
		output, gone, ogone, changed := "", "", "", 0
		created := stamp(time.Duration(i) * time.Minute)
		if st == Done {
			after = before/2 + int64(rnd.Intn(int(before)/2+1))
			if rnd.Intn(5) == 0 {
				after = before + 10 // grew
			}
			output = fmt.Sprintf("%s/shrunk/d%d/f%d.mkv", l.Path, rnd.Intn(30), i)
			if rnd.Intn(2) == 0 {
				output = path[:len(path)-4] + ".mkv" // replaced in place
			}
			if rnd.Intn(7) == 0 {
				gone = stamp(time.Duration(i) * time.Minute)
			}
			if rnd.Intn(9) == 0 {
				ogone = stamp(time.Duration(i) * time.Minute)
			}
			if rnd.Intn(11) == 0 {
				changed = 1
			}
		}
		res, err := tx.Exec(`INSERT INTO jobs(library,library_id,path,status,size_before,size_after,output_path,original_gone_at,output_gone_at,original_changed,created,updated)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, l.Path, l.ID, path, string(st), before, after, output, gone, ogone, changed, created, created)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		if st == Done {
			at := stamp(time.Duration(rnd.Intn(60*24*60)) * time.Minute)
			if _, err := tx.Exec(`INSERT INTO events(kind,job_id,at,from_status,to_status) VALUES('job',?,?,?,?)`, id, at, string(Running), string(Done)); err != nil {
				t.Fatal(err)
			}
		}
		if rnd.Intn(3) == 0 {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO files(path,size,mtime,codec,decision,reason) VALUES(?,?,?,?,?,?)`,
				path, before, int64(i), "h264", "queued", ""); err != nil {
				t.Fatal(err)
			}
		}
		if output != "" && rnd.Intn(3) == 0 {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO files(path,size,mtime,codec,decision,reason) VALUES(?,?,?,?,?,?)`,
				output, after, int64(i), "hevc", "skip: HEVC is already an efficient format", "already_hevc"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return libs
}

// overviewSnapshot is everything the overview reads on a cache miss, as JSON-able values.
type overviewSnapshot struct {
	Totals   Totals
	Days     []Day
	Saving   float64
	SavingOK bool
	PerGB    float64
	TimeOK   bool
	Done     int
	Libs     []LibraryTotals
}

func takeSnapshot(t *testing.T, s *Store, libs []Library) overviewSnapshot {
	t.Helper()
	var snap overviewSnapshot
	var err error
	if snap.Totals, err = s.TotalsOf(); err != nil {
		t.Fatal(err)
	}
	if snap.Days, err = s.Days(30, time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC), snap.Totals); err != nil {
		t.Fatal(err)
	}
	if snap.Saving, snap.SavingOK, snap.PerGB, snap.TimeOK, err = s.Averages(); err != nil {
		t.Fatal(err)
	}
	if snap.Done, err = s.DoneCount(); err != nil {
		t.Fatal(err)
	}
	for _, l := range libs {
		lt, err := s.LibraryTotalsOf(l)
		if err != nil {
			t.Fatal(err)
		}
		snap.Libs = append(snap.Libs, lt)
	}
	return snap
}

// The overview's numbers must not change when the SQL that computes them gets faster. The golden file was written
// by the code before the change; set OVERVIEW_GOLDEN_WRITE=1 to rewrite it (only from the code it describes).
func TestOverviewNumbersMatchGolden(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	libs := seedOverview(t, s, 3000)
	got, err := json.MarshalIndent(takeSnapshot(t, s, libs), "", " ")
	if err != nil {
		t.Fatal(err)
	}
	const golden = "testdata/overview_golden.json"
	if os.Getenv("OVERVIEW_GOLDEN_WRITE") == "1" {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		os.WriteFile(filepath.Join(t.TempDir(), "got.json"), got, 0o644)
		t.Errorf("overview numbers changed from the golden file (%d bytes want, %d got)", len(want), len(got))
	}
}

// PERF_200K=1 go test -run TestOverviewTimings: seeds 200k jobs and prints the cache-miss path in ms (best of 3),
// then the time to migrate a store that is one step behind (user_version 50).
func TestOverviewTimings(t *testing.T) {
	if os.Getenv("PERF_200K") != "1" {
		t.Skip("set PERF_200K=1 to time the overview at 200k jobs")
	}
	path := filepath.Join(t.TempDir(), "p.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	libs := seedOverview(t, s, 200000)
	s.db.Exec(`ANALYZE`)
	best := func(name string, f func()) time.Duration {
		var b time.Duration
		for i := 0; i < 3; i++ {
			start := time.Now()
			f()
			if d := time.Since(start); i == 0 || d < b {
				b = d
			}
		}
		fmt.Printf("%-28s best %8.1f ms\n", name, float64(b.Microseconds())/1000)
		return b
	}
	var total time.Duration
	total += best("TotalsOf", func() { s.TotalsOf() })
	tot, _ := s.TotalsOf()
	total += best("Days(30)", func() { s.Days(30, time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC), tot) })
	total += best("Averages", func() { s.Averages() })
	total += best("DoneCount", func() { s.DoneCount() })
	total += best("LibraryTotalsOf x3", func() {
		for _, l := range libs {
			s.LibraryTotalsOf(l)
		}
	})
	fmt.Printf("%-28s best %8.1f ms\n", "TOTAL cache-miss path", float64(total.Microseconds())/1000)

	// migration: rewind to step 50 (drop the step-51 index if it exists), then time the reopen
	s.Close()
	raw, err := openRawForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	raw.Exec(`DROP INDEX IF EXISTS jobs_done_path`)
	raw.Exec(`PRAGMA user_version = 50`)
	raw.Close()
	start := time.Now()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("%-28s %8.1f ms\n", "migration (reopen, 50->51)", float64(time.Since(start).Microseconds())/1000)
	s2.Close()
}

// openRawForTest opens the database file without the store's migrations, to rewind it.
func openRawForTest(path string) (*sql.DB, error) { return sql.Open("sqlite", path) }
