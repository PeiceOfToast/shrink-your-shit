// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package library

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// eventRecorder keeps the scan's events and checks each one against the real bus, so a kind the
// bus does not know is a test failure rather than a silently dropped event.
type eventRecorder struct {
	bus    *events.Bus
	mu     sync.Mutex
	events []events.Event
	err    error
}

func newEventRecorder(t *testing.T) *eventRecorder {
	t.Helper()
	bus, err := events.New(events.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bus.Close(context.Background()) })
	return &eventRecorder{bus: bus}
}

func (r *eventRecorder) Emit(e events.Event) error {
	if err := r.bus.Emit(e); err != nil {
		r.mu.Lock()
		if r.err == nil {
			r.err = err
		}
		r.mu.Unlock()
		return err
	}
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
	return nil
}

func (r *eventRecorder) ofKind(kind events.Kind) []events.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []events.Event
	for _, e := range r.events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func (r *eventRecorder) firstErr() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// fakeProber answers with a small video for every file unless the test gives the file an error.
type fakeProber struct {
	mu      sync.Mutex
	answers map[string]error
	calls   []string
}

func (p *fakeProber) Probe(_ context.Context, path string) (media.Info, error) {
	name := filepath.Base(path)
	p.mu.Lock()
	p.calls = append(p.calls, name)
	err := p.answers[name]
	p.mu.Unlock()
	if err != nil {
		return media.Info{}, err
	}
	return media.Info{
		Path:   path,
		Format: media.Format{Names: []string{"mov"}, Size: 1 << 20, Duration: time.Second},
		Video:  []media.VideoTrack{{Codec: "h264", Width: 320, Height: 240}},
	}, nil
}

func (p *fakeProber) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

// queueGate makes the first Enqueue call wait until the test lets it go, so a scan can be caught
// mid-flight. Later calls pass straight through.
type queueGate struct {
	entered chan struct{}
	release chan struct{}
	taken   atomic.Bool
}

type queueRecorder struct {
	mu    sync.Mutex
	files []File
	err   error
	gate  *queueGate
}

func (q *queueRecorder) enqueue(_ context.Context, _ Library, file File) error {
	if q.gate != nil && q.gate.taken.CompareAndSwap(false, true) {
		close(q.gate.entered)
		<-q.gate.release
	}
	if q.err != nil {
		return q.err
	}
	q.mu.Lock()
	q.files = append(q.files, file)
	q.mu.Unlock()
	return nil
}

func (q *queueRecorder) names() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]string, len(q.files))
	for i, f := range q.files {
		out[i] = f.RelPath
	}
	return out
}

// scanTest is one library in a temp folder with every injected piece recorded.
type scanTest struct {
	t       *testing.T
	c       *Catalog
	db      *store.DB
	dir     string
	lib     Library
	scanner *Scanner
	prober  *fakeProber
	queue   *queueRecorder
	events  *eventRecorder
	sleeps  []time.Duration
	decide  func(File, media.Info) (bool, string)
	onSleep func()
}

func newScanTest(t *testing.T) *scanTest {
	t.Helper()
	return newScanTestAt(t, newFolder(t))
}

func newScanTestAt(t *testing.T, dir string) *scanTest {
	t.Helper()
	st := &scanTest{t: t, dir: dir}
	st.c, st.db = newTestCatalog(t)
	lib, err := st.c.AddLibrary(context.Background(), NewLibrary{Path: dir})
	if err != nil {
		t.Fatalf("add the library: %v", err)
	}
	st.lib = lib
	st.prober = &fakeProber{answers: map[string]error{}}
	st.queue = &queueRecorder{}
	st.events = newEventRecorder(t)
	st.decide = func(File, media.Info) (bool, string) { return true, "" }
	st.scanner = &Scanner{
		Catalog: st.c,
		Bus:     st.events,
		Prober:  st.prober,
		Decide:  func(f File, info media.Info) (bool, string) { return st.decide(f, info) },
		Enqueue: st.queue.enqueue,
		Now:     st.c.Now,
		Sleep: func(d time.Duration) {
			st.sleeps = append(st.sleeps, d)
			if st.onSleep != nil {
				st.onSleep()
			}
		},
	}
	return st
}

func (st *scanTest) write(name, body string) string {
	st.t.Helper()
	path := filepath.Join(st.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		st.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		st.t.Fatal(err)
	}
	return path
}

