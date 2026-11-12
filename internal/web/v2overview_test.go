// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
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
	c, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: p("c.mkv"), SizeBefore: 300})
	st.Claim("w")
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/overview"})
	var o struct {
		Headline struct{ Freed, FilesConverted, FilesWaiting, Grew int64 }
		Trash    struct{ Bytes int64 }
		Workers  []struct{ Jobs []struct{ Name string } }
		Active   []struct {
			Name    string
			Library *struct{ Name string }
		}
		Libraries []struct {
			BytesBefore, BytesNow, Saved, Grew int64
			SavedPercent                       float64
			Counts                             map[string]int
		}
		Daily []struct {
			Day                        string
			Recorded                   bool
			Completed, Skipped, Failed int
			FreedBytes                 int64
		}
		TimeZone string
		Averages struct{ FileShrinkPercent *float64 }
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
	if !today.Recorded || today.Completed != 1 || today.FreedBytes != 600 || before.Recorded || before.Completed != 0 {
		t.Errorf("today %+v, the day before %+v", today, before)
	}
	if o.TimeZone == "" || o.Averages.FileShrinkPercent == nil || *o.Averages.FileShrinkPercent != 60 {
		t.Errorf("time zone %q, averages %v", o.TimeZone, o.Averages.FileShrinkPercent)
	}
	// c.mkv 300 -> 400: kept as it is, saves nothing, and its 100 bytes of growth show as grew (headline and library)
	st.Complete(c, p("c.mkv"), 400)
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/overview"})
	if err := json.Unmarshal([]byte(body), &o); err != nil {
		t.Fatal(err)
	}
	if o.Headline.Grew != 100 || o.Headline.Freed != 600 || len(o.Libraries) != 1 || o.Libraries[0].Grew != 100 || o.Libraries[0].Saved != 600 {
		t.Errorf("grew: headline %+v, library %+v: %s", o.Headline, o.Libraries, body)
	}
}

// Review ab99630: the day's freed bytes are NET (a kept original, an original left in place: not freed);
// a library inside another counts its files once (the inner one's); the time zone is its IANA name.
func TestV2OverviewNetAndNested(t *testing.T) {
	t.Setenv("TZ", "Europe/Berlin")
	srv, st, _, _ := libServer(t, false)
	outer, _ := st.AddLibrary("All", t.TempDir(), rules.SliderTemplate(3), true)
	innerDir := filepath.Join(outer.Path, "kids")
	os.Mkdir(innerDir, 0o755)
	inner, err := st.AddLibrary("Kids", innerDir, rules.SliderTemplate(3), true)
	if err != nil {
		t.Skipf("a library inside another isn't allowed here: %v", err)
	}
	st.PutFile(jobs.File{Path: filepath.Join(outer.Path, "a.mkv"), Size: 1000, Decision: "queued: x"})
	st.PutFile(jobs.File{Path: filepath.Join(innerDir, "b.mkv"), Size: 500, Decision: "queued: x"})
	// an original left in place (a folder for new files): could free, not freed
	id, _ := st.Enqueue(jobs.Job{Library: outer.Path, LibraryID: outer.ID, Path: filepath.Join(outer.Path, "a.mkv"), SizeBefore: 1000})
	st.Claim("w")
	st.Complete(id, filepath.Join(outer.Path+" (shrunk)", "a.mkv"), 400)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/overview"})
	var o struct {
		Libraries []struct {
			ID          string
			BytesBefore int64
		}
		Daily    []struct{ FreedBytes int64 }
		TimeZone string
	}
	json.Unmarshal([]byte(body), &o)
	byID := map[string]int64{}
	for _, l := range o.Libraries {
		byID[l.ID] = l.BytesBefore
	}
	if byID[strconv.FormatInt(outer.ID, 10)] != 1000 || byID[strconv.FormatInt(inner.ID, 10)] != 500 {
		t.Errorf("nested counted once: %s", body)
	}
	if o.Daily[29].FreedBytes != 0 {
		t.Errorf("nothing was freed today: %+v", o.Daily[29])
	}
	if o.TimeZone != "Europe/Berlin" {
		t.Errorf("time zone %q", o.TimeZone)
	}
}

