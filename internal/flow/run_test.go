// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	_ "github.com/PeiceOfToast/shrink-your-shit/internal/flow/steps"
	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media/fake"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// recorder is an events.Bus that keeps what it is given.
type recorder struct {
	mu  sync.Mutex
	got []events.Event
}

func (r *recorder) Emit(e events.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, e)
	return nil
}

func (r *recorder) all() []events.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]events.Event(nil), r.got...)
}

// fakeSafety is the file boundary without a file system surprise: it stages a folder with a small
// output, answers Verify from the test, and places by moving the staged file.
type fakeSafety struct {
	verify  safety.Verdict
	verifyE error
	// source answers CheckSource. Its zero value passes: a fake that says nothing about the
	// original does not refuse it.
	source  safety.Verdict
	sourceE error
	stageE  error
	placeE  error
	// placeLeftBehind makes Place behave like the link fallback: the final name exists (a hard link),
	// the staged copy stays, and the error is ErrSourceLeftBehind.
	placeLeftBehind bool

	gotWant     safety.Want
	gotRecorder safety.Recorder
	staged      []safety.Staged
	placedTo    string
	sourceCalls int
	gotSource   media.Info
}

func (f *fakeSafety) Stage(base, dst string) (safety.Staged, error) {
	if f.stageE != nil {
		return safety.Staged{}, f.stageE
	}
	dir := base
	if dir == "" {
		dir = filepath.Dir(dst)
	}
	folder, err := os.MkdirTemp(dir, ".out.*.syp-tmp")
	if err != nil {
		return safety.Staged{}, err
	}
	path := filepath.Join(folder, "out"+filepath.Ext(dst))
	if err := os.WriteFile(path, []byte("encoded"), 0o600); err != nil {
		os.RemoveAll(folder)
		return safety.Staged{}, err
	}
	st := safety.Staged{Folder: folder, Path: path}
	f.staged = append(f.staged, st)
	return st, nil
}

func (f *fakeSafety) Verify(ctx context.Context, c safety.Checker, rec safety.Recorder, st safety.Staged, src media.Info, want safety.Want, timeout time.Duration) (safety.Verdict, error) {
	f.gotRecorder = rec
	f.gotWant = want
	return f.verify, f.verifyE
}

func (f *fakeSafety) CheckSource(ctx context.Context, c safety.Checker, rec safety.Recorder, path string, src media.Info, timeout time.Duration) (safety.Verdict, error) {
	f.sourceCalls++
	f.gotSource = src
	f.gotRecorder = rec
	if f.sourceE != nil {
		return safety.Verdict{}, f.sourceE
	}
	if !f.source.OK && f.source.Code == "" {
		return safety.Verdict{OK: true}, nil
	}
	return f.source, nil
}

func (f *fakeSafety) Place(ctx context.Context, st safety.Staged, final string) (string, error) {
	if f.placeLeftBehind {
		if err := os.Link(st.Path, final); err != nil {
			return "", err
		}
		f.placedTo = final
		return final, safety.ErrSourceLeftBehind
	}
	if f.placeE != nil {
		return "", f.placeE
	}
	f.placedTo = final
	return final, os.RemoveAll(st.Folder)
}

// starterFlow is the fixed starter, decoded through the real steps.
func starterFlow(t *testing.T) flow.Flow {
	t.Helper()
	s, ok := flow.StarterByID("shrink-videos")
	if !ok {
		t.Fatal("no shrink-videos starter")
	}
	return s.Flow
}

