// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media/fake"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// fakeJournal is the sidecar record a run writes, in memory: what was recorded, what was finished
// and what was dropped.
type fakeJournal struct {
	recs     []safety.Sidecar
	added    []safety.Sidecar
	finished []int64
	dropped  []string
}

func (f *fakeJournal) AddSidecars(list []safety.Sidecar) error {
	f.recs = append(f.recs, list...)
	f.added = append(f.added, list...)
	return nil
}
func (f *fakeJournal) FinishSidecars(jobID int64) error {
	f.finished = append(f.finished, jobID)
	f.recs = nil // the finished job keeps the names; the records go
	return nil
}
func (f *fakeJournal) Sidecars() ([]safety.Sidecar, error) { return f.recs, nil }

func (f *fakeJournal) DropSidecar(jobID int64, path string) error {
	f.dropped = append(f.dropped, path)
	for i, c := range f.recs {
		if c.JobID == jobID && c.Path == path {
			f.recs = append(f.recs[:i], f.recs[i+1:]...)
			return nil
		}
	}
	return nil
}

// srtWriting is the fake media plus the .srt files ffmpeg would have written: after the run, it
// writes a short subtitle at every extraction path the command names. noFiles leaves them out, the
// way an encode that could not write one would; collide writes a file of someone else's at each of
// these paths now, the way a name can be taken while the file converts.
type srtWriting struct {
	*fake.Media
	noFiles bool
	collide []string
}

func (m *srtWriting) Run(ctx context.Context, argv []string, onProgress func(media.Progress)) (media.Result, error) {
	res, err := m.Media.Run(ctx, argv, onProgress)
	if err != nil {
		return res, err
	}
	if !m.noFiles {
		for _, a := range argv {
			if strings.HasSuffix(a, ".srt") && strings.Contains(a, ".syp-tmp") {
				if err := os.WriteFile(a, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 0o600); err != nil {
					return media.Result{}, err
				}
			}
		}
	}
	for _, p := range m.collide {
		if err := os.WriteFile(p, []byte("someone else's subtitles"), 0o644); err != nil {
			return media.Result{}, err
		}
	}
	return res, nil
}

// srtRun is one library file with two English text subtitles and an audio step that saves them as
// .srt files.
func srtRun(t *testing.T) (root, src string, info media.Info, rev flow.Flow) {
	t.Helper()
	root, src, info = libraryFile(t, "clip.mkv", "h264")
	info.Subtitles = []media.SubtitleTrack{
		{Index: 3, Codec: "subrip", Language: "eng"},
		{Index: 4, Codec: "subrip", Language: "eng"},
	}
	rev = starterFlow(t)
	rev.Steps[1].Settings = json.RawMessage(`{"subtitles":"srt","subtitleLanguages":["eng"]}`)
	return root, src, info, rev
}

// TestRunSRTWritesNamedSidecarsBesideTheNewFile is L-AUD-22 and L-AUD-25 end to end: the text
// subtitles leave the new file, each is named for the file it will sit beside, it is recorded
// before it is placed, and the finished job keeps its names.
func TestRunSRTWritesNamedSidecarsBesideTheNewFile(t *testing.T) {
	root, src, info, rev := srtRun(t)
	out := filepath.Join(root, "clip-shrunk.mkv")
	m := &srtWriting{Media: &fake.Media{Probes: map[string]media.Info{src: info}}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	journal := &fakeJournal{}
	env := newEnv(m, sf, &recorder{}, rev, nil)
	env.Sidecars = journal
	res := flow.Run(context.Background(), runInput(t, root, src), env)

	if res.Outcome != flow.OutcomeSmaller || res.OutputPath != out {
		t.Fatalf("result = %+v, want the new file at %s", res, out)
	}
	for _, name := range []string{"clip-shrunk.eng.srt", "clip-shrunk.eng.2.srt"} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || len(b) == 0 {
			t.Errorf("%s: %v (%q), want a subtitle file beside the new file", name, err, b)
		}
	}
	if len(journal.added) != 2 {
		t.Fatalf("records = %+v, want two", journal.added)
	}
	for _, c := range journal.added {
		if c.JobID != 42 || c.VideoPath != out || c.Bytes == 0 || c.Hash == "" || c.VideoBytes == 0 || c.VideoHash == "" ||
			!strings.HasPrefix(c.Path, strings.TrimSuffix(out, filepath.Ext(out))) {
			t.Errorf("record = %+v, want the .srt's identity and the new file's", c)
		}
	}
	if len(journal.finished) != 1 || journal.finished[0] != 42 {
		t.Errorf("finished = %v, want the job once", journal.finished)
	}
	if len(journal.dropped) != 0 {
		t.Errorf("dropped = %v, want nothing", journal.dropped)
	}
}

