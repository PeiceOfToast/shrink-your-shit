package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// Batching: a batch is written when it reaches dbBatchSize events or dbFlushEvery has passed, whichever is first.
const (
	dbBatchSize  = 200
	dbFlushEvery = 250 * time.Millisecond
	// dbBuffer is the db sink's channel size. Seq is assigned gap-free at Emit, so a drop at this sink (only under
	// extreme backpressure) leaves a gap in a job's stored seq. The large buffer makes that unlikely.
	dbBuffer = 8192
	// maxPage caps Query's limit.
	maxPage = 1000
)

func init() {
	store.Register(store.Migration{Version: 2, Name: "events", Up: createEvents})
}

// eventColumns is the table's column order. insertSQL, eventArgs and scanEvent all follow it.
var eventColumns = []string{
	"seq", "ts", "level", "kind", "code", "source", "job_id", "attempt",
	"step_id", "step_index", "step_type", "step_name", "library_id", "worker_id",
	"message", "port", "port_label", "reason", "duration_ms", "command",
	"size_before", "size_after", "next_action", "data", "keep",
}

var insertSQL = "INSERT INTO events(" + strings.Join(eventColumns, ", ") + ") VALUES(" +
	strings.TrimSuffix(strings.Repeat("?, ", len(eventColumns)), ", ") + ")"