// libraryFile writes a source file in a fresh library folder and describes it the way the scan
// would: 1080p H.264, Matroska with one audio and one subtitle stream.
func libraryFile(t *testing.T, name, codec string) (root, src string, info media.Info) {
	t.Helper()
	root = t.TempDir()
	src = filepath.Join(root, name)
	if err := os.WriteFile(src, bytes.Repeat([]byte("source"), 100), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	info = media.Info{
		Path:      src,
		Format:    media.Format{Names: []string{"matroska", "webm"}, Duration: 60 * time.Second},
		Video:     []media.VideoTrack{{Index: 0, Codec: codec, Width: 1920, Height: 1080, BitDepth: 8}},
		Audio:     []media.AudioTrack{{Index: 1, Codec: "eac3", Language: "eng"}, {Index: 2, Codec: "aac", Language: "ger"}},
		Subtitles: []media.SubtitleTrack{{Index: 3, Codec: "subrip", Language: "eng"}},
	}
	return root, src, info
}

// runInput describes one libraryFile the way the worker would.
func runInput(t *testing.T, root, src string) flow.RunInput {
	st, _ := os.Stat(src)
	return flow.RunInput{
		JobID: 42, Attempt: 1, LibraryID: "Movies", WorkerID: "local",
		WorkflowID: 7, Revision: 1,
		LibraryRoot: root, Source: src,
		Size: st.Size(), MTime: st.ModTime(),
		FileID: fileID(t, src),
	}
}

func fileID(t *testing.T, path string) string {
	t.Helper()
	id, err := fsx.FileID(path)
	if err != nil {
		t.Fatalf("file id: %v", err)
	}
	return id
}

// newEnv is an Env whose services are the fakes and whose clock and sleep do nothing but what a
// test says.
func newEnv(m media.Media, sf *fakeSafety, bus *recorder, rev flow.Flow, raw *bytes.Buffer) flow.Env {
	return flow.Env{
		Media: m, Safety: sf, Pick: flow.ProcessorPick,
		LoadRevision: func(ctx context.Context, id int64, r int) (flow.Flow, error) { return rev, nil },
		Bus:          bus, Raw: raw,
		Now:        func() time.Time { return time.Unix(0, 0) },
		Sleep:      func(time.Duration) {},
		AppVersion: "test",
	}
}

// frameKinds is a run's event frame by kind, with step ids for the step events.
func frameKinds(evs []events.Event) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		s := string(e.Kind)
		if e.StepID != "" {
			s += ":" + e.StepID
		}
		out = append(out, s)
	}
	return out
}

func TestRunGoodRunEventFrame(t *testing.T) {
	root, src, info := libraryFile(t, "clip.mkv", "h264")
	want := filepath.Join(root, "clip-shrunk.mkv")
	// The check probes the placed file too, so the fake answers for it.
	m := &fake.Media{Probes: map[string]media.Info{src: info, want: info}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	bus := &recorder{}
	var raw bytes.Buffer
	env := newEnv(m, sf, bus, starterFlow(t), &raw)
	env.SafetyLog = safety.Recorder{JobID: 42} // the engine must hand it to the check
	res := flow.Run(context.Background(), runInput(t, root, src), env)

	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("outcome = %q (%s: %s), want smaller", res.Outcome, res.Reason, res.Message)
	}
	if res.OutputPath != want {
		t.Errorf("output = %q, want %q", res.OutputPath, want)
	}
	if res.SizeBefore != 600 || res.SizeAfter >= res.SizeBefore {
		t.Errorf("sizes = %d -> %d, want the encoded file", res.SizeBefore, res.SizeAfter)
	}

	expected := []string{
		"run_start", "probe",
		"step_start:video", "decision:video", "step_end:video",
		"step_start:audio", "decision:audio", "step_end:audio",
		"step_start:save", "decision:save", "step_end:save",
		"command", "size", "move", "probe", "run_end",
	}
	if got := frameKinds(bus.all()); strings.Join(got, ",") != strings.Join(expected, ",") {
		t.Errorf("event frame =\n%v\nwant\n%v", got, expected)
	}
	// Every event the log shows has a sentence: the step, probe, command and size lines are never empty.
	for _, e := range bus.all() {
		switch e.Kind {
		case events.KindStepStart, events.KindProbe, events.KindCommand, events.KindSize:
			if e.Message == "" {
				t.Errorf("%s event has no message", e.Kind)
			}
		}
		switch {
		case e.Kind == events.KindStepStart && e.StepIndex == 1 && !strings.HasPrefix(e.Message, "1. "):
			t.Errorf("first step line = %q, want it numbered 1.", e.Message)
		case e.Kind == events.KindProbe && !strings.Contains(e.Message, "H.264"):
			t.Errorf("probe line = %q, want the codec", e.Message)
		case e.Kind == events.KindSize && !strings.Contains(e.Message, "→"):
			t.Errorf("size line = %q, want before → after", e.Message)
		}
	}
	if sf.gotRecorder.JobID != 42 {
		t.Errorf("check recorder = %+v, want the job's", sf.gotRecorder)
	}
	if sf.gotWant.Audio[0].Language != "eng" || sf.gotWant.Audio[1].Language != "ger" ||
		sf.gotWant.SubtitleLanguages[0] != "eng" || len(sf.gotWant.SubtitleLanguages) != 1 {
		t.Errorf("verify want = %+v, want eng/ger audio and eng subtitles", sf.gotWant)
	}

	// The command is the built argv with ffmpeg in front, writing the staged file rather than the
	// destination; the staged folder is gone once it is placed.
	run := m.Argvs()
	if len(run) != 1 || run[0][0] != "ffmpeg" || run[0][len(run[0])-1] != sf.staged[0].Path {
		t.Errorf("argv = %v, want the staged file last", run)
	}
	if !strings.Contains(sf.staged[0].Path, ".syp-tmp") {
		t.Errorf("staged path = %q, want a staging folder", sf.staged[0].Path)
	}
	if _, err := os.Stat(sf.staged[0].Folder); !os.IsNotExist(err) {
		t.Errorf("staging folder %s still there", sf.staged[0].Folder)
	}
}