func (st *scanTest) run() ScanResult {
	st.t.Helper()
	res, err := st.scanner.Scan(context.Background(), st.lib)
	if err != nil {
		st.t.Fatalf("scan: %v", err)
	}
	if err := st.events.firstErr(); err != nil {
		st.t.Fatalf("the event bus refused a scan event: %v", err)
	}
	return res
}

func (st *scanTest) row(rel string) File {
	st.t.Helper()
	files, err := st.c.Files(context.Background(), st.lib.ID)
	if err != nil {
		st.t.Fatal(err)
	}
	for _, f := range files {
		if f.RelPath == rel {
			return f
		}
	}
	st.t.Fatalf("no catalog row for %s; rows: %+v", rel, files)
	return File{}
}

func (st *scanTest) lastScan() *ScanSummary {
	st.t.Helper()
	lib, err := st.c.GetLibrary(context.Background(), st.lib.ID)
	if err != nil {
		st.t.Fatal(err)
	}
	return lib.LastScan
}

func wantQueued(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("queued = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("queued = %v, want %v", got, want)
		}
	}
}

func TestScanQueuesANewSettledFile(t *testing.T) {
	st := newScanTest(t)
	st.write("clip.mp4", "0123456789")

	res := st.run()
	if res.Found != 1 || res.New != 1 || res.Queued != 1 || res.Skipped != 0 {
		t.Errorf("result = %+v, want 1 found, new and queued", res)
	}
	wantQueued(t, st.queue.names(), "clip.mp4")
	if st.prober.callCount() != 1 {
		t.Errorf("probe calls = %d, want 1", st.prober.callCount())
	}

	f := st.row("clip.mp4")
	if f.State != Queued || f.Probe == nil || f.Probe.Container != "mov" {
		t.Errorf("row = %+v, want queued with the probe summary", f)
	}
	if f.Size != 10 || f.FileID == "" {
		t.Errorf("row = %+v, want the size and the file id", f)
	}
	last := st.lastScan()
	if last == nil || last.Found != 1 || last.New != 1 || last.Queued != 1 || !last.At.Equal(testNow()) {
		t.Errorf("last scan = %+v, want this scan's counts", last)
	}
	lib, _ := st.c.GetLibrary(context.Background(), st.lib.ID)
	if lib.NextScanAt == nil || !lib.NextScanAt.After(testNow()) {
		t.Errorf("next scan = %v, want one armed after this scan", lib.NextScanAt)
	}

	starts := st.events.ofKind(events.KindScanStart)
	ends := st.events.ofKind(events.KindScanEnd)
	if len(starts) != 1 || len(ends) != 1 {
		t.Fatalf("events = %d start, %d end, want one of each", len(starts), len(ends))
	}
	end := ends[0]
	for field, want := range map[string]int{"found": 1, "new": 1, "queued": 1, "skipped": 0} {
		if got, ok := end.Data[field].(int); !ok || got != want {
			t.Errorf("scan.end %s = %v, want %d", field, end.Data[field], want)
		}
	}
	if end.LibraryID != "1" {
		t.Errorf("scan.end library = %q, want 1", end.LibraryID)
	}
}

func TestScanLeavesAnUnchangedFileAlone(t *testing.T) {
	st := newScanTest(t)
	st.write("clip.mp4", "0123456789")
	st.run()

	res := st.run()
	if res.Found != 1 || res.New != 0 || res.Changed != 0 || res.Queued != 0 {
		t.Errorf("second scan = %+v, want the file found but untouched", res)
	}
	if st.prober.callCount() != 1 {
		t.Errorf("probe calls = %d, want the unchanged file probed once in total", st.prober.callCount())
	}
	if len(st.sleeps) != 1 {
		t.Errorf("settle sleeps = %d, want no wait for an unchanged library", len(st.sleeps))
	}
}

func TestScanNoticesAChangedFile(t *testing.T) {
	st := newScanTest(t)
	st.write("clip.mp4", "0123456789")
	st.run()

	st.write("clip.mp4", "0123456789abcdef")
	res := st.run()
	if res.New != 0 || res.Changed != 1 || res.Queued != 1 {
		t.Errorf("second scan = %+v, want the file changed and queued again", res)
	}
	if got := st.row("clip.mp4").Size; got != 16 {
		t.Errorf("row size = %d, want 16", got)
	}
	if st.prober.callCount() != 2 {
		t.Errorf("probe calls = %d, want 2", st.prober.callCount())
	}
}

