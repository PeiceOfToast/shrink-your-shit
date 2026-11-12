// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// User review 11-12 item 5: the overview's numbers, the same as the summary's and the Libraries page's;
// NET and GROSS apart; days before the history begins say so.
func TestV2Overview(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	p := func(n string) string { return filepath.Join(lib.Path, n) }
	st.PutFile(jobs.File{Path: p("a.mkv"), Size: 1000, Decision: "queued: big"})
	st.PutFile(jobs.File{Path: p("b.mkv"), Size: 500, Decision: "skip: already HEVC", Reason: rules.SkipAlreadyInFormat})
	st.PutFile(jobs.File{Path: p("c.mkv"), Size: 300, Decision: "queued: big"})
	a, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: p("a.mkv"), SizeBefore: 1000})
	st.Claim("w")
	st.Complete(a, p("a.mkv"), 400) // replaced, nothing kept: 600 freed
	st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: p("c.mkv"), SizeBefore: 300})
	st.Claim("w")
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/overview"})
	var o struct {
		Headline struct{ Freed, FilesConverted, FilesWaiting int64 }
		Trash    struct{ Bytes int64 }
		Workers  []struct{ Jobs []struct{ Name string } }
		Active   []struct {
			Name    string
			Library *struct{ Name string }
		}
		Libraries []struct {
			BytesBefore, BytesNow, Saved int64
			SavedPercent                 float64
			Counts                       map[string]int
		}
		Daily []struct {
			Day                        string
			Recorded                   bool
			Completed, Skipped, Failed int
			SavedBytes                 int64
		}
		TimeZone string
		Averages struct{ SavingPercent *float64 }
	}
	if err := json.Unmarshal([]byte(body), &o); err != nil {
		t.Fatal(err)
	}
	if o.Headline.Freed != 600 || o.Headline.FilesConverted != 1 || o.Headline.FilesWaiting != 0 || o.Trash.Bytes != 0 {
		t.Errorf("headline %s", body)
	}
	if len(o.Active) != 1 || o.Active[0].Name != "c.mkv" || o.Active[0].Library == nil || o.Active[0].Library.Name != "Movies" ||
		len(o.Workers) != 1 || len(o.Workers[0].Jobs) != 1 {
		t.Errorf("active, workers %s", body)
	}
	if len(o.Libraries) != 1 {
		t.Fatalf("%s", body)
	}
	l := o.Libraries[0]
	if l.BytesBefore != 1800 || l.BytesNow != 1200 || l.Saved != 600 || l.SavedPercent < 33.3 || l.SavedPercent > 33.4 ||
		l.Counts["done"] != 1 || l.Counts["skipped"] != 1 || l.Counts["waiting"] != 0 || l.Counts["failed"] != 0 {
		t.Errorf("library %+v", l)
	}
	if _, sb, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/summary"}); !strings.Contains(sb, `"freed":600`) {
		t.Errorf("the summary says otherwise: %s", sb)
	}
	if len(o.Daily) != 30 {
		t.Fatalf("days %d", len(o.Daily))
	}
	today, before := o.Daily[29], o.Daily[28]
	if !today.Recorded || today.Completed != 1 || today.SavedBytes != 600 || before.Recorded || before.Completed != 0 {
		t.Errorf("today %+v, the day before %+v", today, before)
	}
	if o.TimeZone == "" || o.Averages.SavingPercent == nil || *o.Averages.SavingPercent != 60 {
		t.Errorf("time zone %q, averages %v", o.TimeZone, o.Averages.SavingPercent)
	}
}