// TestRunConvertedATrackTheFileTypeCannotHold is the file-dependent case the check cannot see:
// Audio copies, so the flow's container rule passes, but the source's Opus track cannot go into
// MP4. The run converts the track to E-AC-3 instead (L-AUD-10) and says so in the job log as a
// plain warning; it does not fail on a check nobody could pass, and it does not drop the track.
func TestRunConvertedATrackTheFileTypeCannotHold(t *testing.T) {
	root, src, info := libraryFile(t, "clip.mkv", "h264")
	info.Audio = []media.AudioTrack{{Index: 1, Codec: "opus", Language: "eng"}}
	want := filepath.Join(root, "clip-shrunk.mp4")
	m := &fake.Media{Probes: map[string]media.Info{src: info, want: info}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	bus := &recorder{}
	rev := starterFlow(t)
	rev.Steps[2].Settings = json.RawMessage(`{"container":"mp4","where":"next_to","nameSuffix":"shrunk"}`)
	res := flow.Run(context.Background(), runInput(t, root, src), newEnv(m, sf, bus, rev, nil))

	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("outcome = %q (%s: %s), want smaller", res.Outcome, res.Reason, res.Message)
	}
	warned := false
	for _, e := range bus.all() {
		if e.Level == events.LevelError {
			t.Errorf("error event %q: a stream the file type cannot hold is a warning, not a failure", e.Message)
		}
		if e.Kind == events.KindWarning && e.Message == "audio stream #1 (opus) is converted to eac3: MP4 cannot hold Opus" {
			warned = e.Level == events.LevelWarn
		}
	}
	if !warned {
		t.Errorf("events = %v, want the plain sentence about the converted Opus track", frameKinds(bus.all()))
	}
	argvs := m.Argvs()
	if len(argvs) == 0 || !strings.Contains(strings.Join(argvs[0], " "), "-c:a:0 eac3") {
		t.Errorf("argv = %v, want the Opus track written as E-AC-3", argvs)
	}
}

