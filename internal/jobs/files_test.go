// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The Files view's list (S5): each card's files, in its order, a page at a time, with every card's
// count under the same library and name filters.
func TestFileList(t *testing.T) {
	s := open(t)
	movies, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	shows, _ := s.AddLibrary("Shows", "/s", rules.SliderTemplate(3), true)
	add := func(lib Library, name string) int64 {
		id, err := s.Enqueue(Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, name)})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	run := func(want int64) {
		j, err := s.Claim("w")
		if err != nil || j.ID != want {
			t.Fatalf("claimed %d %v, want %d", j.ID, err, want)
		}
	}
	done, failed, kept, running := add(movies, "Done One.mkv"), add(movies, "bad.mkv"), add(movies, "checked.mkv"), add(shows, "Converting.mkv")
	run(done)
	s.Complete(done, "/m/Done One.mkv", 10)
	run(failed)
	s.Fail(failed, "broke")
	run(kept)
	s.Keep(kept, "a check said no")
	run(running)
	retry := add(movies, "retry.mkv")
	run(retry)
	s.RetryLater(retry, time.Now().Add(time.Hour), "busy")
	for i := 0; i < 3; i++ {
		add(movies, fmt.Sprintf("wait%d.mkv", i))
	}
	s.PutFile(File{Path: filepath.Join(movies.Path, "skip a.mkv"), Size: 1, Decision: "skip: x", Reason: "no_video"})
	s.PutFile(File{Path: filepath.Join(shows.Path, "skip b.mkv"), Size: 1, Decision: "skip: x", Reason: "no_video"})

	list := func(f FileFilter) ([]string, int, map[string]int) {
		t.Helper()
		if f.Limit == 0 {
			f.Limit = 50
		}
		rows, total, counts, err := s.FileList(f)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, r := range rows {
			names = append(names, filepath.Base(r.Path))
		}
		return names, total, counts
	}
	names, total, counts := list(FileFilter{Show: CardUnprocessed})
	if fmt.Sprint(names) != "[retry.mkv wait0.mkv wait1.mkv wait2.mkv]" || total != 4 {
		t.Errorf("unprocessed %v %d (retrying first)", names, total)
	}
	if fmt.Sprint(counts) != "map[failed:2 keptOriginals:0 processed:1 processing:1 skipped:2 unprocessed:4]" {
		t.Errorf("counts %v", counts)
	}
	if names, total, _ := list(FileFilter{Show: CardUnprocessed, Limit: 2, Offset: 2}); fmt.Sprint(names) != "[wait1.mkv wait2.mkv]" || total != 4 {
		t.Errorf("page 2: %v %d", names, total)
	}
	if names, _, _ := list(FileFilter{Show: CardFailed}); len(names) != 2 {
		t.Errorf("failed (and kept by a check) %v", names)
	}
	// library and name filters apply to the list and to every count
	if names, total, counts := list(FileFilter{Show: CardSkipped, LibraryID: shows.ID}); fmt.Sprint(names) != "[skip b.mkv]" || total != 1 || counts[CardProcessing] != 1 || counts[CardUnprocessed] != 0 {
		t.Errorf("shows: %v %d %v", names, total, counts)
	}
	if names, _, counts := list(FileFilter{Show: CardProcessed, Q: "DONE"}); fmt.Sprint(names) != "[Done One.mkv]" || counts[CardUnprocessed] != 0 || counts[CardSkipped] != 0 {
		t.Errorf("q: %v %v", names, counts)
	}
	if names, _, _ := list(FileFilter{Show: CardUnprocessed, Q: "/m"}); len(names) != 0 { // only in the folder, never a name
		t.Errorf("q matched a folder: %v", names)
	}
	if _, _, _, err := s.FileList(FileFilter{Show: "nope", Limit: 1}); err == nil {
		t.Error("an unknown card")
	}
}

// Review 43e6f66: the skipped card takes reason=a,b; its library filter works with Windows paths; the
// processing card is in start order (not last progress).
func TestFileListReviewFixes(t *testing.T) {
	s := open(t)
	lib, _ := s.AddLibrary("Media", `C:\Media`, rules.SliderTemplate(3), true)
	s.db.Exec(`UPDATE libraries SET path=? WHERE id=?`, `C:\Media`, lib.ID) // as a Windows install stores it (this test runs on Linux)
	s.PutFile(File{Path: `C:\Media\a.mkv`, Size: 1, Decision: "skip: x", Reason: "already_hevc"})
	s.PutFile(File{Path: `C:\Media\b.mkv`, Size: 1, Decision: "skip: x", Reason: "no_video"})
	s.PutFile(File{Path: `C:\Other\c.mkv`, Size: 1, Decision: "skip: x", Reason: "already_hevc"})
	rows, total, counts, err := s.FileList(FileFilter{Show: CardSkipped, LibraryID: lib.ID, Reasons: []string{"already_hevc", "already_in_format"}, Limit: 50})
	if err != nil || total != 1 || len(rows) != 1 || rows[0].Path != `C:\Media\a.mkv` || counts[CardSkipped] != 1 {
		t.Errorf("windows + reasons: %v %d %v %v", rows, total, counts, err)
	}
	a, _ := s.Enqueue(Job{Library: `C:\Media`, LibraryID: lib.ID, Path: `C:\Media\x.mkv`})
	b, _ := s.Enqueue(Job{Library: `C:\Media`, LibraryID: lib.ID, Path: `C:\Media\y.mkv`})
	s.Claim("w")
	s.Claim("w")
	s.db.Exec(`UPDATE jobs SET started='2026-11-07T10:00:00Z' WHERE id=?`, a)
	s.db.Exec(`UPDATE jobs SET started='2026-11-07T11:00:00Z' WHERE id=?`, b)
	s.Progress(a, 50) // the earlier one reports progress last
	rows, _, _, _ = s.FileList(FileFilter{Show: CardProcessing, Limit: 50})
	if len(rows) != 2 || rows[0].JobID != a {
		t.Errorf("processing order %v", rows)
	}
}

// Every way a job is removed tells OnJobsRemoved (its run log goes) and drops its steps.
func TestJobsRemovedHook(t *testing.T) {
	s := open(t)
	var gone []int64
	s.OnJobsRemoved = func(ids []int64) { gone = append(gone, ids...) }
	lib, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	stopped, _ := s.Enqueue(Job{Library: "/m", LibraryID: lib.ID, Path: "/m/a.mkv"})
	s.AddStep(stopped, "found", true, "x")
	s.RequestStop(stopped) // queued: removed at once
	skipped, _ := s.Enqueue(Job{Library: "/m", LibraryID: lib.ID, Path: "/m/b.mkv"})
	s.Skip(skipped, "x", "dv")
	userStopped, _ := s.Enqueue(Job{Library: "/m", LibraryID: lib.ID, Path: "/m/c.mkv"})
	s.UserStopped(userStopped)
	withLib, _ := s.Enqueue(Job{Library: "/m", LibraryID: lib.ID, Path: "/m/d.mkv"})
	s.DeleteLibrary(lib.ID)
	if fmt.Sprint(gone) != fmt.Sprint([]int64{stopped, skipped, userStopped, withLib}) {
		t.Errorf("told %v", gone)
	}
	if steps, _ := s.Steps(stopped); len(steps) != 0 {
		t.Errorf("steps kept %v", steps)
	}
}
