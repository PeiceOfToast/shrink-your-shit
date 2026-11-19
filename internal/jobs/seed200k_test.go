// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// TestSeed200k writes a real store with about 200k jobs at SEED_200K_OUT, for taking screenshots of the app on a
// realistic library. It goes through Open, so the store is at the current schema. It refuses an existing file.
//
//	SEED_200K_OUT=/abs/path/syp.db go test ./internal/jobs -run TestSeed200k -count=1
func TestSeed200k(t *testing.T) {
	out := os.Getenv("SEED_200K_OUT")
	if out == "" {
		t.Skip("set SEED_200K_OUT to an absolute path to write a 200k-job store there")
	}
	if !filepath.IsAbs(out) {
		t.Fatalf("SEED_200K_OUT %q must be an absolute path", out)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("%s already exists: refusing to overwrite it", out)
	}
	s, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	jobs := seedRealistic(t, s, 200000)
	fmt.Printf("seeded %s: %d jobs\n", out, jobs)
}

var (
	showWords  = []string{"Harbor", "Lantern", "Glass", "Iron", "Northern", "Quiet", "Hollow", "Second", "Orchard", "Tide", "Winter", "Paper"}
	showNouns  = []string{"Street", "Kitchen", "Crown", "Station", "Road", "Lights", "Engine", "Garden", "Ferry", "Signal", "Hours", "Bridge"}
	titleWords = []string{"The Long Night", "A Small Favour", "Return", "Ten Minutes", "Cold Open", "Dead Reckoning", "The Gift", "Low Tide", "Open Water", "Last Stop", "Spare Parts", "Night Shift"}
	filmWords  = []string{"Silver", "Winter", "Harbour", "Cartographer", "Last Summer", "The Lamplighter", "Blue Hour", "Marrow", "Static", "Garnet", "The Long Road", "Kestrel"}
)

// realisticName is the library-relative path of job i and whether its original is an .mkv (replaced in place) or
// not (converted beside it). Each library uses its own layout: TV is Show/Season NN/Show - SxxExx - Title, movies
// are Film (Year)/Film (Year), and the nested Kids library sits inside Movies.
func realisticName(rnd *rand.Rand, lib int, i int) (rel string, mkv bool) {
	switch lib {
	case 0: // TV
		show := showWords[rnd.Intn(len(showWords))] + " " + showNouns[rnd.Intn(len(showNouns))]
		season := 1 + rnd.Intn(6)
		ep := 1 + rnd.Intn(22)
		title := titleWords[rnd.Intn(len(titleWords))]
		mkv = rnd.Intn(3) != 0
		ext := ".mp4"
		if mkv {
			ext = ".mkv"
		}
		return fmt.Sprintf("%s/Season %02d/%s - S%02dE%02d - %s%s", show, season, show, season, ep, title, ext), mkv
	case 1: // Movies (top level)
		film := filmWords[rnd.Intn(len(filmWords))]
		year := 1970 + rnd.Intn(55)
		mkv = rnd.Intn(2) == 0
		ext := ".mp4"
		if mkv {
			ext = ".mkv"
		}
		return fmt.Sprintf("%s (%d)/%s (%d)%s", film, year, film, year, ext), mkv
	default: // Movies/Kids (nested)
		film := filmWords[rnd.Intn(len(filmWords))] + " for Kids"
		year := 2000 + rnd.Intn(25)
		mkv = rnd.Intn(2) == 0
		ext := ".mp4"
		if mkv {
			ext = ".mkv"
		}
		return fmt.Sprintf("%s (%d)/%s (%d)%s", film, year, film, year, ext), mkv
	}
}

// seedRealistic is seedOverview with realistic names, spread over about 90 days, plus skipped files (files rows
// with no job) for both outputs beside and in place. It returns the number of jobs written.
func seedRealistic(t *testing.T, s *Store, n int) int {
	t.Helper()
	tmpl := rules.DefaultSelectors(3).Template()
	roots := []string{"/srv/media/TV", "/srv/media/Movies", "/srv/media/Movies/Kids"}
	var libs []Library
	for _, root := range roots {
		l, err := s.AddLibrary(filepath.Base(root), root, tmpl, true)
		if err != nil {
			t.Fatal(err)
		}
		libs = append(libs, l)
	}
	rnd := rand.New(rand.NewSource(11))
	base := time.Date(2026, 8, 21, 6, 0, 0, 0, time.UTC)
	// a moment in the last ~90 days, and a job's own time (monotone in i, so created order follows id)
	spread := 90 * 24 * time.Hour
	stamp := func(d time.Duration) string { return base.Add(d).UTC().Format(time.RFC3339) }
	// 60% done, 12% failed, 8% kept, 8% queued, 12% running-or-other (folded into queued below)
	pickStatus := func() Status {
		switch v := rnd.Intn(100); {
		case v < 60:
			return Done
		case v < 72:
			return Failed
		case v < 80:
			return Kept
		default:
			return Queued
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		li := rnd.Intn(len(libs))
		l := libs[li]
		rel, mkv := realisticName(rnd, li, i)
		path := l.Path + "/" + rel
		st := pickStatus()
		before := int64(300_000_000 + rnd.Intn(6_000_000_000))
		after, output, gone, ogone, changed := int64(0), "", "", "", 0
		at := time.Duration(float64(spread) * float64(i) / float64(n)) // float: i*spread overflows int64 at 200k
		created := stamp(at)
		if st == Done {
			after = before/3 + int64(rnd.Intn(int(before)/2))
			if rnd.Intn(20) == 0 {
				after = before + 10_000 // a rare conversion that grew
			}
			if mkv {
				output = path // replaced in place
			} else {
				output = path[:len(path)-len(filepath.Ext(path))] + " (shrunk).mkv" // beside the original
			}
			if rnd.Intn(7) == 0 {
				gone = created
			}
			if rnd.Intn(9) == 0 {
				ogone = created
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
			done := stamp(at + time.Duration(rnd.Intn(600))*time.Minute)
			if _, err := tx.Exec(`INSERT INTO events(kind,job_id,at,from_status,to_status) VALUES('job',?,?,?,?)`, id, done, string(Running), string(Done)); err != nil {
				t.Fatal(err)
			}
		}
		if rnd.Intn(3) == 0 {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO files(path,size,mtime,codec,decision,reason) VALUES(?,?,?,?,?,?)`,
				path, before, int64(i), "h264", "queued", ""); err != nil {
				t.Fatal(err)
			}
		}
		if output != "" && output != path && rnd.Intn(3) == 0 {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO files(path,size,mtime,codec,decision,reason) VALUES(?,?,?,?,?,?)`,
				output, after, int64(i), "hevc", "skip: HEVC is already an efficient format", "already_hevc"); err != nil {
				t.Fatal(err)
			}
		}
		// a skipped file: no job, a files row whose decision is a skip
		if rnd.Intn(6) == 0 {
			skipped := fmt.Sprintf("%s/Skipped %03d/%s", l.Path, i%400, filepath.Base(path))
			if _, err := tx.Exec(`INSERT OR IGNORE INTO files(path,size,mtime,codec,decision,reason) VALUES(?,?,?,?,?,?)`,
				skipped, before/2, int64(i), "hevc", "skip: HEVC is already an efficient format", "already_hevc"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return n
}
