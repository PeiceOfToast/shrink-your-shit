// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package jobs stores files and transcode jobs in SQLite and enforces the
// job state machine. Store implements worker.Queue, which is the seam a
// remote worker would later talk to over HTTP.
package jobs

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, no cgo
)

// Status of a job.
type Status string

const (
	Queued  Status = "queued"
	Running Status = "running"
	Done    Status = "done"
	Failed  Status = "failed"
)

// allowed lists the legal transitions. Failed jobs may be retried (back to queued).
var allowed = map[Status][]Status{
	Queued:  {Running},
	Running: {Done, Failed, Queued}, // Queued: worker went away, put it back
	Failed:  {Queued},
	Done:    {},
}

// CanTransition reports whether from -> to is legal.
func CanTransition(from, to Status) bool {
	for _, s := range allowed[from] {
		if s == to {
			return true
		}
	}
	return false
}

// ErrNoJob is returned by Claim when nothing is queued.
var ErrNoJob = errors.New("no queued job")

// Job is one transcode of one file.
type Job struct {
	ID          int64   `json:"id"`
	Library     string  `json:"library"`
	Path        string  `json:"path"`
	Status      Status  `json:"status"`
	Progress    float64 `json:"progress"`
	Reason      string  `json:"reason"`
	Error       string  `json:"error,omitempty"`
	Worker      string  `json:"worker,omitempty"`
	DurationSec float64 `json:"durationSec"`
	SizeBefore  int64   `json:"sizeBefore"`
	SizeAfter   int64   `json:"sizeAfter,omitempty"`
	OutputPath  string  `json:"outputPath,omitempty"`
	Created     string  `json:"created"`
	Updated     string  `json:"updated"`
}

// File is what the scanner last saw for a path, so unchanged files are not re-probed.
type File struct {
	Path     string
	Size     int64
	ModTime  int64
	Codec    string
	Decision string
}

// Store is the SQLite-backed store.
type Store struct {
	db   *sql.DB
	path string
	now  func() time.Time
}

// Open opens (creating if needed) the database at path and brings its schema up to date
// (see migrate.go). It refuses a database written by a newer program (ErrNewerDatabase).
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path) // plain path: no URI query, so Windows drive letters and backslashes are safe
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: one writer; keeps claims atomic and simple
	s := &Store{db: db, path: path, now: time.Now}
	// Look at the schema version before changing anything (even the journal mode writes to the file).
	if err := s.checkNotNewer(); err != nil {
		db.Close()
		return nil, err
	}
	// One connection only (above), so these settings stay in force for the whole process.
	if _, err := db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;`); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) stamp() string { return s.now().UTC().Format(time.RFC3339) }

// GetFile returns the last seen record for path (nil if never seen).
func (s *Store) GetFile(path string) (*File, error) {
	var f File
	err := s.db.QueryRow(`SELECT path,size,mtime,codec,decision FROM files WHERE path=?`, path).
		Scan(&f.Path, &f.Size, &f.ModTime, &f.Codec, &f.Decision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &f, err
}

// PutFile records what was seen for a path.
func (s *Store) PutFile(f File) error {
	_, err := s.db.Exec(`INSERT INTO files(path,size,mtime,codec,decision) VALUES(?,?,?,?,?)
ON CONFLICT(path) DO UPDATE SET size=excluded.size,mtime=excluded.mtime,codec=excluded.codec,decision=excluded.decision`,
		f.Path, f.Size, f.ModTime, f.Codec, f.Decision)
	return err
}

// Enqueue adds a queued job.
func (s *Store) Enqueue(j Job) (int64, error) {
	now := s.stamp()
	r, err := s.db.Exec(`INSERT INTO jobs(library,path,status,reason,duration,size_before,created,updated)
VALUES(?,?,?,?,?,?,?,?)`, j.Library, j.Path, Queued, j.Reason, j.DurationSec, j.SizeBefore, now, now)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

const cols = `id,library,path,status,progress,reason,error,worker,duration,size_before,size_after,output_path,created,updated`

func scan(r interface{ Scan(...any) error }) (Job, error) {
	var j Job
	err := r.Scan(&j.ID, &j.Library, &j.Path, &j.Status, &j.Progress, &j.Reason, &j.Error, &j.Worker,
		&j.DurationSec, &j.SizeBefore, &j.SizeAfter, &j.OutputPath, &j.Created, &j.Updated)
	return j, err
}

// Get returns one job.
func (s *Store) Get(id int64) (Job, error) {
	return scan(s.db.QueryRow(`SELECT `+cols+` FROM jobs WHERE id=?`, id))
}

// List returns the newest jobs first.
func (s *Store) List(limit int) ([]Job, error) {
	rows, err := s.db.Query(`SELECT `+cols+` FROM jobs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// transition moves a job to a new status, rejecting illegal moves.
// extra is an optional "col=?" fragment list applied in the same update.
func (s *Store) transition(id int64, to Status, set string, args ...any) error {
	j, err := s.Get(id)
	if err != nil {
		return err
	}
	if !CanTransition(j.Status, to) {
		return fmt.Errorf("job %d: cannot go from %s to %s", id, j.Status, to)
	}
	q := `UPDATE jobs SET status=?, updated=?` + set + ` WHERE id=? AND status=?`
	a := append([]any{to, s.stamp()}, args...)
	a = append(a, id, j.Status)
	res, err := s.db.Exec(q, a...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("job %d changed under us", id)
	}
	return nil
}

// Claim hands the oldest queued job to worker and marks it running.
func (s *Store) Claim(worker string) (Job, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM jobs WHERE status=? ORDER BY id LIMIT 1`, Queued).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNoJob
	}
	if err != nil {
		return Job{}, err
	}
	if err := s.transition(id, Running, `, worker=?, progress=0, error=''`, worker); err != nil {
		return Job{}, err
	}
	return s.Get(id)
}

// HasOpenJob reports whether path already has a queued or running job.
func (s *Store) HasOpenJob(path string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE path=? AND status IN (?,?)`, path, Queued, Running).Scan(&n)
	return n > 0, err
}

// Progress records percent done for a running job.
func (s *Store) Progress(id int64, pct float64) error {
	_, err := s.db.Exec(`UPDATE jobs SET progress=?, updated=? WHERE id=? AND status=?`, pct, s.stamp(), id, Running)
	return err
}

// Complete marks a running job done.
func (s *Store) Complete(id int64, outputPath string, sizeAfter int64) error {
	return s.transition(id, Done, `, progress=100, output_path=?, size_after=?`, outputPath, sizeAfter)
}

// Fail marks a running job failed.
func (s *Store) Fail(id int64, msg string) error {
	return s.transition(id, Failed, `, error=?`, msg)
}

// Retry puts a failed job back in the queue.
func (s *Store) Retry(id int64) error {
	return s.transition(id, Queued, `, progress=0, error='', worker=''`)
}

// Release puts a running job back in the queue (the worker was told to stop).
func (s *Store) Release(id int64) error {
	return s.transition(id, Queued, `, progress=0, worker=''`)
}

// RequeueRunning puts jobs left "running" by a crashed process back in the queue.
// Call once at startup, before workers start.
func (s *Store) RequeueRunning() (int64, error) {
	r, err := s.db.Exec(`UPDATE jobs SET status=?, progress=0, worker='', updated=? WHERE status=?`,
		Queued, s.stamp(), Running)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}