// TestRunSRTNameIsTakenStopsBeforeAnything: a subtitle file name taken already stops the run before
// anything is written, with the sentence that says so and nothing changed.
func TestRunSRTNameIsTakenStopsBeforeAnything(t *testing.T) {
	root, src, info, rev := srtRun(t)
	mine := filepath.Join(root, "clip-shrunk.eng.srt")
	if err := os.WriteFile(mine, []byte("someone else's subtitles"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &srtWriting{Media: &fake.Media{Probes: map[string]media.Info{src: info}}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	journal := &fakeJournal{}
	env := newEnv(m, sf, &recorder{}, rev, nil)
	env.Sidecars = journal
	res := flow.Run(context.Background(), runInput(t, root, src), env)

	if res.Outcome != flow.OutcomeFatal || res.Reason != "save.srt_exists" || !strings.Contains(res.Message, "is already there") {
		t.Fatalf("result = %+v, want the taken-name refusal", res)
	}
	if len(m.Argvs()) != 0 {
		t.Errorf("ran ffmpeg for a name taken already: %v", m.Argvs())
	}
	if b, _ := os.ReadFile(mine); string(b) != "someone else's subtitles" {
		t.Errorf("the file that was there changed: %q", b)
	}
	if len(journal.added) != 0 {
		t.Errorf("records = %+v, want none for a run that never started", journal.added)
	}
}

// TestRunSRTLateCollisionLeavesNoRecord: a name taken while the file converts fails the job with
// the same sentence, leaves no record for the file that was never placed, and leaves the other
// file as it was.
func TestRunSRTLateCollisionLeavesNoRecord(t *testing.T) {
	root, src, info, rev := srtRun(t)
	mine := filepath.Join(root, "clip-shrunk.eng.srt")
	m := &srtWriting{Media: &fake.Media{Probes: map[string]media.Info{src: info}}, collide: []string{mine}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	journal := &fakeJournal{}
	env := newEnv(m, sf, &recorder{}, rev, nil)
	env.Sidecars = journal
	res := flow.Run(context.Background(), runInput(t, root, src), env)

	if res.Outcome != flow.OutcomeFatal || res.Reason != "save.srt_exists" || !strings.Contains(res.Message, "is already there") {
		t.Fatalf("result = %+v, want the taken-name refusal", res)
	}
	if len(journal.recs) != 0 {
		t.Errorf("records left for files never placed: %+v", journal.recs)
	}
	if b, _ := os.ReadFile(mine); string(b) != "someone else's subtitles" {
		t.Errorf("the other file was removed or changed: %q", b)
	}
	if _, err := os.Stat(filepath.Join(root, "clip-shrunk.mkv")); err == nil {
		t.Error("the new file was placed after a subtitle file failed")
	}
	// The source is exactly as it was: the run never placed anything.
	if b, _ := os.ReadFile(src); !strings.HasPrefix(string(b), "source") {
		t.Errorf("source = %q, want it untouched", b)
	}
}

// TestRunSRTSidecarsStayWhenTheSourceIsLeftBehind: Place's link fallback puts the new file in place
// and reports ErrSourceLeftBehind. That is a success, so the .srt files stay beside it and the job
// keeps their names; only a place that did not happen takes them back.
func TestRunSRTSidecarsStayWhenTheSourceIsLeftBehind(t *testing.T) {
	root, src, info, rev := srtRun(t)
	out := filepath.Join(root, "clip-shrunk.mkv")
	m := &srtWriting{Media: &fake.Media{Probes: map[string]media.Info{src: info}}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}, placeLeftBehind: true}
	journal := &fakeJournal{}
	env := newEnv(m, sf, &recorder{}, rev, nil)
	env.Sidecars = journal
	res := flow.Run(context.Background(), runInput(t, root, src), env)

	if res.Outcome != flow.OutcomeSmaller || res.Reason != "source.not_removed" || res.OutputPath != out {
		t.Fatalf("result = %+v, want the new file placed with its source left behind", res)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("the new file is not in place: %v", err)
	}
	for _, name := range []string{"clip-shrunk.eng.srt", "clip-shrunk.eng.2.srt"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("%s must stay: the new file is in place", name)
		}
	}
	if len(journal.recs) != 0 || len(journal.finished) != 1 || len(journal.dropped) != 0 {
		t.Errorf("records = %+v finished = %v dropped = %v, want the job to keep the names",
			journal.recs, journal.finished, journal.dropped)
	}
}

// TestRunSRTSidecarsGoWithAFailedPlace: when the new file cannot be placed, the .srt files this run
// put there go again, and so do their records.
func TestRunSRTSidecarsGoWithAFailedPlace(t *testing.T) {
	root, src, info, rev := srtRun(t)
	m := &srtWriting{Media: &fake.Media{Probes: map[string]media.Info{src: info}}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}, placeE: errors.New("the disk is full")}
	journal := &fakeJournal{}
	env := newEnv(m, sf, &recorder{}, rev, nil)
	env.Sidecars = journal
	res := flow.Run(context.Background(), runInput(t, root, src), env)

	if res.Outcome != flow.OutcomeFatal || res.Reason != "save.failed" {
		t.Fatalf("result = %+v, want the place failure", res)
	}
	for _, name := range []string{"clip-shrunk.eng.srt", "clip-shrunk.eng.2.srt"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			t.Errorf("%s left beside a file that was not placed", name)
		}
	}
	if len(journal.recs) != 0 || len(journal.dropped) != 2 {
		t.Errorf("records = %+v dropped = %v, want both records gone", journal.recs, journal.dropped)
	}
}

