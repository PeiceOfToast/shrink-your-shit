// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media/fake"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// trySafety is the file boundary for a try: its staging is a real hidden folder in the folder the
// try gave it (so a leftover would show there), and its output is a file of a size the test picks.
type trySafety struct {
	verify safety.Verdict
	// verifyErr makes the check unable to run.
	verifyErr error
	// size is the staged output's size; 0 means smaller than any test source.
	size int
	// staged closes when the staging folder exists, so a test can stop the run mid-encode.
	staged chan struct{}

	folder   string
	placedTo string
	gotSrc   media.Info
}

func (f *trySafety) Stage(base, dst string) (safety.Staged, error) {
	dir := base
	if dir == "" {
		dir = filepath.Dir(dst)
	}
	folder, err := os.MkdirTemp(dir, ".out.*.syp-tmp")
	if err != nil {
		return safety.Staged{}, err
	}
	f.folder = folder
	path := filepath.Join(folder, "out"+filepath.Ext(dst))
	if err := os.WriteFile(path, make([]byte, f.size), 0o600); err != nil {
		os.RemoveAll(folder)
		return safety.Staged{}, err
	}
	if f.staged != nil {
		select {
		case <-f.staged:
		default:
			close(f.staged)
		}
	}
	return safety.Staged{Folder: folder, Path: path}, nil
}

func (f *trySafety) Verify(ctx context.Context, p media.Prober, rec safety.Recorder, st safety.Staged, src media.Info, want safety.Want, timeout time.Duration) (safety.Verdict, error) {
	f.gotSrc = src
	return f.verify, f.verifyErr
}

func (f *trySafety) Place(ctx context.Context, st safety.Staged, final string) (string, error) {
	f.placedTo = final
	if err := os.WriteFile(final, []byte("placed"), 0o600); err != nil {
		return "", err
	}
	return final, os.RemoveAll(st.Folder)
}

// tryMedia is fake.Media plus the two facts cutting a sample adds: once the cut has run, the
// sample file exists, and probing it answers with the sample's own length. The test cannot name
// the sample path in advance (it is a fresh temp folder), so the fake answers for whatever
// non-source path the try probes, and leaves the cut's own output behind as ffmpeg would.
type tryMedia struct {
	*fake.Media
	src    string
	whole  media.Info
	sample time.Duration
}

func (m *tryMedia) Probe(ctx context.Context, path string) (media.Info, error) {
	if path != m.src && m.sample > 0 {
		info := m.whole
		info.Path = path
		info.Format.Duration = m.sample
		return info, nil
	}
	return m.Media.Probe(ctx, path)
}

func (m *tryMedia) Run(ctx context.Context, argv []string, onProgress func(media.Progress)) (media.Result, error) {
	res, err := m.Media.Run(ctx, argv, onProgress)
	if err == nil && m.sample > 0 && strings.Contains(strings.Join(argv, " "), " -c copy ") {
		os.WriteFile(argv[len(argv)-1], []byte("sample"), 0o600)
	}
	return res, err
}

// tryInput is libraryFile with a duration the test chooses: the bytes on disk are small, the
// media it claims to be is long.
func trySource(t *testing.T, duration time.Duration) (root, src string, info media.Info) {
	t.Helper()
	root, src, info = libraryFile(t, "clip.mkv", "h264")
	info.Format.Duration = duration
	return root, src, info
}

// tryEnv is newEnv without a revision loader: a try brings its own flow.
func tryEnv(m media.Media, sf flow.Safety) flow.Env {
	return flow.Env{
		Media: m, Safety: sf, Pick: flow.ProcessorPick,
		Now: func() time.Time { return time.Unix(0, 0) }, Sleep: func(time.Duration) {},
		AppVersion: "test",
	}
}

// entries is a folder's names in order, so a before and after can be compared.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(list))
	for _, e := range list {
		names = append(names, e.Name())
	}
	return names
}

func texts(lines []flow.TryLine) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.Text)
	}
	return out
}