func TestScanMarksAMissingFileGone(t *testing.T) {
	st := newScanTest(t)
	path := st.write("clip.mp4", "0123456789")
	st.run()

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	res := st.run()
	if res.Gone != 1 || res.Queued != 0 {
		t.Errorf("second scan = %+v, want the file gone", res)
	}
	if got := st.row("clip.mp4").State; got != Gone {
		t.Errorf("row state = %q, want gone", got)
	}
	if last := st.lastScan(); last == nil || last.Gone != 1 {
		t.Errorf("last scan = %+v, want one gone", last)
	}
}

func TestScanReportsAMissingFolder(t *testing.T) {
	st := newScanTest(t)
	st.write("clip.mp4", "0123456789")
	st.run()
	if err := os.RemoveAll(st.dir); err != nil {
		t.Fatal(err)
	}

	res, err := st.scanner.Scan(context.Background(), st.lib)
	if err == nil {
		t.Fatal("scan of a removed folder succeeded, want an error")
	}
	// A root that is not there is not evidence that the files are: the scan errors and every
	// catalogued row keeps its state (#806 review).
	if res.Gone != 0 {
		t.Errorf("result = %+v, want nothing marked gone", res)
	}
	if f := st.row("clip.mp4"); f.State != Queued {
		t.Errorf("row = %+v, want its state kept", f)
	}
	if got := st.events.ofKind(events.KindError); len(got) != 1 {
		t.Errorf("error events = %d, want one naming the folder", len(got))
	}
}

func TestScanWaitsForAGrowingFile(t *testing.T) {
	st := newScanTest(t)
	st.write("clip.mp4", "0123456789")
	st.onSleep = func() {
		// The file is still being copied while the scan waits between its two looks.
		st.write("clip.mp4", "0123456789abcdefghij")
	}

	res := st.run()
	if res.StillChanging != 1 || res.Queued != 0 || res.New != 1 {
		t.Errorf("first scan = %+v, want one still changing, none queued", res)
	}
	wantQueued(t, st.queue.names())
	if f := st.row("clip.mp4"); f.State != StillChanging || f.Size != 20 {
		t.Errorf("row = %+v, want still_changing at the size the second look saw", f)
	}

	st.onSleep = nil
	res = st.run()
	if res.StillChanging != 0 || res.Queued != 1 {
		t.Errorf("second scan = %+v, want the settled file queued", res)
	}
	wantQueued(t, st.queue.names(), "clip.mp4")
	if f := st.row("clip.mp4"); f.State != Queued {
		t.Errorf("row state = %q, want queued", f.State)
	}
}

func TestScanNeverSettlesAnEmptyFile(t *testing.T) {
	st := newScanTest(t)
	st.write("clip.mp4", "")

	res := st.run()
	if res.StillChanging != 1 || res.Queued != 0 {
		t.Errorf("first scan = %+v, want an empty file still changing", res)
	}
	res = st.run()
	if res.StillChanging != 1 || res.Queued != 0 {
		t.Errorf("second scan = %+v, want it still changing; an empty file is never ready", res)
	}
	if st.prober.callCount() != 0 {
		t.Errorf("probe calls = %d, want none", st.prober.callCount())
	}
}

func TestScanSkipsANonMediaFile(t *testing.T) {
	st := newScanTest(t)
	st.write("notes.mp4", "not really a video")
	st.prober.answers["notes.mp4"] = media.ErrNotMedia

	res := st.run()
	if res.Skipped != 1 || res.SkippedBy[skipNotMedia] != 1 || res.Queued != 0 {
		t.Errorf("result = %+v, want one skip for not-media", res)
	}
	wantQueued(t, st.queue.names())
	if f := st.row("notes.mp4"); f.State != Skipped || f.SkipReason != skipNotMedia {
		t.Errorf("row = %+v, want skipped/not_media", f)
	}
}

func TestScanSkipsAFileThatVanishedBeforeTheProbe(t *testing.T) {
	st := newScanTest(t)
	path := st.write("clip.mp4", "0123456789")
	st.prober.answers["clip.mp4"] = &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}

	res := st.run()
	if res.SkippedBy[skipVanished] != 1 || res.Skipped != 1 {
		t.Errorf("result = %+v, want the vanished file skipped", res)
	}
	if warnings := st.events.ofKind(events.KindWarning); len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none: a file that left is not a problem to show", warnings)
	}
	ends := st.events.ofKind(events.KindScanEnd)
	if len(ends) != 1 || ends[0].Data["skipped"] != 1 {
		t.Errorf("scan.end = %+v, want the skipped count", ends)
	}
}