// TestRunSRTPlaceFailureKeepsAChangedSidecar is L-SAFE-11's changed earlier output: a .srt that
// someone replaced in the instant before the new file failed to go in is not ours any more, so it
// stays; only its record goes.
func TestRunSRTPlaceFailureKeepsAChangedSidecar(t *testing.T) {
	root, src, info, rev := srtRun(t)
	mine := filepath.Join(root, "clip-shrunk.eng.srt")
	m := &srtWriting{Media: &fake.Media{Probes: map[string]media.Info{src: info}}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}, placeE: errors.New("the disk is full"),
		beforePlace: func() {
			if err := os.WriteFile(mine, []byte("someone else's subtitles"), 0o644); err != nil {
				t.Error(err)
			}
		}}
	journal := &fakeJournal{}
	env := newEnv(m, sf, &recorder{}, rev, nil)
	env.Sidecars = journal
	res := flow.Run(context.Background(), runInput(t, root, src), env)

	if res.Outcome != flow.OutcomeFatal || res.Reason != "save.failed" {
		t.Fatalf("result = %+v, want the place failure", res)
	}
	if b, _ := os.ReadFile(mine); string(b) != "someone else's subtitles" {
		t.Errorf("a file that is not ours was removed: %q", b)
	}
	if len(journal.recs) != 0 {
		t.Errorf("records left: %+v", journal.recs)
	}
}

// TestRunSRTWithoutTheFilesKeepsTheOriginal: a subtitle the command did not write is not placed
// over anything; the original is kept.
func TestRunSRTWithoutTheFilesKeepsTheOriginal(t *testing.T) {
	root, src, info, rev := srtRun(t)
	m := &srtWriting{Media: &fake.Media{Probes: map[string]media.Info{src: info}}, noFiles: true}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	journal := &fakeJournal{}
	env := newEnv(m, sf, &recorder{}, rev, nil)
	env.Sidecars = journal
	res := flow.Run(context.Background(), runInput(t, root, src), env)

	if res.Outcome != flow.OutcomeBiggerKept || res.Reason != "subtitle.not_saved" ||
		!strings.Contains(res.Message, "could not be saved as a separate file") {
		t.Fatalf("result = %+v, want the original kept", res)
	}
	if len(journal.added) != 0 {
		t.Errorf("records = %+v, want none", journal.added)
	}
	if sf.placedTo != "" {
		t.Errorf("placed %q, want nothing", sf.placedTo)
	}
}