// TestATryNeverWritesBesideTheOriginal is S2-5's first rule: the try runs the real engine, the
// result check passes, and the file's folder is exactly as it was — no output, no staging.
func TestATryNeverWritesBesideTheOriginal(t *testing.T) {
	root, src, info := trySource(t, 60*time.Second)
	before := entries(t, root)
	sourceBefore, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	m := &fake.Media{Probes: map[string]media.Info{src: info}, Runs: []fake.RunScript{{}}}
	sf := &trySafety{verify: safety.Verdict{OK: true}, size: 7}
	scratch := t.TempDir()
	res, err := flow.Try(context.Background(), starterFlow(t), src, tryEnv(m, sf), scratch)
	if err != nil {
		t.Fatalf("try: %v", err)
	}
	if sf.placedTo != "" {
		t.Errorf("the try placed a file at %s", sf.placedTo)
	}
	// The staging is the app's temporary folder, never the original's: a killed process leaves
	// nothing for the person to find beside their video.
	if sf.folder == "" || !strings.HasPrefix(sf.folder, scratch+string(filepath.Separator)) {
		t.Errorf("the try staged in %q, want a folder under the app's %s", sf.folder, scratch)
	}
	if got := entries(t, root); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Errorf("the folder changed: %v -> %v", before, got)
	}
	sourceAfter, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("the original is gone: %v", err)
	}
	if string(sourceAfter) != string(sourceBefore) {
		t.Errorf("the original changed: %d -> %d bytes", len(sourceBefore), len(sourceAfter))
	}
	// Each step's own line, then the size line for the whole file (60 s is under TryWhole).
	if len(res.Lines) != 4 {
		t.Fatalf("lines = %q, want three step lines and the size line", texts(res.Lines))
	}
	for i, id := range []string{"video", "audio", "save", "save"} {
		if res.Lines[i].StepID != id {
			t.Errorf("line %d is %s, want %s", i, res.Lines[i].StepID, id)
		}
	}
	if got := res.Lines[len(res.Lines)-1].Text; got != "Size: 7 B (now 600 B)." {
		t.Errorf("size line = %q", got)
	}
}

// TestATryEncodesOneMinuteFromAThirdIn: a long file is tried on a stream-copied 60 s sample from
// a third of the way in, the engine encodes the sample itself, and the check compares against the
// sample's own measured length. The sample is cut into the folder the app gave the try (a real
// copy of the video could fill a small system temp) and is gone when the try ends.
func TestATryEncodesOneMinuteFromAThirdIn(t *testing.T) {
	root, src, info := trySource(t, 2*time.Hour)
	before := entries(t, root)
	scratch := t.TempDir()
	mf := &fake.Media{Probes: map[string]media.Info{src: info}, Runs: []fake.RunScript{{}, {}}}
	m := &tryMedia{Media: mf, src: src, whole: info, sample: 60 * time.Second}
	sf := &trySafety{verify: safety.Verdict{OK: true}}
	if _, err := flow.Try(context.Background(), starterFlow(t), src, tryEnv(m, sf), scratch); err != nil {
		t.Fatalf("try: %v", err)
	}
	argvs := mf.Argvs()
	if len(argvs) != 2 {
		t.Fatalf("runs = %d, want the cut and then the engine's encode", len(argvs))
	}

	cut := argvs[0]
	joined := strings.Join(cut, " ")
	if !strings.Contains(joined, "-ss 2400.000 -t 60.000 -i ") {
		t.Errorf("cut = %q, want the sample bounded before -i", joined)
	}
	if !strings.Contains(joined, " -c copy ") {
		t.Errorf("cut = %q, want a stream copy so the audio stays aligned", joined)
	}
	sample := cut[len(cut)-1]
	if filepath.Base(sample) != "sample.mkv" || !strings.HasPrefix(filepath.Dir(sample), scratch+string(filepath.Separator)) {
		t.Errorf("cut writes %q, want a sample in a fresh folder under the app's %s", sample, scratch)
	}

	enc := strings.Join(argvs[1], " ")
	if strings.Contains(enc, " -ss ") || strings.Contains(enc, " -t ") {
		t.Errorf("encode = %q, want the sample encoded whole", enc)
	}
	if !strings.Contains(enc, " -i "+sample+" ") {
		t.Errorf("encode = %q, want the sample %s as its only input", enc, sample)
	}
	if got := sf.gotSrc.Format.Duration; got != 60*time.Second {
		t.Errorf("the check compared against %s, want the sample's own length", got)
	}

	// Neither the sample's folder nor the staging outlives the try, and the source's folder never
	// held either.
	if got := entries(t, scratch); len(got) != 0 {
		t.Errorf("the app's try folder still holds %v after the try", got)
	}
	if got := entries(t, root); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Errorf("the folder changed: %v -> %v", before, got)
	}
}