func TestRunSkipsWhenEveryStepSaysNothing(t *testing.T) {
	root, src, info := libraryFile(t, "clip.mkv", "hevc")
	m := &fake.Media{Probes: map[string]media.Info{src: info}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	bus := &recorder{}
	res := flow.Run(context.Background(), runInput(t, root, src),
		newEnv(m, sf, bus, starterFlow(t), nil))
	if res.Outcome != flow.OutcomeSkip || res.Reason != "flow.nothing" {
		t.Fatalf("result = %+v, want skip flow.nothing", res)
	}
	if len(m.Argvs()) != 0 {
		t.Errorf("ran ffmpeg for a file with nothing to do")
	}
	if sf.sourceCalls != 0 {
		t.Errorf("source check ran %d times for a file with no work", sf.sourceCalls)
	}
}

func TestRunRechecksTheFile(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, root, src string) flow.RunInput
		reason string
	}{
		{
			name: "gone",
			setup: func(t *testing.T, root, src string) flow.RunInput {
				in := runInput(t, root, src)
				os.Remove(src)
				return in
			},
			reason: "file.gone",
		},
		{
			name: "linked",
			setup: func(t *testing.T, root, src string) flow.RunInput {
				in := runInput(t, root, src)
				link := filepath.Join(root, "link.mkv")
				if err := os.Symlink(src, link); err != nil {
					t.Skipf("no symlinks here: %v", err)
				}
				in.Source = link
				return in
			},
			reason: "file.linked",
		},
		{
			name: "outside the library",
			setup: func(t *testing.T, root, src string) flow.RunInput {
				other := t.TempDir()
				out := filepath.Join(other, "elsewhere.mkv")
				if err := os.WriteFile(out, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				in := runInput(t, root, src)
				in.Source = out
				return in
			},
			reason: "file.outside_library",
		},
		{
			name: "our own staging",
			setup: func(t *testing.T, root, src string) flow.RunInput {
				dir := filepath.Join(root, ".clip.123.syp-tmp")
				os.MkdirAll(dir, 0o755)
				own := filepath.Join(dir, "out.mkv")
				os.WriteFile(own, []byte("x"), 0o600)
				in := runInput(t, root, src)
				in.Source = own
				return in
			},
			reason: "file.ours",
		},
		{
			name: "shared by a hard link",
			setup: func(t *testing.T, root, src string) flow.RunInput {
				in := runInput(t, root, src)
				if err := os.Link(src, filepath.Join(root, "other-name.mkv")); err != nil {
					t.Skipf("no hard links here: %v", err)
				}
				return in
			},
			reason: "file.shared",
		},
		{
			name: "replaced since the scan",
			setup: func(t *testing.T, root, src string) flow.RunInput {
				in := runInput(t, root, src)
				in.FileID = "1:999999" // not this file's id
				return in
			},
			reason: "file.changed",
		},
		{
			name: "changed since the scan",
			setup: func(t *testing.T, root, src string) flow.RunInput {
				in := runInput(t, root, src)
				in.Size++
				return in
			},
			reason: "file.changed",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, src, info := libraryFile(t, "clip.mkv", "h264")
			_ = info
			in := c.setup(t, root, src)
			res := flow.Run(context.Background(), in,
				newEnv(&fake.Media{}, &fakeSafety{}, &recorder{}, starterFlow(t), nil))
			if res.Outcome != flow.OutcomeSkip || res.Reason != c.reason {
				t.Errorf("result = %+v, want skip %s", res, c.reason)
			}
		})
	}
}

func TestRunRequeuesAFileStillBeingWritten(t *testing.T) {
	root, src, _ := libraryFile(t, "clip.mkv", "h264")
	in := runInput(t, root, src)
	in.Size++ // the record is stale either way
	env := newEnv(&fake.Media{}, &fakeSafety{}, &recorder{}, starterFlow(t), nil)
	env.Sleep = func(time.Duration) {
		// The second look sees a file that is still moving.
		if err := os.WriteFile(src, []byte("growing"), 0o600); err != nil {
			t.Errorf("grow: %v", err)
		}
	}
	res := flow.Run(context.Background(), in, env)
	if res.Outcome != flow.OutcomeRequeue || res.Reason != "file.still_changing" {
		t.Fatalf("result = %+v, want requeue file.still_changing", res)
	}
}