// TestRunSRTNamesFollowTheFilesRealName: when a file has the name the new file wanted, the new file
// becomes "name (2)" and its subtitle files are named for that, not for the name it did not get.
func TestRunSRTNamesFollowTheFilesRealName(t *testing.T) {
	root, src, info, rev := srtRun(t)
	earlier := filepath.Join(root, "clip-shrunk.mkv")
	if err := os.WriteFile(earlier, []byte("an earlier output"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &srtWriting{Media: &fake.Media{Probes: map[string]media.Info{src: info}}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	env := newEnv(m, sf, &recorder{}, rev, nil)
	env.Sidecars = &fakeJournal{}
	res := flow.Run(context.Background(), runInput(t, root, src), env)

	out := filepath.Join(root, "clip-shrunk (2).mkv")
	if res.Outcome != flow.OutcomeSmaller || res.OutputPath != out {
		t.Fatalf("result = %+v, want the new file at %s", res, out)
	}
	if _, err := os.Stat(filepath.Join(root, "clip-shrunk (2).eng.srt")); err != nil {
		t.Errorf("the subtitle file did not follow the new file's name: %v", err)
	}
	if b, _ := os.ReadFile(earlier); string(b) != "an earlier output" {
		t.Errorf("the file that was there changed: %q", b)
	}
}

// TestRunSRTLateNameRaceWarnsAndKeepsTheSubtitlesOnTheChosenName: a file takes the new file's name
// between FreeName and Place, so the new file becomes "name (2)". The job warns, and the .srt
// files keep the base they were named for (the name chosen before the race).
func TestRunSRTLateNameRaceWarnsAndKeepsTheSubtitlesOnTheChosenName(t *testing.T) {
	root, src, info, rev := srtRun(t)
	bumped := filepath.Join(root, "clip-shrunk (2).mkv")
	m := &srtWriting{Media: &fake.Media{Probes: map[string]media.Info{src: info}}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}, placeAs: bumped}
	journal := &fakeJournal{}
	bus := &recorder{}
	env := newEnv(m, sf, bus, rev, nil)
	env.Sidecars = journal
	res := flow.Run(context.Background(), runInput(t, root, src), env)

	if res.Outcome != flow.OutcomeSmaller || res.OutputPath != bumped {
		t.Fatalf("result = %+v, want the new file at %s", res, bumped)
	}
	for _, name := range []string{"clip-shrunk.eng.srt", "clip-shrunk.eng.2.srt"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("%s: %v, want the .srt on the name chosen before the race", name, err)
		}
	}
	warned := false
	for _, e := range bus.all() {
		if e.Kind == events.KindWarning && strings.Contains(e.Message, "appeared meanwhile") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no warning that a file with the new file's name appeared meanwhile: %+v", bus.all())
	}
	if len(journal.finished) != 1 || len(journal.dropped) != 0 {
		t.Errorf("finished = %v dropped = %v, want the names kept", journal.finished, journal.dropped)
	}
}

// TestRunTryWritesNoSidecarRecords: a try keeps nothing on record and leaves nothing beside the
// source.
func TestRunTryWritesNoSidecarRecords(t *testing.T) {
	root, src, info, rev := srtRun(t)
	m := &srtWriting{Media: &fake.Media{Probes: map[string]media.Info{src: info}}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	journal := &fakeJournal{}
	env := newEnv(m, sf, &recorder{}, rev, nil)
	env.Sidecars = journal
	in := runInput(t, root, src)
	in.Try = true
	in.Flow = &rev
	in.StageBase = t.TempDir()
	res := flow.Run(context.Background(), in, env)

	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("result = %+v, want the try's answer", res)
	}
	if len(journal.added) != 0 || len(journal.finished) != 0 {
		t.Errorf("records = %+v finished = %v, want a try to keep none", journal.added, journal.finished)
	}
	if sf.placedTo != "" {
		t.Errorf("placed %q, want a try to place nothing", sf.placedTo)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "*.srt"))
	if len(matches) != 0 {
		t.Errorf("sidecars beside the source after a try: %v", matches)
	}
}