func TestScanKeepsAnUnreadableFileVisible(t *testing.T) {
	st := newScanTest(t)
	path := st.write("clip.mp4", "0123456789")
	st.prober.answers["clip.mp4"] = &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}

	res := st.run()
	if res.SkippedBy[skipUnreadable] != 1 {
		t.Errorf("result = %+v, want the file skipped as unreadable", res)
	}
	warnings := st.events.ofKind(events.KindWarning)
	if len(warnings) != 1 || warnings[0].Message == "" {
		t.Fatalf("warnings = %+v, want one naming the file", warnings)
	}
	ends := st.events.ofKind(events.KindScanEnd)
	if len(ends) != 1 {
		t.Fatalf("scan.end events = %d, want one", len(ends))
	}
	by, ok := ends[0].Data["skippedBy"].(map[string]int)
	if !ok || by[skipUnreadable] != 1 {
		t.Errorf("scan.end skippedBy = %v, want unreadable: 1 so the person can see it", ends[0].Data["skippedBy"])
	}
}

func TestScanSkipsSystemAndRecycleNames(t *testing.T) {
	st := newScanTest(t)
	for _, folder := range []string{".hidden", "@eaDir", "#recycle", "#snapshot", "System Volume Information", "lost+found", "$RECYCLE.BIN"} {
		st.write(folder+"/inside.mp4", "0123456789")
	}
	st.write(".hiddenclip.mp4", "0123456789")
	st.write("busy.syp-tmp", "0123456789")
	st.write("ok.mp4", "0123456789")

	res := st.run()
	if res.Found != 1 || res.Queued != 1 {
		t.Errorf("result = %+v, want only ok.mp4 found and queued", res)
	}
	wantQueued(t, st.queue.names(), "ok.mp4")
}

func TestScanNeverSkipsItsOwnRoot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".videos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	st := newScanTestAt(t, dir)
	st.write("clip.mp4", "0123456789")

	res := st.run()
	if res.Found != 1 || res.Queued != 1 {
		t.Errorf("result = %+v, want the file in a dot-named root folder found", res)
	}
	wantQueued(t, st.queue.names(), "clip.mp4")
}

func TestScanSkipsAnOutputFolderInsideTheLibrary(t *testing.T) {
	st := newScanTest(t)
	outputs := filepath.Join(st.dir, "converted")
	st.scanner.OutputDirs = func(Library) []string { return []string{outputs} }
	st.write("converted/out.mp4", "0123456789")
	st.write("ok.mp4", "0123456789")

	// The walk stops at the folder itself, so its files are never met, probed or counted as skips.
	res := st.run()
	if res.Found != 1 || res.Queued != 1 || res.Skipped != 0 {
		t.Errorf("result = %+v, want only ok.mp4 met and queued", res)
	}
	wantQueued(t, st.queue.names(), "ok.mp4")
}

func TestScanSendsProgressEveryNFiles(t *testing.T) {
	st := newScanTest(t)
	st.scanner.ProgressEvery = 2
	for _, name := range []string{"a.mp4", "b.mp4", "c.mp4"} {
		st.write(name, "0123456789")
	}

	res := st.run()
	if res.Queued != 3 {
		t.Fatalf("result = %+v, want three queued", res)
	}
	progress := st.events.ofKind(events.KindScanProgress)
	if len(progress) != 1 {
		t.Fatalf("progress events = %d, want one after the second file", len(progress))
	}
	if progress[0].Data["done"] != 2 || progress[0].Data["total"] != 3 {
		t.Errorf("progress = %+v, want 2 of 3", progress[0].Data)
	}
}

func TestScanCountsAnAlreadyOpenFileAsQueued(t *testing.T) {
	st := newScanTest(t)
	st.queue.err = ErrAlreadyQueued
	st.write("clip.mp4", "0123456789")

	res := st.run()
	if res.Queued != 1 || res.Skipped != 0 {
		t.Errorf("result = %+v, want an already-open file counted as queued", res)
	}
	if f := st.row("clip.mp4"); f.State != Queued || f.SkipReason != "" {
		t.Errorf("row = %+v, want queued with no skip reason", f)
	}
}

