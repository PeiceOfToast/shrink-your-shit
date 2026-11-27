package events

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fixedClock is the bus's clock in every test: 2026-11-27 03:12:45.120 UTC.
func fixedClock() time.Time {
	return time.Date(2026, 11, 27, 3, 12, 45, 120_000_000, time.UTC)
}

func clockFn() func() time.Time { return fixedClock }

// recorder is a sink that keeps every event it is given.
type recorder struct {
	mu  sync.Mutex
	got []Event
}

func (r *recorder) name() string       { return "recorder" }
func (r *recorder) accepts(Event) bool { return true }
func (r *recorder) close() error       { return nil }
func (r *recorder) write(e Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, e)
	return nil
}

func (r *recorder) events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.got...)
}

// blocked is a sink whose write waits until release is closed.
type blocked struct {
	release chan struct{}
	rec     recorder
}

func (b *blocked) name() string       { return "blocked" }
func (b *blocked) accepts(Event) bool { return true }
func (b *blocked) close() error       { return nil }
func (b *blocked) write(e Event) error {
	<-b.release
	return b.rec.write(e)
}

func note(msg string) Event {
	return Event{Kind: KindNote, Level: LevelInfo, Source: "Scanner", Message: msg}
}

func drain(ch <-chan Event) []Event {
	var out []Event
	for e := range ch {
		out = append(out, e)
	}
	return out
}

