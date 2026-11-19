// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package notify

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// A failure notification names files, never folders: each path in the Jobs sentence is cut to its last name, for
// Windows (\) and POSIX (/) separators alike.
func TestWithoutFoldersKeepsOnlyFileNames(t *testing.T) {
	dir := t.TempDir()
	cases := []struct{ in, want string }{
		{"Couldn't read " + filepath.Join(dir, "Movie (2020).mkv") + ": the file is missing.",
			"Couldn't read Movie (2020).mkv: the file is missing."},
		{"Couldn't write to " + filepath.Join(dir, "Movies") + ": permission denied.",
			"Couldn't write to Movies: permission denied."},
		{`The file at C:\Users\someone\private\a.mkv is not the one that was queued.`,
			"The file at a.mkv is not the one that was queued."},
		{"The file at /home/someone/private/a.mkv is not the one that was queued.",
			"The file at a.mkv is not the one that was queued."},
		{`Couldn't write to \\server\share\Out: permission denied.`, "Couldn't write to Out: permission denied."},
		{"Read and/or write. Retry.", "Read and/or write. Retry."},
	}
	for _, c := range cases {
		got := withoutFolders(c.in)
		if got != c.want {
			t.Errorf("withoutFolders(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The notification for each folder source names the file and no folder, with the Jobs sentence as the source.
func TestFailureNotificationHoldsNoFolder(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		jobsText string
		must     string
	}{
		{"Couldn't read " + filepath.Join(dir, "Movies", "Movie (2020).mkv") + ": the file is missing.", "Movie (2020).mkv"},
		{"Couldn't write to " + filepath.Join(dir, "Out") + ": permission denied.", "Couldn't write to Out"},
		{`The file at C:\Out\Movies\a.mkv is not the one that was queued.`, "a.mkv"},
	}
	for _, c := range cases {
		got := withoutFolders("Failed: a.mkv. " + c.jobsText)
		if !strings.Contains(got, c.must) {
			t.Errorf("notification %q does not name %q", got, c.must)
		}
		if strings.ContainsAny(got[len("Failed: a.mkv. "):], `/\`) {
			t.Errorf("notification %q still names a folder", got)
		}
	}
}

// A path the patterns can't cut (an unknown folder with spaces) never goes out: the notification falls back to the
// fixed words for the failure's code rather than send a folder name (review of #342).
func TestFailureSentenceFailsClosedOnALeftoverFolder(t *testing.T) {
	for _, text := range []string{
		"Couldn't read My Movies/Film.mkv: the file is missing.",
		`Couldn't write to My Movies\Out: permission denied.`,
		`The file at TV Shows\Ep.mkv is not the one that was queued.`,
	} {
		s := &Service{}
		s.opt.FailureText = func(int64) string { return text }
		got := s.failureSentence(jobs.Event{JobID: 1, Reason: jobs.ReasonCannotWrite})
		if want := failedWords(jobs.ReasonCannotWrite); got != want {
			t.Errorf("for %q: %q, want the fixed words %q", text, got, want)
		}
	}
	s := &Service{}
	s.opt.FailureText = func(int64) string { return "Couldn't read /srv/media/Film.mkv: the file is missing." }
	if got := s.failureSentence(jobs.Event{JobID: 1}); got != "Couldn't read Film.mkv: the file is missing." {
		t.Errorf("a cuttable path: %q", got)
	}
}