// createEvents is migration 2. backend-plan §4 lists the core columns; the step, port, command and size fields
// are stored too, because the job page and the text download render them.
func createEvents(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE events (
			id          INTEGER PRIMARY KEY,
			seq         INTEGER NOT NULL,
			ts          INTEGER NOT NULL,
			level       TEXT    NOT NULL,
			kind        TEXT    NOT NULL,
			code        TEXT    NOT NULL DEFAULT '',
			source      TEXT    NOT NULL DEFAULT '',
			job_id      INTEGER NOT NULL DEFAULT 0,
			attempt     INTEGER NOT NULL DEFAULT 0,
			step_id     TEXT    NOT NULL DEFAULT '',
			step_index  INTEGER NOT NULL DEFAULT 0,
			step_type   TEXT    NOT NULL DEFAULT '',
			step_name   TEXT    NOT NULL DEFAULT '',
			library_id  TEXT    NOT NULL DEFAULT '',
			worker_id   TEXT    NOT NULL DEFAULT '',
			message     TEXT    NOT NULL DEFAULT '',
			port        TEXT    NOT NULL DEFAULT '',
			port_label  TEXT    NOT NULL DEFAULT '',
			reason      TEXT    NOT NULL DEFAULT '',
			duration_ms INTEGER NOT NULL DEFAULT 0,
			command     TEXT    NOT NULL DEFAULT '',
			size_before INTEGER NOT NULL DEFAULT 0,
			size_after  INTEGER NOT NULL DEFAULT 0,
			next_action TEXT    NOT NULL DEFAULT '',
			data        TEXT    NOT NULL DEFAULT '',
			keep        TEXT    NOT NULL
		)`,
		// job_id 0 is an app event (no job).
		`CREATE INDEX events_job_seq ON events(job_id, seq)`,
		`CREATE INDEX events_ts ON events(ts)`,
		`CREATE INDEX events_level_seq ON events(level, seq)`,
		`CREATE INDEX events_source_seq ON events(source, seq)`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// dbSink writes events in batches to the events table. It is the only sink that is queried.
type dbSink struct {
	db  *store.DB
	bus *Bus

	mu    sync.Mutex // guards batch and err; taken before the bus lock, never after it
	batch []Event
	err   error

	stop    chan struct{}
	stopped chan struct{}
}

func newDBSink(db *store.DB, bus *Bus) *dbSink {
	s := &dbSink{db: db, bus: bus, stop: make(chan struct{}), stopped: make(chan struct{})}
	go s.flushEvery()
	return s
}

func (s *dbSink) name() string { return "db" }

// accepts takes everything but progress, which is for live subscribers only.
func (s *dbSink) accepts(e Event) bool { return e.Kind != KindProgress }

func (s *dbSink) write(e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batch = append(s.batch, e)
	if len(s.batch) >= dbBatchSize {
		return s.flushLocked()
	}
	return nil
}

func (s *dbSink) flushEvery() {
	defer close(s.stopped)
	t := time.NewTicker(dbFlushEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.mu.Lock()
			_ = s.flushLocked()
			s.mu.Unlock()
		case <-s.stop:
			return
		}
	}
}

// close stops the timer and writes the last batch. Bus.Close calls it after the sink's channel is drained.
func (s *dbSink) close() error {
	close(s.stop)
	<-s.stopped
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.flushLocked()
	return s.err
}

// flushLocked writes the batch in one transaction. It records the first error and always reports the batch as
// committed or failed to the bus, so a job's pending count can never stay up.
func (s *dbSink) flushLocked() error {
	if len(s.batch) == 0 {
		return nil
	}
	rows := s.batch
	s.batch = nil
	err := s.db.Tx(context.Background(), func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(insertSQL)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, e := range rows {
			args, err := eventArgs(e)
			if err != nil {
				return err
			}
			if _, err := stmt.Exec(args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil && s.err == nil {
		s.err = fmt.Errorf("write %d events: %w", len(rows), err)
	}
	s.bus.committed(rows)
	return err
}

func eventArgs(e Event) ([]any, error) {
	command := ""
	if e.Command != nil {
		b, err := json.Marshal(e.Command)
		if err != nil {
			return nil, fmt.Errorf("encode command: %w", err)
		}
		command = string(b)
	}
	data := ""
	if e.Data != nil {
		b, err := json.Marshal(e.Data)
		if err != nil {
			return nil, fmt.Errorf("encode data: %w", err)
		}
		data = string(b)
	}
	return []any{
		e.Seq, e.TS.UnixMilli(), string(e.Level), string(e.Kind), e.Code, e.Source, e.JobID, e.Attempt,
		e.StepID, e.StepIndex, e.StepType, e.StepName, e.LibraryID, e.WorkerID,
		e.Message, e.Port, e.PortLabel, e.Reason, e.DurationMS, command,
		e.SizeBefore, e.SizeAfter, e.NextAction, data, string(e.Keep),
	}, nil
}

// committed is called by the db sink after each batch. It releases the pending count that seq seeding waits on.
func (b *Bus) committed(rows []Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, e := range rows {
		if e.JobID == 0 {
			continue
		}
		b.pending[e.JobID]--
		if b.pending[e.JobID] <= 0 {
			delete(b.pending, e.JobID)
		}
	}
	b.cond.Broadcast()
}

// seedLocked gives a job its seq counter the first time this bus sees it, from the largest seq in the database.
// It waits until the job's accepted events are all written, so the database maximum is the true one.
// The caller holds b.mu.
func (b *Bus) seedLocked(job int64) error {
	if _, ok := b.jobSeq[job]; ok {
		return nil
	}
	for b.pending[job] > 0 {
		b.cond.Wait()
		if _, ok := b.jobSeq[job]; ok {
			return nil
		}
	}
	var last int64
	if b.db != nil {
		err := b.db.Read().QueryRow("SELECT COALESCE(MAX(seq), 0) FROM events WHERE job_id = ?", job).Scan(&last)
		if err != nil {
			return fmt.Errorf("read the last seq of job %d: %w", job, err)
		}
	}
	b.jobSeq[job] = last
	return nil
}

// Page is where a Query starts and how many events it returns. Cursor is the seq of the last event of the
// previous page, as returned in the next Page; zero starts at the beginning (oldest first) or the end (newest first).
type Page struct {
	Limit  int
	Newest bool
	Cursor int64
}

// Query reads stored events matching f, one page at a time, from the read pool. Results are in insertion order,
// which is seq order within a job. A page shorter than its limit is the last one.
func (b *Bus) Query(ctx context.Context, f Filter, p Page) ([]Event, Page, error) {
	if b.db == nil {
		return nil, Page{}, errors.New("events: no database is open for queries")
	}
	switch {
	case p.Limit <= 0:
		p.Limit = 100
	case p.Limit > maxPage:
		p.Limit = maxPage
	}

	var where []string
	var args []any
	add := func(cond string, a ...any) {
		where = append(where, cond)
		args = append(args, a...)
	}
	if f.JobID != 0 {
		add("job_id = ?", f.JobID)
	}
	if f.Kind != "" {
		add("kind = ?", string(f.Kind))
	}
	if f.Attempt != 0 {
		add("attempt = ?", f.Attempt)
	}
	if f.MinLevel != "" {
		levels := levelsAtLeast(f.MinLevel)
		if len(levels) == 0 {
			return nil, Page{}, fmt.Errorf("events: unknown level %q", f.MinLevel)
		}
		marks := strings.TrimSuffix(strings.Repeat("?, ", len(levels)), ", ")
		var vals []any
		for _, l := range levels {
			vals = append(vals, string(l))
		}
		add("level IN ("+marks+")", vals...)
	}
	if f.Source != "" {
		add("source = ?", f.Source)
	}
	if f.StepID != "" {
		add("step_id = ?", f.StepID)
	}
	if !f.Since.IsZero() {
		add("ts >= ?", f.Since.UnixMilli())
	}
	if !f.Until.IsZero() {
		add("ts <= ?", f.Until.UnixMilli())
	}
	if f.AfterSeq > 0 {
		add("seq > ?", f.AfterSeq)
	}
	order := "ASC"
	if p.Newest {
		order = "DESC"
	}
	// Paging is by seq, which is per job: a page walk is meant to be scoped to one JobID.
	if p.Cursor > 0 {
		if p.Newest {
			add("seq < ?", p.Cursor)
		} else {
			add("seq > ?", p.Cursor)
		}
	}

	q := "SELECT id, " + strings.Join(eventColumns, ", ") + " FROM events"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY seq " + order + ", id " + order + " LIMIT ?"
	args = append(args, p.Limit)

	rows, err := b.db.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, Page{}, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var out []Event
	cursor := p.Cursor
	for rows.Next() {
		var id int64
		e, err := scanEvent(rows, &id)
		if err != nil {
			return nil, Page{}, err
		}
		out = append(out, e)
		cursor = e.Seq
	}
	if err := rows.Err(); err != nil {
		return nil, Page{}, fmt.Errorf("read events: %w", err)
	}
	return out, Page{Limit: p.Limit, Newest: p.Newest, Cursor: cursor}, nil
}

func scanEvent(rows *sql.Rows, id *int64) (Event, error) {
	var e Event
	var ts int64
	var level, kind, command, data, keep string
	err := rows.Scan(id, &e.Seq, &ts, &level, &kind, &e.Code, &e.Source, &e.JobID, &e.Attempt,
		&e.StepID, &e.StepIndex, &e.StepType, &e.StepName, &e.LibraryID, &e.WorkerID,
		&e.Message, &e.Port, &e.PortLabel, &e.Reason, &e.DurationMS, &command,
		&e.SizeBefore, &e.SizeAfter, &e.NextAction, &data, &keep)
	if err != nil {
		return Event{}, fmt.Errorf("read an event row: %w", err)
	}
	e.TS = time.UnixMilli(ts).UTC()
	e.Level = Level(level)
	e.Kind = Kind(kind)
	e.Keep = Keep(keep)
	if command != "" {
		e.Command = &Command{}
		if err := json.Unmarshal([]byte(command), e.Command); err != nil {
			return Event{}, fmt.Errorf("decode command of event %d: %w", *id, err)
		}
	}
	if data != "" {
		if err := json.Unmarshal([]byte(data), &e.Data); err != nil {
			return Event{}, fmt.Errorf("decode data of event %d: %w", *id, err)
		}
	}
	return e, nil
}

// levelsAtLeast is the levels that rank at or above min.
func levelsAtLeast(min Level) []Level {
	if min.rank() < 0 {
		return nil
	}
	var out []Level
	for _, l := range []Level{LevelDebug, LevelInfo, LevelWarn, LevelError} {
		if l.rank() >= min.rank() {
			out = append(out, l)
		}
	}
	return out
}