func TestRunSourceTheProbeRefusesIsSkipped(t *testing.T) {
	root, src, _ := libraryFile(t, "clip.mkv", "h264")
	m := &fake.Media{ProbeErrors: map[string]error{src: media.ErrNotMedia}}
	res := flow.Run(context.Background(), runInput(t, root, src),
		newEnv(m, &fakeSafety{}, &recorder{}, starterFlow(t), nil))
	if res.Outcome != flow.OutcomeSkip || res.Reason != "source.unreadable" {
		t.Fatalf("result = %+v, want skip source.unreadable", res)
	}
}

func TestRunKeepsTheOriginalWhenTheCheckSaysNo(t *testing.T) {
	root, src, info := libraryFile(t, "clip.mkv", "h264")
	m := &fake.Media{Probes: map[string]media.Info{src: info}}
	sf := &fakeSafety{verify: safety.Verdict{OK: false, Code: safety.CodeTooShort,
		Message: "The new file is shorter than the original.", NextAction: safety.NextKeepOriginal}}
	res := flow.Run(context.Background(), runInput(t, root, src),
		newEnv(m, sf, &recorder{}, starterFlow(t), nil))
	if res.Outcome != flow.OutcomeBiggerKept || res.Reason != safety.CodeTooShort {
		t.Fatalf("result = %+v, want bigger_kept %s", res, safety.CodeTooShort)
	}
	if _, err := os.Stat(sf.staged[0].Folder); !os.IsNotExist(err) {
		t.Errorf("staging folder %s still there", sf.staged[0].Folder)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("source is gone: %v", err)
	}
}

func TestRunMayPassWhenTheCheckCouldNotRun(t *testing.T) {
	root, src, info := libraryFile(t, "clip.mkv", "h264")
	m := &fake.Media{Probes: map[string]media.Info{src: info}}
	sf := &fakeSafety{verifyE: errors.New("ffprobe timed out")}
	res := flow.Run(context.Background(), runInput(t, root, src),
		newEnv(m, sf, &recorder{}, starterFlow(t), nil))
	if res.Outcome != flow.OutcomeMayPass || res.Reason != "check.unavailable" {
		t.Fatalf("result = %+v, want may_pass check.unavailable", res)
	}
}