func TestScanKeepsTheDecideSkipReason(t *testing.T) {
	st := newScanTest(t)
	st.decide = func(File, media.Info) (bool, string) { return false, "already small" }
	st.write("clip.mp4", "0123456789")

	res := st.run()
	if res.SkippedBy["already small"] != 1 || res.Queued != 0 {
		t.Errorf("result = %+v, want the decide reason kept", res)
	}
	if f := st.row("clip.mp4"); f.State != Skipped || f.SkipReason != "already small" || f.Probe == nil {
		t.Errorf("row = %+v, want skipped with its reason and the probe", f)
	}
}

func TestScanFollowsARename(t *testing.T) {
	st := newScanTest(t)
	st.write("before.mp4", "0123456789")
	st.run()
	if err := os.Rename(filepath.Join(st.dir, "before.mp4"), filepath.Join(st.dir, "after.mp4")); err != nil {
		t.Fatal(err)
	}

	res := st.run()
	if res.New != 0 || res.Changed != 0 || res.Queued != 0 || res.Gone != 0 {
		t.Errorf("result = %+v, want the rename followed without a new queue", res)
	}
	if st.prober.callCount() != 1 {
		t.Errorf("probe calls = %d, want the renamed file not probed again", st.prober.callCount())
	}
	f := st.row("after.mp4")
	if f.State != Queued {
		t.Errorf("row = %+v, want the row followed to its new path, still queued", f)
	}
}

func TestSecondScanJoinsTheFirst(t *testing.T) {
	st := newScanTest(t)
	st.write("clip.mp4", "0123456789")
	st.queue.gate = &queueGate{entered: make(chan struct{}), release: make(chan struct{})}

	type answer struct {
		res ScanResult
		err error
	}
	first := make(chan answer, 1)
	go func() {
		res, err := st.scanner.Scan(context.Background(), st.lib)
		first <- answer{res, err}
	}()
	<-st.queue.gate.entered

	second := make(chan answer, 1)
	go func() {
		res, err := st.scanner.Scan(context.Background(), st.lib)
		second <- answer{res, err}
	}()
	select {
	case got := <-second:
		t.Fatalf("the second scan returned (%+v) while the first was still enqueuing; it did not join", got)
	case <-time.After(200 * time.Millisecond):
	}
	close(st.queue.gate.release)

	a, b := <-first, <-second
	if a.err != nil || b.err != nil {
		t.Fatalf("scan errors: %v, %v", a.err, b.err)
	}
	if !reflect.DeepEqual(a.res, b.res) {
		t.Errorf("joined result = %+v, want the running scan's %+v", b.res, a.res)
	}
	if st.prober.callCount() != 1 {
		t.Errorf("probe calls = %d, want the joining scan not to walk again", st.prober.callCount())
	}
	wantQueued(t, st.queue.names(), "clip.mp4")
}

func TestScanSkipsSymlinks(t *testing.T) {
	st := newScanTest(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "target.mp4"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "target.mp4"), filepath.Join(st.dir, "link.mp4")); err != nil {
		t.Skipf("this host will not make symlinks: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(st.dir, "linked")); err != nil {
		t.Fatalf("symlink a folder: %v", err)
	}
	st.write("ok.mp4", "0123456789")

	res := st.run()
	if res.Found != 1 || res.Queued != 1 {
		t.Errorf("result = %+v, want only the real file found and queued", res)
	}
	wantQueued(t, st.queue.names(), "ok.mp4")
}

// swapForSymlink moves the folder and leaves a link of the same name in its place, the way a NAS
// alias or a folder alias looks. It skips when the host will not make a link.
func swapForSymlink(t *testing.T, dir string) {
	t.Helper()
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, dir); err != nil {
		t.Skipf("this host will not make symlinks: %v", err)
	}
}

