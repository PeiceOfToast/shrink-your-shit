package events

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrClosed is returned by Emit after Close.
var ErrClosed = errors.New("the event bus is closed")

// Options configure a Bus. Zero values take the defaults noted on each field.
type Options struct {
	// DataDir is the data folder. The system log is written to DataDir/logs/system.log. Empty means no file.
	DataDir string
	// Debug lets debug events through. Without it they are dropped at Emit.
	Debug bool
	// Clock stamps events whose TS is zero. Default time.Now.
	Clock func() time.Time
	// Stderr receives each system-log line as well. Default os.Stderr.
	Stderr io.Writer
	// Buffer is each sink's channel size. Default 1024.
	Buffer int
	// SubscriberBuffer is each subscriber's channel size. Default 256.
	SubscriberBuffer int
	// LogMaxBytes is the size at which system.log rotates. Default 10 MB.
	LogMaxBytes int64
	// LogKeep is how many rotated files to keep (system.log.1 … .N). Default 5.
	LogKeep int

	// sinks are extra sinks, for tests in this package.
	sinks []sink
}

// Bus takes events from the app and the steps, stores them in its sinks and gives them to subscribers.
// Emit never waits on a sink or a subscriber.
type Bus struct {
	mu         sync.Mutex
	opts       Options
	clock      func() time.Time
	sinks      []*sinkRun
	subs       map[int]*subscriber
	nextSub    int
	nextGlobal int64
	jobSeq     map[int64]int64
	closed     bool
}

// sink receives events on its own goroutine. write is only called from that goroutine.
type sink interface {
	name() string
	accepts(Event) bool
	write(Event) error
	close() error
}

type sinkRun struct {
	s     sink
	ch    chan Event
	drops int64 // guarded by Bus.mu
	done  chan struct{}
	err   error // written by the sink goroutine; read after done is closed
}

type subscriber struct {
	filter Filter
	ch     chan Event
}

// New starts a bus. It fails if the system log folder cannot be made.
func New(opts Options) (*Bus, error) {
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Buffer <= 0 {
		opts.Buffer = 1024
	}
	if opts.SubscriberBuffer <= 0 {
		opts.SubscriberBuffer = 256
	}
	if opts.LogMaxBytes <= 0 {
		opts.LogMaxBytes = 10 << 20
	}
	if opts.LogKeep <= 0 {
		opts.LogKeep = 5
	}

	sinks := append([]sink(nil), opts.sinks...)
	if opts.DataDir != "" {
		sf, err := openSystemFile(filepath.Join(opts.DataDir, "logs"), opts)
		if err != nil {
			return nil, err
		}
		sinks = append(sinks, sf)
	}

	b := &Bus{
		opts:   opts,
		clock:  opts.Clock,
		subs:   map[int]*subscriber{},
		jobSeq: map[int64]int64{},
	}
	for _, s := range sinks {
		r := &sinkRun{s: s, ch: make(chan Event, opts.Buffer), done: make(chan struct{})}
		b.sinks = append(b.sinks, r)
		go r.run()
	}
	return b, nil
}

func (r *sinkRun) run() {
	defer close(r.done)
	for e := range r.ch {
		if err := r.s.write(e); err != nil && r.err == nil {
			r.err = fmt.Errorf("%s sink: %w", r.s.name(), err)
		}
	}
	if err := r.s.close(); err != nil && r.err == nil {
		r.err = fmt.Errorf("%s sink: %w", r.s.name(), err)
	}
}

// Emit records one event. It returns at once: a full sink drops the event for that sink only and counts the drop.
// It refuses an unknown kind or level. Debug events are dropped silently unless the bus was opened with Debug.
// The Keep field is set from the kind here, whatever the caller set.
func (b *Bus) Emit(e Event) error {
	if err := e.validate(); err != nil {
		return err
	}
	if e.Level == LevelDebug && !b.opts.Debug {
		return nil
	}
	e.Keep = keepFor(e.Kind)
	if e.TS.IsZero() {
		e.TS = b.clock()
	}
	e.TS = e.TS.UTC().Truncate(time.Millisecond)

	type report struct {
		name string
		n    int64
	}
	var reports []report

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrClosed
	}
	if e.JobID != 0 {
		b.jobSeq[e.JobID]++
		e.Seq = b.jobSeq[e.JobID]
	} else {
		b.nextGlobal++
		e.Seq = b.nextGlobal
	}

	for _, r := range b.sinks {
		if !r.s.accepts(e) {
			continue
		}
		select {
		case r.ch <- e:
			if r.drops > 0 {
				reports = append(reports, report{r.s.name(), r.drops})
				r.drops = 0
			}
		default:
			r.drops++
		}
	}

	// Progress reaches subscribers only; the sinks' accepts refuse it. A subscriber that cannot keep up is dropped.
	for id, s := range b.subs {
		if !s.filter.match(e) {
			continue
		}
		select {
		case s.ch <- e:
		default:
			close(s.ch)
			delete(b.subs, id)
		}
	}
	b.mu.Unlock()

	for _, rp := range reports {
		b.reportDrops(rp.name, rp.n)
	}
	return nil
}

// reportDrops says, once, how many events a sink missed. It runs outside the lock. Its own Emit cannot report
// again in the same call: a report is made only when a sink accepts an event after drops, and a report event
// that is itself dropped is counted and reported later.
func (b *Bus) reportDrops(name string, n int64) {
	_ = b.Emit(Event{
		Kind:    KindWarning,
		Level:   LevelWarn,
		Source:  "events",
		Message: fmt.Sprintf("%d events were dropped for the %s sink: it could not keep up", n, name),
		Data:    map[string]any{"sink": name, "dropped": n},
	})
}

// Subscribe returns the events that match f, in order. A subscriber that stops reading is dropped: its channel
// closes, and the caller should resync. cancel stops the subscription and closes the channel.
func (b *Bus) Subscribe(f Filter) (<-chan Event, func()) {
	ch := make(chan Event, b.opts.SubscriberBuffer)
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	id := b.nextSub
	b.nextSub++
	b.subs[id] = &subscriber{filter: f, ch: ch}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if s, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(s.ch)
		}
	}
	return ch, cancel
}

// Close stops the bus and flushes every sink. It returns the first sink error, or ctx's error if the flush ran out of time.
func (b *Bus) Close(ctx context.Context) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	for _, r := range b.sinks {
		close(r.ch)
	}
	for id, s := range b.subs {
		close(s.ch)
		delete(b.subs, id)
	}
	b.mu.Unlock()

	var first error
	for _, r := range b.sinks {
		select {
		case <-r.done:
			if r.err != nil && first == nil {
				first = r.err
			}
		case <-ctx.Done():
			if first == nil {
				first = ctx.Err()
			}
			return first
		}
	}
	return first
}
