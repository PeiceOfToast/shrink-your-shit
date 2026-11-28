// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package steps_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow/steps"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

func jsonDecode(raw json.RawMessage, v any) error { return json.Unmarshal(raw, v) }

func jsonEncode(v any) (json.RawMessage, error) { return json.Marshal(v) }

func starter(t *testing.T) flow.Flow {
	t.Helper()
	s, ok := flow.StarterByID("shrink-videos")
	if !ok {
		t.Fatal("no shrink-videos starter")
	}
	return s.Flow
}

// setStep is the starter with one key of one step (by its type id) set to value, so each line is checked on the real flow.
func setStep(t *testing.T, f flow.Flow, typeID, key string, value any) flow.Flow {
	t.Helper()
	found := false
	for i, ref := range f.Steps {
		if ref.Type != typeID {
			continue
		}
		found = true
		var settings map[string]any
		if err := jsonDecode(ref.Settings, &settings); err != nil {
			t.Fatal(err)
		}
		settings[key] = value
		raw, err := jsonEncode(settings)
		if err != nil {
			t.Fatal(err)
		}
		f.Steps[i].Settings = raw
	}
	if !found {
		t.Fatalf("the starter has no %s step", typeID)
	}
	return f
}

func withSave(t *testing.T, where string) flow.Flow {
	return setStep(t, starter(t), "file.save", "where", where)
}

func sentencesOf(t *testing.T, f flow.Flow, keepDays int) (converts, leavesAlone, original string) {
	t.Helper()
	s, err := steps.WorkflowSentences(f, keepDays)
	if err != nil {
		t.Fatal(err)
	}
	return s.Converts, s.LeavesAlone, s.Original
}

// The starter converts to HEVC at the default quality on the graphics card, and leaves HEVC alone.
func TestStarterSentences(t *testing.T) {
	c, l, o := sentencesOf(t, starter(t), 7)
	if !strings.Contains(c, "Converts video to HEVC at the Balanced setting") {
		t.Errorf("converts = %q", c)
	}
	// The starter's card is off, so its line says the processor.
	if !strings.Contains(c, "on the processor") {
		t.Errorf("converts = %q, want the processor (the starter's card is off)", c)
	}
	if !strings.Contains(l, "Leaves alone files already in HEVC") {
		t.Errorf("leavesAlone = %q", l)
	}
	if o != "The original stays next to the new file." {
		t.Errorf("original = %q", o)
	}
}

// The card line follows the card setting, and the card's failure rule.
func TestCardLinePerSetting(t *testing.T) {
	onCard := setStep(t, starter(t), "video.convert", "useGpu", true)
	c, _, _ := sentencesOf(t, onCard, 7)
	if !strings.Contains(c, "on the graphics card, or the processor if the card can't do the file") {
		t.Errorf("card on, fall back = %q", c)
	}
	stops := setStep(t, onCard, "video.convert", "ifGpuFails", "stop")
	c, _, _ = sentencesOf(t, stops, 7)
	if !strings.Contains(c, "on the graphics card, and it stops if the card can't do the file") {
		t.Errorf("card on, stop = %q", c)
	}
}

// Replace follows the keepOriginalsDays setting the run reads, not a constant; one day is singular.
func TestReplaceFollowsKeepOriginalsDays(t *testing.T) {
	_, _, o30 := sentencesOf(t, withSave(t, "replace"), 30)
	if o30 != "The original goes to the trash for 30 days." {
		t.Errorf("replace at 30 = %q", o30)
	}
	_, _, o1 := sentencesOf(t, withSave(t, "replace"), 1)
	if o1 != "The original goes to the trash for 1 day." {
		t.Errorf("replace at 1 = %q", o1)
	}
}

// Next to: the original stays. A folder: the step's own note, since the mode is not built yet.
func TestOriginalPerSaveMode(t *testing.T) {
	_, _, next := sentencesOf(t, withSave(t, "next_to"), 7)
	if next != "The original stays next to the new file." {
		t.Errorf("next_to = %q", next)
	}
	_, _, folder := sentencesOf(t, withSave(t, "folder"), 7)
	if folder == "" || strings.HasPrefix(folder, "folder=") || folder == next {
		t.Errorf("folder = %q, want the step's note without its key, and not the next-to line", folder)
	}
}

// A flow with no video step says so, and no file is left alone by it.
func TestAFlowWithNoVideoStep(t *testing.T) {
	f := starter(t)
	var keep []flow.StepRef
	for _, ref := range f.Steps {
		if ref.Type != "video.convert" {
			keep = append(keep, ref)
		}
	}
	f.Steps = keep
	c, l, _ := sentencesOf(t, f, 7)
	if strings.Contains(c, "Converts video") {
		t.Errorf("converts = %q, with no video step", c)
	}
	if l != "Leaves no files alone." {
		t.Errorf("leavesAlone = %q", l)
	}
}

// The leave-alone line matches the run: a file the line says is already in the chosen format gets nothing to do, and a
// file in another format is converted.
func TestLeaveAloneMatchesTheRun(t *testing.T) {
	rev := flow.Revision{WorkflowID: 1, Rev: 1, Flow: starter(t)}
	hevc := media.Info{
		Format: media.Format{Names: []string{"matroska", "webm"}, Duration: time.Hour, Size: 900_000_000},
		Video:  []media.VideoTrack{{Codec: "hevc", Profile: "Main", Width: 1920, Height: 1080, FrameRate: media.Rational{Num: 25, Den: 1}, PixelFormat: "yuv420p", BitDepth: 8}},
		Audio:  []media.AudioTrack{{Codec: "aac", Channels: 2, SampleRate: 48000, Language: "eng"}},
	}
	if work := planWork(t, rev, hevc); work {
		t.Error("an HEVC file gets work, but the leave-alone line says it is left alone")
	}
	h264 := hevc
	h264.Video = []media.VideoTrack{{Codec: "h264", Profile: "High", Width: 1920, Height: 1080, FrameRate: media.Rational{Num: 25, Den: 1}, PixelFormat: "yuv420p", BitDepth: 8}}
	if work := planWork(t, rev, h264); !work {
		t.Error("an H.264 file is left alone, but the converts line says it converts to HEVC")
	}
}

// planWork is whether the run has any work for the file: the same decision the queue makes.
func planWork(t *testing.T, rev flow.Revision, info media.Info) bool {
	t.Helper()
	d, _, err := flow.PlanFile(rev, info, flow.Env{})
	if err != nil {
		t.Fatal(err)
	}
	return d.Work()
}