// TestScanFollowsASymlinkedRoot is the #806 review's P1: a library path that is itself a symlink
// must scan the folder it names. WalkDir does not follow a root link, so without resolving it the
// walk came back empty, looked complete, and marked the whole catalogue gone.
func TestScanFollowsASymlinkedRoot(t *testing.T) {
	st := newScanTest(t)
	st.write("a.mp4", "0123456789")
	st.write("b.mp4", "0123456789")
	st.run()
	wantQueued(t, st.queue.names(), "a.mp4", "b.mp4")

	swapForSymlink(t, st.dir)

	res := st.run()
	if res.Found != 2 || res.Gone != 0 || res.New != 0 || res.Changed != 0 {
		t.Errorf("second scan = %+v, want both files found and nothing gone", res)
	}
	for _, name := range []string{"a.mp4", "b.mp4"} {
		if f := st.row(name); f.State != Queued {
			t.Errorf("row %s = %+v, want its state kept", name, f)
		}
	}
	if queued := st.queue.names(); len(queued) != 2 {
		t.Errorf("queued = %v, want the files left alone", queued)
	}
}

// TestScanFindsAnOutputFolderUnderASymlinkedRoot checks the output-folder guard against the
// resolved root: the guard compares real paths, so a folder named through the link is still the
// folder the walk meets.
func TestScanFindsAnOutputFolderUnderASymlinkedRoot(t *testing.T) {
	st := newScanTest(t)
	st.write("ok.mp4", "0123456789")
	st.write("converted/out.mp4", "0123456789")
	swapForSymlink(t, st.dir)
	st.scanner.OutputDirs = func(Library) []string { return []string{filepath.Join(st.dir, "converted")} }

	// out.mp4 would be new to the walk, but the output folder is stopped at before it is met.
	res := st.run()
	if res.Found != 1 || res.Queued != 1 || res.Skipped != 0 {
		t.Errorf("scan = %+v, want only ok.mp4 met and queued", res)
	}
	wantQueued(t, st.queue.names(), "ok.mp4")
}

// TestWalkRefusesARootItDidNotEnter is the belt behind the resolved root: even handed a root the
// walk could not stand in, the walk is incomplete, and an incomplete walk marks nothing gone.
func TestWalkRefusesARootItDidNotEnter(t *testing.T) {
	st := newScanTest(t)
	st.write("a.mp4", "0123456789")
	swapForSymlink(t, st.dir)

	var res ScanResult
	res.SkippedBy = map[string]int{}
	seen, complete, err := st.scanner.walk(context.Background(), st.lib, st.dir, &res)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if complete {
		t.Error("walk of a root it never entered reported complete")
	}
	if len(seen) != 0 {
		t.Errorf("seen = %+v, want an empty walk", seen)
	}
	warnings := st.events.ofKind(events.KindWarning)
	if len(warnings) == 0 {
		t.Error("no warning event for the root that was not walked as a folder")
	}
}

