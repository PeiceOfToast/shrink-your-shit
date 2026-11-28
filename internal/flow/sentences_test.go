// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

func TestSizeSentenceShowsTheSaving(t *testing.T) {
	cases := []struct {
		before, after int64
		want          string
	}{
		{1_200_000_000, 400_000_000, "1.2 GB → 400.0 MB (-67 %)"},
		{1_000_000, 1_000_000, "1.0 MB → 1.0 MB (+0 %)"},
		{0, 500, "0 B → 500 B"},
	}
	for _, c := range cases {
		if got := sizeSentence(c.before, c.after); got != c.want {
			t.Errorf("sizeSentence(%d, %d) = %q, want %q", c.before, c.after, got, c.want)
		}
	}
}

func TestProbeSentenceNamesTheMovieNotTheCoverArt(t *testing.T) {
	info := media.Info{
		Format: media.Format{Duration: 4 * time.Second},
		Video: []media.VideoTrack{
			{Codec: "mjpeg", AttachedPicture: true, Width: 300, Height: 300},
			{Codec: "h264", Width: 1920, Height: 1080},
		},
	}
	if got, want := probeSentence(info), "H.264 1920×1080, 4 s"; got != want {
		t.Errorf("probeSentence = %q, want %q", got, want)
	}
}

func TestStepSentenceIsNumberedAsThePagesCountThem(t *testing.T) {
	if got := stepSentence(2, "Audio"); got != "2. Audio" {
		t.Errorf("stepSentence = %q, want \"2. Audio\"", got)
	}
}

// TestNextActionSentencesArePinned pins the failure card's next-action line (S3-6a): ui-shop wrote these words, so
// a rewording shows up here rather than on a page. Each names the action the person can take, and a workflow fix
// ends "then press Retry", the button the page shows.
func TestNextActionSentencesArePinned(t *testing.T) {
	cases := []struct{ got, want string }{
		{NextRetry, "Press Retry to try the file again."},
		{NextRetryLog, "Press Retry to try the file again; if it fails again, the job log has the details."},
		{NextTriedAgain, "This file will be tried again in a few minutes."},
		{NextFixWorkflow, "Change the workflow so it fits this file, then press Retry."},
		{NextOpenWorkflow, "Open the workflow and fix what it reports, then press Retry."},
		{NextFixSettings, "Fix the step settings in the workflow, then press Retry."},
		{NextFreeName, "Another file has the new file's name. Move or rename it, then press Retry."},
		{NextMoveSrt, "Move or rename that .srt file, then press Retry."},
		{NextDestination, "Check the destination folder, then press Retry."},
		{NextEncoder, "Check the video encoder on the computer that ran this job, then press Retry."},
		{NextFileReadable, "Check that the file is still there and readable, then press Retry."},
		{NextWorker, "Check that the file and its workflow are still there, then press Retry."},
		{NextBug, "This is a bug, not a problem with your file. Download this job's log and report it."},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("sentence = %q, want %q", c.got, c.want)
		}
	}
}

// TestNextForPlanPicksByCode: a workflow that cannot run and a step with bad settings each say what to fix; every
// other refusal is the flow not fitting this file.
func TestNextForPlanPicksByCode(t *testing.T) {
	cases := map[string]string{
		"invalid_flow":              NextOpenWorkflow,
		"invalid_settings":          NextFixSettings,
		"video.no_video":            NextFixWorkflow,
		"save.would_land_on_source": NextFixWorkflow,
	}
	for code, want := range cases {
		if got := nextForPlan(code); got != want {
			t.Errorf("nextForPlan(%q) = %q, want %q", code, got, want)
		}
	}
}