func TestBlockedSinkNeverBlocksEmit(t *testing.T) {
	release := make(chan struct{})
	blk := &blocked{release: release}
	rec := &recorder{}
	b, err := New(Options{Clock: clockFn(), Buffer: 4, sinks: []sink{blk, rec}})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	const total = 1000
	for i := 0; i < total; i++ {
		if err := b.Emit(note("burst")); err != nil {
			t.Fatalf("emit %d: %v", i, err)
		}
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("1000 emits against a blocked sink took %v; Emit must not wait", elapsed)
	}

	// Free the sink, then emit until a later accepted event reports the drops. The recorder shares the same
	// buffer size, so it may also drop during the burst and report its own drops; only the blocked sink's count is checked.
	close(release)
	for i := 0; i < 1000 && len(blockedReports(rec.events())) == 0; i++ {
		_ = b.Emit(note("after"))
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	reports := blockedReports(rec.events())
	if len(reports) != 1 {
		t.Fatalf("drop reports for the blocked sink = %d, want exactly 1", len(reports))
	}
	dropped, _ := reports[0].Data["dropped"].(int64)
	if dropped <= 0 {
		t.Fatalf("reported drops = %d, want > 0", dropped)
	}
	if got := len(blk.rec.events()); got >= total {
		t.Fatalf("blocked sink received %d events of %d; the drops should show as missing", got, total)
	}
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	b, err := New(Options{Clock: clockFn(), SubscriberBuffer: 64})
	if err != nil {
		t.Fatal(err)
	}
	slow, cancelSlow := b.Subscribe(Filter{})
	defer cancelSlow()
	good, cancelGood := b.Subscribe(Filter{})
	defer cancelGood()

	// The good subscriber is drained after every batch of 32, so it never holds more than 32 of its 64 slots.
	// The slow one never reads, so its buffer fills and it is dropped.
	const total = 200
	var got []Event
	for i := 0; i < total; i++ {
		if err := b.Emit(note("x")); err != nil {
			t.Fatal(err)
		}
		if (i+1)%32 == 0 {
		collect:
			for {
				select {
				case e := <-good:
					got = append(got, e)
				default:
					break collect
				}
			}
		}
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	got = append(got, drain(good)...)

	if len(got) != total {
		t.Fatalf("good subscriber got %d events, want %d", len(got), total)
	}
	for i, e := range got {
		if e.Seq != int64(i+1) {
			t.Fatalf("good subscriber event %d has seq %d, want %d", i, e.Seq, i+1)
		}
	}
	// The slow channel is closed: whatever it buffered, then nothing more.
	slowCount := 0
	timeout := time.After(time.Second)
	for {
		select {
		case _, ok := <-slow:
			if !ok {
				if slowCount >= total {
					t.Fatalf("slow subscriber was never dropped; it received all %d events", slowCount)
				}
				return
			}
			slowCount++
		case <-timeout:
			t.Fatal("slow subscriber channel never closed")
		}
	}
}

func TestProgressNeverReachesSystemFile(t *testing.T) {
	dir := t.TempDir()
	var stderr strings.Builder
	b, err := New(Options{DataDir: dir, Clock: clockFn(), Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	sub, cancel := b.Subscribe(Filter{})
	defer cancel()

	mustEmit(t, b, Event{Kind: KindProgress, Level: LevelInfo, JobID: 7, Message: "PROGRESS-MARK"})
	mustEmit(t, b, Event{Kind: KindWarning, Level: LevelWarn, JobID: 7, Message: "JOBWARN-MARK"})
	mustEmit(t, b, Event{Kind: KindNote, Level: LevelInfo, JobID: 7, Message: "JOBINFO-MARK"})
	mustEmit(t, b, Event{Kind: KindNote, Level: LevelInfo, Source: "Scanner", Message: "APPINFO-MARK"})
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	subGot := drain(sub)
	if len(subGot) != 4 || subGot[0].Kind != KindProgress {
		t.Fatalf("subscriber got %d events, want all 4 with progress first", len(subGot))
	}
	text := readFile(t, filepath.Join(dir, "logs", "system.log"))
	if strings.Contains(text, "PROGRESS-MARK") {
		t.Fatal("progress reached the system log")
	}
	if strings.Contains(text, "JOBINFO-MARK") {
		t.Fatal("a job info event reached the system log; only job warnings and errors do")
	}
	if !strings.Contains(text, "JOBWARN-MARK") || !strings.Contains(text, "APPINFO-MARK") {
		t.Fatalf("system log is missing an expected line:\n%s", text)
	}
	if strings.Contains(stderr.String(), "PROGRESS-MARK") {
		t.Fatal("progress reached stderr")
	}
}

func TestDebugDroppedWithoutDebugOption(t *testing.T) {
	for _, debug := range []bool{false, true} {
		b, err := New(Options{Clock: clockFn(), Debug: debug})
		if err != nil {
			t.Fatal(err)
		}
		sub, cancel := b.Subscribe(Filter{})
		defer cancel()
		mustEmit(t, b, Event{Kind: KindNote, Level: LevelDebug, Message: "DEBUG-MARK"})
		if err := b.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		got := len(drain(sub))
		if debug && got != 1 {
			t.Fatalf("Debug on: subscriber got %d events, want 1", got)
		}
		if !debug && got != 0 {
			t.Fatalf("Debug off: subscriber got %d events, want 0", got)
		}
	}
}

func TestKeepFollowsKind(t *testing.T) {
	b, err := New(Options{Clock: clockFn()})
	if err != nil {
		t.Fatal(err)
	}
	sub, cancel := b.Subscribe(Filter{})
	defer cancel()
	// The caller sets keep the wrong way round on both; Emit must overwrite both.
	mustEmit(t, b, Event{Kind: KindRunEnd, Level: LevelInfo, JobID: 1, Keep: KeepLog})
	mustEmit(t, b, Event{Kind: KindDecision, Level: LevelInfo, JobID: 1, Keep: KeepJob})
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := drain(sub)
	if got[0].Keep != KeepJob {
		t.Fatalf("run_end keep = %q, want job", got[0].Keep)
	}
	if got[1].Keep != KeepLog {
		t.Fatalf("decision keep = %q, want log", got[1].Keep)
	}
}

func TestSystemLogRotatesAndKeepsFive(t *testing.T) {
	dir := t.TempDir()
	const limit = 300
	b, err := New(Options{DataDir: dir, Clock: clockFn(), LogMaxBytes: limit, LogKeep: 5, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		mustEmit(t, b, Event{Kind: KindNote, Level: LevelInfo, Source: "Scanner", Message: fmt.Sprintf("line %04d", i)})
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	logs := filepath.Join(dir, "logs")
	for i := 1; i <= 5; i++ {
		name := filepath.Join(logs, "system.log."+strconv.Itoa(i))
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("expected %s after rotation: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(logs, "system.log.6")); !os.IsNotExist(err) {
		t.Fatalf("system.log.6 exists; only 5 old files are kept (err=%v)", err)
	}
	// Each file holds whole lines within the limit, and every line has the agreed format.
	line := regexp.MustCompile(`^2026-11-27 03:12:45\.120 INFO  Scanner  line \d{4}$`)
	for _, name := range []string{"system.log", "system.log.1", "system.log.5"} {
		text := readFile(t, filepath.Join(logs, name))
		if int64(len(text)) > limit {
			t.Fatalf("%s is %d bytes, over the %d limit", name, len(text), limit)
		}
		for _, l := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
			if !line.MatchString(l) {
				t.Fatalf("%s line %q does not match the format", name, l)
			}
		}
	}
}

func TestSystemLogModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	dir := t.TempDir()
	b, err := New(Options{DataDir: dir, Clock: clockFn(), Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	mustEmit(t, b, note("hello"))
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	checkMode(t, filepath.Join(dir, "logs"), 0o750)
	checkMode(t, filepath.Join(dir, "logs", "system.log"), 0o640)
}

func TestSystemLogFormatCarriesWarnAndJobSource(t *testing.T) {
	got := formatSystemLine(Event{
		TS: fixedClock(), Level: LevelWarn, Source: "Scanner", Message: "disk\nfull",
	})
	want := "2026-11-27 03:12:45.120 WARN  Scanner  disk full\n"
	if got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
	if got := formatSystemLine(Event{TS: fixedClock(), Level: LevelError, Message: "x"}); !strings.Contains(got, "ERROR -        x") {
		t.Fatalf("missing source renders as %q", got)
	}
}

func TestSeqIsGapFreePerJob(t *testing.T) {
	b, err := New(Options{Clock: clockFn()})
	if err != nil {
		t.Fatal(err)
	}
	sub7, cancel7 := b.Subscribe(Filter{JobID: 7})
	defer cancel7()
	sub8, cancel8 := b.Subscribe(Filter{JobID: 8})
	defer cancel8()
	all, cancelAll := b.Subscribe(Filter{})
	defer cancelAll()

	mustEmit(t, b, Event{Kind: KindNote, Level: LevelInfo, JobID: 7})
	mustEmit(t, b, Event{Kind: KindNote, Level: LevelInfo, JobID: 8})
	mustEmit(t, b, Event{Kind: KindNote, Level: LevelInfo, JobID: 7})
	mustEmit(t, b, Event{Kind: KindNote, Level: LevelInfo, Message: "app"})
	mustEmit(t, b, Event{Kind: KindNote, Level: LevelInfo, JobID: 8})
	mustEmit(t, b, Event{Kind: KindNote, Level: LevelInfo, JobID: 7})
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	seqs := func(events []Event) []int64 {
		var out []int64
		for _, e := range events {
			out = append(out, e.Seq)
		}
		return out
	}
	if got := seqs(drain(sub7)); !equal(got, []int64{1, 2, 3}) {
		t.Fatalf("job 7 seqs = %v, want [1 2 3]", got)
	}
	if got := seqs(drain(sub8)); !equal(got, []int64{1, 2}) {
		t.Fatalf("job 8 seqs = %v, want [1 2]", got)
	}
	// Every event, in emit order: the app event is the 4th and takes the first global seq.
	got := drain(all)
	if len(got) != 6 || got[3].JobID != 0 || got[3].Seq != 1 {
		t.Fatalf("app event = job %d seq %d, want job 0 seq 1 (events %v)", got[3].JobID, got[3].Seq, seqs(got))
	}
}

func TestUnknownKindAndLevelRefused(t *testing.T) {
	b, err := New(Options{Clock: clockFn()})
	if err != nil {
		t.Fatal(err)
	}
	sub, cancel := b.Subscribe(Filter{})
	defer cancel()
	if err := b.Emit(Event{Kind: "nope", Level: LevelInfo}); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if err := b.Emit(Event{Kind: KindNote, Level: "loud"}); err == nil {
		t.Fatal("unknown level accepted")
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(drain(sub)); n != 0 {
		t.Fatalf("refused events reached a subscriber: %d", n)
	}
}

func TestClockStampsZeroTS(t *testing.T) {
	b, err := New(Options{Clock: clockFn()})
	if err != nil {
		t.Fatal(err)
	}
	sub, cancel := b.Subscribe(Filter{})
	defer cancel()
	explicit := time.Date(2026, 1, 2, 3, 4, 5, 999_999_999, time.UTC)
	mustEmit(t, b, Event{Kind: KindNote, Level: LevelInfo})
	mustEmit(t, b, Event{Kind: KindNote, Level: LevelInfo, TS: explicit})
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := drain(sub)
	if !got[0].TS.Equal(fixedClock()) {
		t.Fatalf("zero TS stamped %v, want the clock's %v", got[0].TS, fixedClock())
	}
	if want := explicit.Truncate(time.Millisecond); !got[1].TS.Equal(want) {
		t.Fatalf("explicit TS = %v, want %v (to the millisecond)", got[1].TS, want)
	}
}

func TestEmitAfterCloseRefused(t *testing.T) {
	b, err := New(Options{Clock: clockFn()})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := b.Emit(note("late")); err != ErrClosed {
		t.Fatalf("emit after close = %v, want ErrClosed", err)
	}
}

func TestCloseFlushesSinks(t *testing.T) {
	rec := &recorder{}
	b, err := New(Options{Clock: clockFn(), sinks: []sink{rec}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		mustEmit(t, b, note("flush"))
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(rec.events()); n != 10 {
		t.Fatalf("after Close the sink has %d of 10 events", n)
	}
}

func mustEmit(t *testing.T, b *Bus, e Event) {
	t.Helper()
	if err := b.Emit(e); err != nil {
		t.Fatalf("emit: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func checkMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

// blockedReports is the drop reports that name the blocked sink.
func blockedReports(events []Event) []Event {
	var out []Event
	for _, e := range events {
		if e.Kind == KindWarning && e.Data["sink"] == "blocked" {
			out = append(out, e)
		}
	}
	return out
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