func TestRunKeepsTheOriginalWhenTheResultIsNotSmaller(t *testing.T) {
	root, src, info := libraryFile(t, "tiny.mkv", "h264")
	// The staged "encoded" file is larger than this 1-byte source.
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := runInput(t, root, src)
	in.Size = 1
	m := &fake.Media{Probes: map[string]media.Info{src: info}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	bus := &recorder{}
	res := flow.Run(context.Background(), in, newEnv(m, sf, bus, starterFlow(t), nil))
	if res.Outcome != flow.OutcomeBiggerKept || res.Reason != "size.not_smaller" {
		t.Fatalf("result = %+v, want bigger_kept size.not_smaller", res)
	}
	if sf.placedTo != "" {
		t.Errorf("placed %q although the result was not smaller", sf.placedTo)
	}
	// L-SAFE-7: the job log says both sizes, so the drawer can show why the original stayed.
	var size *events.Event
	for _, e := range bus.all() {
		if e.Kind == events.KindSize {
			e := e
			size = &e
		}
	}
	if size == nil {
		t.Fatal("no size event; the log must say how big the result was")
	}
	if size.SizeBefore != 1 || size.SizeAfter != 7 || !strings.Contains(size.Message, "1 B") || !strings.Contains(size.Message, "7 B") {
		t.Errorf("size event = %+v; want both sizes, 1 B and 7 B", *size)
	}
}

func TestRunCancelMidEncodeIsCancelledAndCleansUp(t *testing.T) {
	root, src, info := libraryFile(t, "clip.mkv", "h264")
	ctx, cancel := context.WithCancel(context.Background())
	m := media.MediaFuncs{
		ProbeFunc: func(ctx context.Context, path string) (media.Info, error) { return info, nil },
		RunFunc: func(ctx context.Context, argv []string, p func(media.Progress)) (media.Result, error) {
			cancel() // the stop lands while ffmpeg is running
			<-ctx.Done()
			return media.Result{ExitCode: -1}, ctx.Err()
		},
	}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	res := flow.Run(ctx, runInput(t, root, src), newEnv(m, sf, &recorder{}, starterFlow(t), nil))
	if res.Outcome != flow.OutcomeCancelled {
		t.Fatalf("result = %+v, want cancelled", res)
	}
	if len(sf.staged) != 1 {
		t.Fatalf("staged %d times, want 1", len(sf.staged))
	}
	if _, err := os.Stat(sf.staged[0].Folder); !os.IsNotExist(err) {
		t.Errorf("staging folder %s still there after a cancel", sf.staged[0].Folder)
	}
}

func TestRunEncodeFailureIsFatalWithKeyLines(t *testing.T) {
	root, src, info := libraryFile(t, "clip.mkv", "h264")
	m := &fake.Media{
		Probes: map[string]media.Info{src: info},
		Runs:   []fake.RunScript{{Result: media.Result{ExitCode: 1, KeyLines: []string{"x265 [error]: out of memory"}}, Err: errors.New("ffmpeg exited 1")}},
	}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}}
	var raw bytes.Buffer
	res := flow.Run(context.Background(), runInput(t, root, src),
		newEnv(m, sf, &recorder{}, starterFlow(t), &raw))
	if res.Outcome != flow.OutcomeFatal || res.Reason != "encode.failed" || res.FailedStep != "video" {
		t.Fatalf("result = %+v, want fatal encode.failed in the video step", res)
	}
	if len(res.KeyLines) != 1 || res.KeyLines[0] != "x265 [error]: out of memory" {
		t.Errorf("key lines = %v", res.KeyLines)
	}
	if !strings.Contains(raw.String(), "out of memory") {
		t.Errorf("raw file = %q, want ffmpeg's tail", raw.String())
	}
	if _, err := os.Stat(sf.staged[0].Folder); !os.IsNotExist(err) {
		t.Errorf("staging folder still there after a failure")
	}
}

// TestRunKeepsTheStagingFolderWhenTheSourceIsLeftBehind is the link fallback: the output is at its
// final name, but the staged copy could not be removed. The run is a success with a warning, and the
// folder is kept for the sweep rather than deleted under the caller.
func TestRunKeepsTheStagingFolderWhenTheSourceIsLeftBehind(t *testing.T) {
	root, src, info := libraryFile(t, "clip.mkv", "h264")
	final := filepath.Join(root, "clip-shrunk.mkv")
	m := &fake.Media{Probes: map[string]media.Info{src: info}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}, placeLeftBehind: true}
	res := flow.Run(context.Background(), runInput(t, root, src),
		newEnv(m, sf, &recorder{}, starterFlow(t), nil))
	if res.Outcome != flow.OutcomeSmaller || res.Reason != "source.not_removed" || res.OutputPath != final {
		t.Fatalf("result = %+v, want smaller source.not_removed at %s", res, final)
	}
	if _, err := os.Stat(final); err != nil {
		t.Errorf("the output is not at its final name: %v", err)
	}
	if len(sf.staged) != 1 {
		t.Fatalf("staged %d times, want 1", len(sf.staged))
	}
	if _, err := os.Stat(sf.staged[0].Folder); err != nil {
		t.Errorf("staging folder removed although the staged copy was left behind: %v", err)
	}
	if _, err := os.Stat(sf.staged[0].Path); err != nil {
		t.Errorf("staged copy removed: %v", err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("the original is gone: %v", err)
	}
}