// TestQueueFilesQueuesTheCountedFilesWithNoRescan: giving a needs_workflow library its first workflow
// queues the files the catalog already counted, without walking the folder again (page 09). A file
// written after the last count is not seen, a gone row is left alone, no scan events are sent, and
// the scan's summary and schedule are untouched.
func TestQueueFilesQueuesTheCountedFilesWithNoRescan(t *testing.T) {
	ctx := context.Background()
	st := newScanTest(t)
	st.decide = func(File, media.Info) (bool, string) { return false, "needs_workflow" }
	st.write("a.mp4", "aaaa")
	st.write("b.mp4", "bbbb")
	st.write("c.mp4", "cccc")
	st.run()
	// c leaves the folder and the next scan records it gone; d arrives and is never counted.
	if err := os.Remove(filepath.Join(st.dir, "c.mp4")); err != nil {
		t.Fatal(err)
	}
	st.run()
	st.write("d.mp4", "dddd")

	before, err := st.c.GetLibrary(ctx, st.lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	scanEvents := func() int {
		return len(st.events.ofKind(events.KindScanStart)) + len(st.events.ofKind(events.KindScanProgress)) +
			len(st.events.ofKind(events.KindScanEnd))
	}
	eventsBefore := scanEvents()

	st.decide = func(File, media.Info) (bool, string) { return true, "" }
	n, err := st.scanner.QueueFiles(ctx, st.lib)
	if err != nil {
		t.Fatalf("queue files: %v", err)
	}
	if n != 2 {
		t.Fatalf("queued %d files, want 2 (a and b; c is gone and d is not counted)", n)
	}
	wantQueued(t, st.queue.names(), "a.mp4", "b.mp4")
	for _, name := range []string{"a.mp4", "b.mp4"} {
		if got := st.row(name).State; got != Queued {
			t.Fatalf("%s state = %s, want queued", name, got)
		}
	}
	if got := st.row("c.mp4").State; got != Gone {
		t.Fatalf("gone file state = %s, want gone", got)
	}
	rows, err := st.c.Files(ctx, st.lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.RelPath == "d.mp4" {
			t.Fatal("queue files saw a file the last scan never counted")
		}
	}

	after, err := st.c.GetLibrary(ctx, st.lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.NextScanAt, after.NextScanAt) || !reflect.DeepEqual(before.LastScan, after.LastScan) {
		t.Fatalf("queue files moved the scan schedule or summary: before %+v, after %+v",
			before.LastScan, after.LastScan)
	}
	if got := scanEvents(); got != eventsBefore {
		t.Fatalf("queue files sent %d scan events, want none", got-eventsBefore)
	}
}

// TestQueueFilesKeepsTheDecideSkipReason: a counted file the workflow does not want is stored as
// skipped with the reason Decide gave, as a scan would store it.
func TestQueueFilesKeepsTheDecideSkipReason(t *testing.T) {
	ctx := context.Background()
	st := newScanTest(t)
	st.decide = func(File, media.Info) (bool, string) { return false, "needs_workflow" }
	st.write("clip.mp4", "0123456789")
	st.run()

	st.decide = func(File, media.Info) (bool, string) { return false, "already_small" }
	n, err := st.scanner.QueueFiles(ctx, st.lib)
	if err != nil {
		t.Fatalf("queue files: %v", err)
	}
	if n != 0 {
		t.Fatalf("queued %d files, want none", n)
	}
	row := st.row("clip.mp4")
	if row.State != Skipped || row.SkipReason != "already_small" {
		t.Fatalf("row state %s reason %q, want skipped with already_small", row.State, row.SkipReason)
	}
}

// TestQueueFilesLeavesAStillChangingFileForTheNextScan: a file the last scan saw mid-copy is not
// queued by QueueFiles, which settles nothing; the next scan settles it and queues it then (Finding 1
// of review-890).
func TestQueueFilesLeavesAStillChangingFileForTheNextScan(t *testing.T) {
	ctx := context.Background()
	st := newScanTest(t)
	st.write("clip.mp4", "0123456789")
	st.onSleep = func() {
		// Still being copied between the scan's two looks.
		st.write("clip.mp4", "0123456789abcdefghij")
	}
	if res := st.run(); res.StillChanging != 1 || res.Queued != 0 {
		t.Fatalf("first scan = %+v, want one still changing and none queued", res)
	}
	st.onSleep = nil

	st.decide = func(File, media.Info) (bool, string) { return true, "" }
	n, err := st.scanner.QueueFiles(ctx, st.lib)
	if err != nil {
		t.Fatalf("queue files: %v", err)
	}
	if n != 0 {
		t.Fatalf("queued %d files, want 0 while the file is still changing", n)
	}
	wantQueued(t, st.queue.names())
	if row := st.row("clip.mp4"); row.State != StillChanging {
		t.Fatalf("row state = %s, want still_changing", row.State)
	}

	// The copy has finished; the next scan settles it and queues it.
	if res := st.run(); res.Queued != 1 {
		t.Fatalf("next scan = %+v, want the settled file queued", res)
	}
	wantQueued(t, st.queue.names(), "clip.mp4")
}

// TestQueueFilesSkipsARowWhoseFileMovedSinceTheCount: a file that grew after the last count is not
// queued from the old row; the next scan settles it (Finding 1 of review-890).
func TestQueueFilesSkipsARowWhoseFileMovedSinceTheCount(t *testing.T) {
	ctx := context.Background()
	st := newScanTest(t)
	st.decide = func(File, media.Info) (bool, string) { return false, "needs_workflow" }
	st.write("clip.mp4", "0123")
	st.run()
	// It grew after the count (the size alone differs, whatever the mtime did).
	st.write("clip.mp4", "0123456789")

	st.decide = func(File, media.Info) (bool, string) { return true, "" }
	n, err := st.scanner.QueueFiles(ctx, st.lib)
	if err != nil {
		t.Fatalf("queue files: %v", err)
	}
	if n != 0 {
		t.Fatalf("queued %d files, want 0 for a row the file has outgrown", n)
	}
	wantQueued(t, st.queue.names())

	// The next scan takes the new facts and queues it.
	if res := st.run(); res.Queued != 1 {
		t.Fatalf("next scan = %+v, want the changed file queued", res)
	}
	wantQueued(t, st.queue.names(), "clip.mp4")
}