// #177 review (orch-lead 18:49Z): with a paired worker converting, the overview lists every worker of
// GET /workers, each with only its own jobs; the summary lists the same workers and counts both running.
func TestOverviewAndSummaryListEveryWorker(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	code, _, _ := st.NewPairing(time.Now())
	rw, _, err := st.Pair(code, "Desktop", "1", WorkerAPILevel, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	here, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "here.mkv"), SizeBefore: 1000})
	there, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "there.mkv"), SizeBefore: 1000})
	if j, _ := st.Claim("builtin"); j.ID != here {
		t.Fatalf("local claimed %d", j.ID)
	}
	if j, _ := st.Claim(rw.ID); j.ID != there {
		t.Fatalf("remote claimed %d", j.ID)
	}
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/overview"})
	var o struct {
		Workers []struct {
			ID, Kind string
			Jobs     []struct{ Name string }
		}
	}
	json.Unmarshal([]byte(body), &o)
	jobsOf := map[string][]string{}
	for _, w := range o.Workers {
		for _, j := range w.Jobs {
			jobsOf[w.Kind+":"+w.ID] = append(jobsOf[w.Kind+":"+w.ID], j.Name)
		}
	}
	if len(o.Workers) != 2 || strings.Join(jobsOf["local:"+jobs.LocalWorker], ",") != "here.mkv" || strings.Join(jobsOf["remote:"+rw.ID], ",") != "there.mkv" {
		t.Errorf("overview workers %v: %s", jobsOf, body)
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/summary"})
	var s struct {
		Workers  []struct{ ID string }
		Activity struct{ WorkersRunning int }
	}
	json.Unmarshal([]byte(body), &s)
	if len(s.Workers) != 2 || s.Workers[1].ID != rw.ID || s.Activity.WorkersRunning != 2 {
		t.Errorf("summary workers %+v running %d", s.Workers, s.Activity.WorkersRunning)
	}
}

// A paired remote converting: the local worker lists only its own job in currentJobIds (a remote's
// job, run here once more, is that worker's) and its slots are 1 of 1, not 2 of 1 (#175, after the
// overview branch had merged an older dev).
func TestLocalWorkerListsOnlyItsOwnJobs(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	code, _, _ := st.NewPairing(time.Now())
	rw, _, err := st.Pair(code, "Desktop", "1", WorkerAPILevel, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	here, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "here.mkv"), SizeBefore: 1000})
	there, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "there.mkv"), SizeBefore: 1000})
	st.Claim("builtin")
	st.Claim(rw.ID)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workers"})
	var w struct {
		Items []v2Worker
	}
	json.Unmarshal([]byte(body), &w)
	if len(w.Items) != 2 {
		t.Fatalf("workers %s", body)
	}
	local := w.Items[0]
	if len(local.CurrentJobIDs) != 1 || local.CurrentJobIDs[0] != strconv.FormatInt(here, 10) {
		t.Errorf("currentJobIds %v (want job %d)", local.CurrentJobIDs, here)
	}
	if local.Slots.CPU.Used != 1 || local.Slots.CPU.Total != 1 {
		t.Errorf("slots %+v", local.Slots)
	}
	if len(w.Items[1].CurrentJobIDs) != 1 || w.Items[1].CurrentJobIDs[0] != strconv.FormatInt(there, 10) {
		t.Errorf("the remote's job is not on its own row: %v", w.Items[1].CurrentJobIDs)
	}
}

// A remote job's progress is what its worker reported: its report goes into the Live entry the run
// set up (0% at its start), so the overview, the Jobs page and the events stream all show it, and it rises.
func TestRemoteJobsProgressIsTheReportedOne(t *testing.T) {
	srv, st, a, _ := libServer(t, true)
	person := a.Token()
	code, _, _ := st.NewPairing(time.Now())
	rw, token, err := st.Pair(code, "Desktop", "1", WorkerAPILevel, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "there.mkv"), SizeBefore: 1000})
	c, b := workerCall(t, srv, "lease", token, `{"slots":1}`, WorkerAPILevel)
	var l struct{ Lease struct{ ID string } }
	json.Unmarshal([]byte(b), &l)
	if c != 200 || l.Lease.ID == "" {
		t.Fatalf("lease %d %s", c, b)
	}
	worker.SetLiveForTest(id, worker.LiveJob{Worker: rw.ID}) // what a run puts there when it starts: 0%
	defer worker.DropLiveForTest(id)
	for _, pct := range []float64{40, 55} {
		body := fmt.Sprintf(`{"stage":"converting","outTimeUs":%v,"percent":%v}`, pct*1e4, pct)
		if c, b := workerCall(t, srv, "leases/"+l.Lease.ID+"/progress", token, body, WorkerAPILevel); c != 200 {
			t.Fatalf("progress %d %s", c, b)
		}
		if live, ok := worker.Live(id); !ok || live.Progress.Percent != pct {
			t.Errorf("Live %v %v, want %v", ok, live.Progress.Percent, pct)
		}
		if p := livePercent(id); p == nil || *p != pct { // the events stream's job.progress
			t.Errorf("job.progress percent %v, want %v", p, pct)
		}
		_, ob, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/overview", bearer: person})
		var o struct {
			Workers []struct {
				Kind string
				Jobs []struct {
					JobID   string
					Percent *float64
				}
			}
		}
		json.Unmarshal([]byte(ob), &o)
		found := false
		for _, w := range o.Workers {
			for _, j := range w.Jobs {
				if j.JobID == strconv.FormatInt(id, 10) {
					found = true
					if j.Percent == nil || *j.Percent != pct {
						t.Errorf("overview percent %v, want %v: %s", j.Percent, pct, ob)
					}
				}
			}
		}
		if !found {
			t.Errorf("the remote job is not on the overview: %s", ob)
		}
	}
}