// TestASampleIsAlwaysAnMkv: the sample does not inherit the source's container, which may refuse
// the streams a stream copy carries (subtitles into AVI or TS, for one). The engine re-encodes
// from the sample, so an MKV loses nothing.
func TestASampleIsAlwaysAnMkv(t *testing.T) {
	root, src, info := trySource(t, 2*time.Hour)
	ts := filepath.Join(root, "clip.ts")
	if err := os.Rename(src, ts); err != nil {
		t.Fatal(err)
	}
	info.Path = ts
	mf := &fake.Media{Probes: map[string]media.Info{ts: info}, Runs: []fake.RunScript{{}, {}}}
	m := &tryMedia{Media: mf, src: ts, whole: info, sample: 60 * time.Second}
	sf := &trySafety{verify: safety.Verdict{OK: true}}
	if _, err := flow.Try(context.Background(), starterFlow(t), ts, tryEnv(m, sf), t.TempDir()); err != nil {
		t.Fatalf("try: %v", err)
	}
	argvs := mf.Argvs()
	if len(argvs) != 2 {
		t.Fatalf("runs = %d, want the cut and then the engine's encode", len(argvs))
	}
	cut := argvs[0]
	sample := cut[len(cut)-1]
	if filepath.Base(sample) != "sample.mkv" {
		t.Errorf("cut writes %q, want sample.mkv whatever the source's container", sample)
	}
	if enc := strings.Join(argvs[1], " "); !strings.Contains(enc, " -i "+sample+" ") {
		t.Errorf("encode = %q, want the MKV sample %s as its input", enc, sample)
	}
}

// TestATryOfAShortFileUsesTheWholeFile: nothing is cut, and the check compares the whole length.
func TestATryOfAShortFileUsesTheWholeFile(t *testing.T) {
	_, src, info := trySource(t, 150*time.Second)
	m := &fake.Media{Probes: map[string]media.Info{src: info}, Runs: []fake.RunScript{{}}}
	sf := &trySafety{verify: safety.Verdict{OK: true}}
	if _, err := flow.Try(context.Background(), starterFlow(t), src, tryEnv(m, sf), t.TempDir()); err != nil {
		t.Fatalf("try: %v", err)
	}
	if argvs := m.Argvs(); len(argvs) != 1 {
		t.Fatalf("runs = %d, want only the engine's encode", len(argvs))
	}
	if joined := strings.Join(m.Argvs()[0], " "); strings.Contains(joined, " -ss ") || strings.Contains(joined, " -t ") {
		t.Errorf("a short file was cut: %q", joined)
	}
	if got := sf.gotSrc.Format.Duration; got != 150*time.Second {
		t.Errorf("the check compared against %s, want the whole file", got)
	}
}

// TestATrySaysWhatTheSizeLineSays pins ui-shop's sentences: the estimate says so, the whole
// file's size does not, a bigger result says the original would be kept, and a failed check says
// why and that a real run would keep the original.
func TestATrySaysWhatTheSizeLineSays(t *testing.T) {
	cases := []struct {
		name     string
		duration time.Duration
		size     int
		verify   safety.Verdict
		want     string
	}{
		{
			name: "a sample's estimate", duration: time.Hour, size: 7,
			verify: safety.Verdict{OK: true},
			want:   "Size: about 420 B for the whole file (now 600 B), estimated from a one-minute sample.",
		},
		{
			name: "the whole file", duration: 60 * time.Second, size: 7,
			verify: safety.Verdict{OK: true},
			want:   "Size: 7 B (now 600 B).",
		},
		{
			name: "a sample bigger than the original", duration: time.Hour, size: 100,
			verify: safety.Verdict{OK: true},
			want:   "Size: about 6.0 KB, bigger than the original 600 B, so the original would be kept.",
		},
		{
			name: "the whole file bigger than the original", duration: 60 * time.Second, size: 700,
			verify: safety.Verdict{OK: true},
			want:   "Size: 700 B, bigger than the original 600 B, so the original would be kept.",
		},
		{
			name: "a failed result check", duration: 2 * time.Hour, size: 7,
			verify: safety.Verdict{OK: false, Code: safety.CodeTooShort, Message: "the result is 30s, the source 1m0s", NextAction: safety.NextKeepOriginal},
			want:   "The result check failed: the result is 30s, the source 1m0s. A real run would keep the original.",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, src, info := trySource(t, c.duration)
			mf := &fake.Media{Probes: map[string]media.Info{src: info}, Runs: []fake.RunScript{{}, {}}}
			var m media.Media = mf
			if c.duration > flow.TryWhole {
				m = &tryMedia{Media: mf, src: src, whole: info, sample: 60 * time.Second}
			}
			sf := &trySafety{verify: c.verify, size: c.size}
			res, err := flow.Try(context.Background(), starterFlow(t), src, tryEnv(m, sf), t.TempDir())
			if err != nil {
				t.Fatalf("try: %v", err)
			}
			last := res.Lines[len(res.Lines)-1]
			if last.StepID != "save" || last.Text != c.want {
				t.Errorf("last line = %s: %q, want save: %q", last.StepID, last.Text, c.want)
			}
			if c.verify.OK {
				if len(res.Lines) != 4 {
					t.Errorf("lines = %q, want three step lines and the size line", texts(res.Lines))
				}
			} else if len(res.Lines) != 3 {
				t.Errorf("lines = %q, want the three step lines with the check's sentence on Save", texts(res.Lines))
			}
		})
	}
}