func TestRunPlaceFailureIsFatalAndCleansUp(t *testing.T) {
	root, src, info := libraryFile(t, "clip.mkv", "h264")
	m := &fake.Media{Probes: map[string]media.Info{src: info}}
	sf := &fakeSafety{verify: safety.Verdict{OK: true}, placeE: errors.New("permission denied")}
	res := flow.Run(context.Background(), runInput(t, root, src),
		newEnv(m, sf, &recorder{}, starterFlow(t), nil))
	if res.Outcome != flow.OutcomeFatal || res.Reason != "save.failed" || res.FailedStep != "save" {
		t.Fatalf("result = %+v, want fatal save.failed", res)
	}
	if _, err := os.Stat(sf.staged[0].Folder); !os.IsNotExist(err) {
		t.Errorf("staging folder still there after a failed place")
	}
}

func TestRunSkipWhenTheEncoderRefuses(t *testing.T) {
	root, src, info := libraryFile(t, "clip.mkv", "h264")
	info.Video[0].HDR = media.HDR{DolbyVision: true, DVProfile: 5}
	m := &fake.Media{Probes: map[string]media.Info{src: info}}
	res := flow.Run(context.Background(), runInput(t, root, src),
		newEnv(m, &fakeSafety{}, &recorder{}, starterFlow(t), nil))
	if res.Outcome != flow.OutcomeSkip || res.Reason != "encode.skip" {
		t.Fatalf("result = %+v, want skip encode.skip for a DV profile 5 source", res)
	}
}

// TestPlanFileIsTheSameAnswerAsTheRun pins PlanFile's shape: the starter on an H.264 file plans
// HEVC at quality 3's CRF, and an HEVC file has nothing to do.
func TestPlanFileIsTheSameAnswerAsTheRun(t *testing.T) {
	rev := flow.Revision{WorkflowID: 7, Rev: 1, Flow: starterFlow(t)}
	info := media.Info{
		Path:   "/library/clip.mkv",
		Format: media.Format{Names: []string{"matroska", "webm"}},
		Video:  []media.VideoTrack{{Index: 0, Codec: "h264", Width: 1920, Height: 1080}},
	}
	d, plan, err := flow.PlanFile(rev, info, flow.Env{})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !d.Work() {
		t.Errorf("decisions = %+v, want work", d.Steps)
	}
	if plan.Video.Codec != encode.CodecHEVC || plan.Video.CRF == nil || *plan.Video.CRF != 23 {
		t.Errorf("plan = %+v, want HEVC CRF 23", plan.Video)
	}
	if plan.Container != encode.ContainerMKV || filepath.ToSlash(plan.Output) != "/library/clip-shrunk.mkv" {
		t.Errorf("save = %q %q", plan.Container, plan.Output)
	}

	info.Video[0].Codec = "hevc"
	d, _, err = flow.PlanFile(rev, info, flow.Env{})
	if err != nil {
		t.Fatalf("plan hevc: %v", err)
	}
	if d.Work() {
		t.Errorf("an HEVC file planned work: %+v", d.Steps)
	}
	if d.Reasons()[0] != "video.already_target" || d.Reasons()[1] != "audio.no_audio" || d.Reasons()[2] != "save.nothing" {
		t.Errorf("reasons = %v", d.Reasons())
	}
}

// TestFlowJSONRoundTrips keeps the saved revision shape stable: a starter's flow survives the
// JSON the store writes.
func TestFlowJSONRoundTrips(t *testing.T) {
	f := starterFlow(t)
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back flow.Flow
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.Steps) != 3 || back.Steps[0].ID != "video" {
		t.Errorf("flow = %+v", back)
	}
	var settings map[string]any
	if err := json.Unmarshal(back.Steps[0].Settings, &settings); err != nil || settings["format"] != "hevc" {
		t.Errorf("settings = %v (%v)", settings, err)
	}
}
