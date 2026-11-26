// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package preview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

func TestTrackSaysWhatEachKindWillDo(t *testing.T) {
	dir := library(t)
	write(t, filepath.Join(dir, "subs.mkv"), 1_000_000)
	p := &Previewer{Store: store(t), Exec: &probes{}, FFprobe: "ffprobe"}
	tr := func(name string, speed func(string) float64) Track {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return p.Track(context.Background(), dir, filepath.Join(dir, name), st, rules.SliderTemplate(3), Destination{}, speed)
	}
	kinds := func(tr Track) string {
		var k []string
		for _, l := range tr.Lines {
			k = append(k, l.Kind)
		}
		return strings.Join(k, ",")
	}

	a := tr("a.mkv", nil)
	if got := kinds(a); got != "video,audio,original" {
		t.Errorf("kinds %s", got)
	}
	if a.Lines[0].Text != "Video: H.264 1080p → HEVC, same picture size and length" || a.Lines[2].Text != "Original: kept in a .syp-trash folder beside it" {
		t.Errorf("%+v", a.Lines)
	}
	if a.Skipped || a.EstimatedSeconds != nil || len(a.Checks) < 3 || a.Checks[0] != "Same length as the original" {
		t.Errorf("%+v", a)
	}
	// the size range is the one the page's sample gives for the same file (one rule, two callers)
	st, _ := os.Stat(filepath.Join(dir, "a.mkv"))
	s := p.Sample(context.Background(), dir, filepath.Join(dir, "a.mkv"), st, rules.SliderTemplate(3))
	if a.ExpectedBytesLow != s.ExpectedBytesLow || a.ExpectedBytesHigh != s.ExpectedBytesHigh {
		t.Errorf("track %d-%d, sample %d-%d", a.ExpectedBytesLow, a.ExpectedBytesHigh, s.ExpectedBytesLow, s.ExpectedBytesHigh)
	}

	if got := kinds(tr("subs.mkv", nil)); !strings.Contains(got, "subtitles") {
		t.Errorf("no subtitles line: %s", got)
	}
	if got := kinds(tr("hevc.mkv", nil)); !strings.HasPrefix(got, "video,") {
		t.Errorf("hevc kinds %s", got)
	}

	bad := tr("bad.mkv", nil)
	if !bad.Skipped || len(bad.Lines) != 1 || bad.Lines[0].Kind != "original" || bad.Lines[0].Text != "Left as it is: it can't be read." {
		t.Errorf("%+v", bad)
	}
	if bad.Checks == nil || len(bad.Checks) != 0 || bad.EstimatedSeconds != nil || bad.ExpectedBytesLow != 100_000 || bad.ExpectedBytesHigh != 100_000 {
		t.Errorf("skipped file: %+v", bad)
	}
}

func TestTrackEstimatesFromTheSpeedItIsGiven(t *testing.T) {
	dir := library(t)
	p := &Previewer{Store: store(t), Exec: &probes{}, FFprobe: "ffprobe"}
	st, _ := os.Stat(filepath.Join(dir, "a.mkv"))
	path := filepath.Join(dir, "a.mkv")
	var asked []string
	at := func(x float64) *int64 {
		tr := p.Track(context.Background(), dir, path, st, rules.SliderTemplate(3), Destination{}, func(enc string) float64 {
			asked = append(asked, enc)
			return x
		})
		return tr.EstimatedSeconds
	}
	slow, fast := at(1), at(4)
	if slow == nil || fast == nil || *slow <= 0 || *fast <= 0 {
		t.Fatalf("estimates %v %v", slow, fast)
	}
	// 4x the speed is a quarter of the time (rounded up to whole seconds)
	if d := *slow - 4**fast; d < -3 || d > 3 {
		t.Errorf("1x %d s, 4x %d s: not a quarter", *slow, *fast)
	}
	if len(asked) != 2 || asked[0] == "" {
		t.Errorf("the encoder asked for: %q", asked)
	}
	if at(0) != nil {
		t.Error("no speed for the encoder: the estimate must be left out")
	}
	if tr := p.Track(context.Background(), dir, path, st, rules.SliderTemplate(3), Destination{}, nil); tr.EstimatedSeconds != nil {
		t.Error("nil speed lookup: no estimate")
	}
}

// the original line says where the new file goes: a folder, why there is none yet, beside, or replaced
func TestTrackSaysWhereTheNewFileGoes(t *testing.T) {
	dir := library(t)
	p := &Previewer{Store: store(t), Exec: &probes{}, FFprobe: "ffprobe"}
	path := filepath.Join(dir, "a.mkv")
	st, _ := os.Stat(path)
	last := func(after string, dest Destination) string {
		s := rules.DefaultSelectors(3)
		s.AfterConverting = after
		if after == "folder" {
			s.FolderMode = rules.NextToLibrary // one folder per library, so the folder comes from dest
		}
		tr := p.Track(context.Background(), dir, path, st, s.Template(), dest, nil)
		return tr.Lines[len(tr.Lines)-1].Text
	}
	for _, c := range []struct {
		name, after, want string
		dest              Destination
	}{
		{"folder", "folder", "Original: stays as it is; the new file goes to /media/movies (shrunk)", Destination{Folder: "/media/movies (shrunk)"}},
		{"folder with a problem", "folder", "Original: stays as it is; the new file goes to a folder that can't be used yet: No folder for new files for Movies yet: /media is on a different disk. Pick a folder.",
			Destination{Problem: "No folder for new files for Movies yet: /media is on a different disk. Pick a folder."}},
		{"beside", "beside", "Original: stays as it is, next to the new file", Destination{}},
		{"replace", "replace", "Original: kept in a .syp-trash folder beside it", Destination{}},
	} {
		if got := last(c.after, c.dest); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}