// TestAStoppedTryLeavesNothingBehind: the request's context reaches ffmpeg, and the staging it
// made is gone when the run stops.
func TestAStoppedTryLeavesNothingBehind(t *testing.T) {
	root, src, info := trySource(t, 2*time.Hour)
	before := entries(t, root)
	scratch := t.TempDir()
	mf := &fake.Media{Probes: map[string]media.Info{src: info}, Runs: []fake.RunScript{{}, {Hang: true}}}
	m := &tryMedia{Media: mf, src: src, whole: info, sample: 60 * time.Second}
	sf := &trySafety{verify: safety.Verdict{OK: true}, staged: make(chan struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-sf.staged
		cancel()
	}()
	_, err := flow.Try(ctx, starterFlow(t), src, tryEnv(m, sf), scratch)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("try error = %v, want context.Canceled", err)
	}
	if got := entries(t, scratch); len(got) != 0 {
		t.Errorf("a stopped try left %v in the app's try folder", got)
	}
	if got := entries(t, root); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Errorf("a stopped try left %v behind (was %v)", got, before)
	}
}

// TestATryRefusesWhatItCannotRead answers the page's own sentence for each refusal.
func TestATryRefusesWhatItCannotRead(t *testing.T) {
	root, src, info := trySource(t, 60*time.Second)
	cases := []struct {
		name    string
		file    string
		m       media.Media
		code    string
		message string
	}{
		{
			// The page has the file in hand and it is gone by the time the try reads it: the
			// probe's own stat error speaks, and the page answers "choose it again", not 500.
			name: "gone since the page listed it", file: filepath.Join(root, "missing.mkv"),
			m:    &fake.Media{ProbeErrors: map[string]error{filepath.Join(root, "missing.mkv"): fmt.Errorf("probe: %w", os.ErrNotExist)}},
			code: "invalid_file", message: "The file is missing or cannot be read.",
		},
		{
			name: "unreadable", file: filepath.Join(root, "missing.mkv"),
			m:    &fake.Media{ProbeErrors: map[string]error{filepath.Join(root, "missing.mkv"): fmt.Errorf("probe: %w", os.ErrPermission)}},
			code: "invalid_file", message: "The file is missing or cannot be read.",
		},
		{"not media", src, &fake.Media{ProbeErrors: map[string]error{src: media.ErrNotMedia}}, "invalid_file", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := flow.Try(context.Background(), starterFlow(t), c.file, tryEnv(c.m, &trySafety{verify: safety.Verdict{OK: true}}), t.TempDir())
			var refuse *flow.TryError
			if !errors.As(err, &refuse) || refuse.Code != c.code {
				t.Fatalf("error = %v, want %s", err, c.code)
			}
			if c.message != "" && !strings.Contains(refuse.Message, c.message) {
				t.Errorf("message = %q, want %q", refuse.Message, c.message)
			}
		})
	}

	// A file with no video is not something a video flow can try.
	audioOnly := info
	audioOnly.Video = nil
	m := &fake.Media{Probes: map[string]media.Info{src: audioOnly}}
	_, err := flow.Try(context.Background(), starterFlow(t), src, tryEnv(m, &trySafety{verify: safety.Verdict{OK: true}}), t.TempDir())
	var refuse *flow.TryError
	if !errors.As(err, &refuse) || refuse.Code != "invalid_file" || !strings.Contains(refuse.Message, "no video") {
		t.Errorf("audio-only error = %v, want invalid_file saying there is no video", err)
	}
}
