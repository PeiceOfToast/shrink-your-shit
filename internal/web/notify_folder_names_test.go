// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A notification's sentence names files, never folders, even when a folder name has spaces: the job's own paths are
// replaced exactly before any pattern runs.
func TestNotificationSentenceNamesFilesWithSpacedFolders(t *testing.T) {
	_, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	tmp := t.TempDir()
	cases := []struct {
		reason, path, outputPath, stored, want string
		denied                                 bool // ffmpeg's own "Error opening output <path>: Permission denied"
	}{
		// a stored sentence with a spaced folder: the code is not in the table, so the stored words are shown
		{"stored_words", filepath.Join(tmp, "My Movies", "Film.mkv"), "",
			"Couldn't read " + filepath.Join(tmp, "My Movies", "Film.mkv") + ": the file is missing.",
			"Couldn't read Film.mkv: the file is missing.", false},
		{"stored_words", filepath.Join(tmp, "tv", "Show", "Season 1", "Ep 01.mkv"), "",
			"Couldn't read " + filepath.Join(tmp, "tv", "Show", "Season 1", "Ep 01.mkv") + ": the file is missing.",
			"Couldn't read Ep 01.mkv: the file is missing.", false},
		{"stored_words", `D:\TV Shows\Ep.mkv`, "",
			`The file at D:\TV Shows\Ep.mkv is not the one that was queued.`,
			"The file at Ep.mkv is not the one that was queued.", false},
		// the Convert again sentence names the file it found in place of the one it made, with spaces in its folder
		{"stored_words", filepath.Join(tmp, "Old Folder", "Film.mkv"), "",
			"The file at " + filepath.Join(tmp, "Old Folder", "Film.mkv") + " is not the one Shrink Your Shit made from this original, so it was not replaced. Nothing was changed. Move or rename it, then press Convert again.",
			"The file at Film.mkv is not the one Shrink Your Shit made from this original, so it was not replaced. Nothing was changed. Move or rename it, then press Convert again.", false},
		// a refused write names the output folder, which has spaces
		{jobs.ReasonCannotWrite, filepath.Join(tmp, "My Movies", "Film.mkv"), filepath.Join(tmp, "Out Folder", "Film.mkv"),
			"The file was not written.", "Couldn't write to Out Folder: permission denied.", true},
	}
	for _, c := range cases {
		id, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: c.path})
		st.Claim("w")
		if err := st.Fail(id, c.reason, c.stored); err != nil {
			t.Fatal(err)
		}
		if c.denied {
			line := "Error opening output " + c.outputPath + ": Permission denied"
			if err := st.SetFailureSummary(id, jobs.FailureSummary{ExitCode: -1, FfmpegRan: true, KeyLines: []string{line}}); err != nil {
				t.Fatal(err)
			}
		}
		got := FailureSentence(st, id)
		if got != c.want {
			t.Errorf("notification %q, want %q", got, c.want)
		}
		if strings.ContainsAny(got, `/\`) {
			t.Errorf("notification %q still names a folder", got)
		}
	}
}
