// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package jobs stores files and transcode jobs in SQLite and enforces the
// job state machine. Store implements worker.Queue, which is the seam a
// remote worker would later talk to over HTTP.
package jobs

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
	// Kept means the conversion ran (or was about to) but the original was deliberately left alone
	// because a safety check said no; the reason is in Job.Error. Not "done", not a crash.
	Kept Status = "kept"
)

// allowed lists the legal transitions. Failed jobs may be retried (back to queued).
var allowed = map[Status][]Status{
	Queued:  {Running},
	Running: {Done, Failed, Kept, Queued}, // Queued: worker went away, put it back
	Failed:  {Queued},
	Kept:    {Queued},
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
	Library     string  `json:"library"`   // the library's folder when the job was made (kept as text)
	LibraryID   int64   `json:"libraryId"` // 0: made before libraries were records, and not matched since
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
	Plan        string  `json:"-"`                         // the Plan it ran with, JSON (RecordPlan)
	PlanPrint   string  `json:"planFingerprint,omitempty"` // its conversion settings' fingerprint
	Created     string  `json:"created"`
	Updated     string  `json:"updated"`
	Attempts    int     `json:"attempts"`          // tries finished (failed, kept, or waiting to be tried again: DD-2)
	RetryAt     string  `json:"retryAt,omitempty"` // a queued job waiting to be tried again: not before then (RFC3339)
}

// File is what the scanner last saw for a path, so unchanged files are not re-probed.
type File struct {
	Path     string
	Size     int64
	ModTime  int64
	Codec    string
	Decision string
	Reason   string // a skip's code (rules.Skip*, "unreadable", "restored"); "" when it is not skipped
}

// Store is the SQLite-backed store.
type Store struct {
	db   *sql.DB
	path string
	now  func() time.Time

	// OnJobsRemoved is told the ids of jobs deleted (S5: their run logs go too); nil: nobody.
	OnJobsRemoved func(ids []int64)

	removeMu sync.Mutex     // held by Claim and StartLibrary while they look at removing
	removing map[int64]bool // libraries being removed (BeginRemove): process-local, never stored
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
	if _, err := db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;`); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.EnsureRevisions(); err != nil { // migration 20's revision 1 of each flow (made in Go)
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
	err := s.db.QueryRow(`SELECT path,size,mtime,COALESCE(codec,''),COALESCE(decision,''),reason FROM files WHERE path=?`, path).
		Scan(&f.Path, &f.Size, &f.ModTime, &f.Codec, &f.Decision, &f.Reason)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &f, err
}

// PutFile records what was seen for a path.
func (s *Store) PutFile(f File) error { return putFile(s.db, f) }

func putFile(x execer, f File) error {
	_, err := x.Exec(`INSERT INTO files(path,size,mtime,codec,decision,reason) VALUES(?,?,?,?,?,?)
ON CONFLICT(path) DO UPDATE SET size=excluded.size,mtime=excluded.mtime,codec=excluded.codec,decision=excluded.decision,reason=excluded.reason`,
		f.Path, f.Size, f.ModTime, f.Codec, f.Decision, f.Reason)
	return err
}

// GetProbe returns the stored probe (JSON) of path if it was taken at this size and modification time.
func (s *Store) GetProbe(path string, size, mtime int64) (string, bool) {
	var info string
	err := s.db.QueryRow(`SELECT info FROM probes WHERE path=? AND size=? AND mtime=?`, path, size, mtime).Scan(&info)
	return info, err == nil
}

// PutProbe stores what ffprobe said about path at this size and modification time.
func (s *Store) PutProbe(path string, size, mtime int64, info string) error {
	_, err := s.db.Exec(`INSERT INTO probes(path,size,mtime,info) VALUES(?,?,?,?)
ON CONFLICT(path) DO UPDATE SET size=excluded.size,mtime=excluded.mtime,info=excluded.info`, path, size, mtime, info)
	return err
}

// Enqueue adds a queued job.
func (s *Store) Enqueue(j Job) (int64, error) {
	now := s.stamp()
	if j.LibraryID == 0 {
		r, err := s.db.Exec(`INSERT INTO jobs(library,path,status,reason,duration,size_before,created,updated)
VALUES(?,?,?,?,?,?,?,?)`, j.Library, j.Path, Queued, j.Reason, j.DurationSec, j.SizeBefore, now, now)
		if err != nil {
			return 0, err
		}
		return r.LastInsertId()
	}
	// One statement: the job is added only if its library still exists and is started, so a library
	// removed (or a scan that started before the removal) cannot leave a job behind.
	// V5b: it takes its library's priority.
	r, err := s.db.Exec(`INSERT INTO jobs(library,library_id,path,status,reason,duration,size_before,created,updated,priority)
SELECT ?,?,?,?,?,?,?,?,?,priority FROM libraries WHERE id=? AND started=1`,
		j.Library, j.LibraryID, j.Path, Queued, j.Reason, j.DurationSec, j.SizeBefore, now, now, j.LibraryID)
	if err != nil {
		return 0, err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return 0, ErrLibraryGone
	}
	return r.LastInsertId()
}

const cols = `id,library,COALESCE(library_id,0),path,status,progress,reason,error,worker,duration,size_before,size_after,output_path,COALESCE(plan,''),COALESCE(plan_fingerprint,''),created,updated,attempts,retry_at`

func scan(r interface{ Scan(...any) error }) (Job, error) {
	var j Job
	err := r.Scan(&j.ID, &j.Library, &j.LibraryID, &j.Path, &j.Status, &j.Progress, &j.Reason, &j.Error, &j.Worker,
		&j.DurationSec, &j.SizeBefore, &j.SizeAfter, &j.OutputPath, &j.Plan, &j.PlanPrint, &j.Created, &j.Updated, &j.Attempts, &j.RetryAt)
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
// One statement, so several workers (parallelJobs) never take the same job.
func (s *Store) Claim(worker string) (Job, error) {
	now := s.stamp()
	var id int64
	// One statement, so a pause or a stop request set before it is always seen: no job is taken while
	// paused, and none whose stop was requested.
	// It takes no job of a library being removed (BeginRemove), under the same lock, so after BeginRemove
	// returns no claim for that library can start.
	s.removeMu.Lock()
	skip, args := "", []any{Running, now, worker, now, Queued}
	for lib := range s.removing {
		skip += " AND (library_id IS NULL OR library_id != ?)" // a job made before libraries belongs to none
		args = append(args, lib)
	}
	// A job waiting to be tried again (DD-2) is not taken before its time.
	args = append(args, now)
	err := s.db.QueryRow(`UPDATE jobs SET status=?, updated=?, worker=?, progress=0, error='', error_code='', started=?
WHERE id=(SELECT id FROM jobs WHERE status=? AND stop_requested=0`+skip+` AND (retry_at='' OR retry_at<=?) AND `+libraryOnSQL+`
  ORDER BY priority DESC, id LIMIT 1) AND status=?
AND NOT EXISTS (SELECT 1 FROM app_meta WHERE key='paused' AND value='1') RETURNING id`,
		append(args, Queued)...).Scan(&id)
	s.removeMu.Unlock()
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNoJob
	}
	if err != nil {
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

// RecordPlan stores the Plan a running job converts with (JSON) and its fingerprint.
func (s *Store) RecordPlan(id int64, plan, fingerprint string) error {
	_, err := s.db.Exec(`UPDATE jobs SET plan=?, plan_fingerprint=?, updated=? WHERE id=?`, plan, fingerprint, s.stamp(), id)
	return err
}

// Progress records percent done for a running job.
func (s *Store) Progress(id int64, pct float64) error {
	_, err := s.db.Exec(`UPDATE jobs SET progress=?, updated=? WHERE id=? AND status=?`, pct, s.stamp(), id, Running)
	return err
}

// Complete marks a running job done.
func (s *Store) Complete(id int64, outputPath string, sizeAfter int64) error {
	// The output as it is now, so the file can later be recognised as this conversion's (Activity's
	// Skipped list leaves it out only while it still is). 0 if it cannot be read.
	var mtime int64
	if st, err := os.Stat(outputPath); err == nil {
		mtime = st.ModTime().UnixNano()
	}
	// the disk the original's folder is on now, while it surely is there: an original left in place is
	// only ever counted as removed while its folder is still on that disk (a nested mount unmounted shows
	// an empty folder on another disk: review-ae6749e P2)
	disk := ""
	if j, err := s.Get(id); err == nil {
		disk, _ = diskOf(filepath.Dir(j.Path))
	}
	return s.transition(id, Done, `, progress=100, output_path=?, size_after=?, output_mtime=?, original_disk=?`, outputPath, sizeAfter, mtime, disk)
}

// Fail marks a running job failed: code is one of ReasonCodes, msg the plain words.
func (s *Store) Fail(id int64, code, msg string) error {
	return s.transition(id, Failed, `, error=?, error_code=?, attempts=attempts+1, retry_at=''`, msg, code)
}

// RetryLater puts a running job back in the queue after a failure that may pass (DD-2): one more try
// counted, not taken before at; reason (its plain words) stays on it meanwhile.
func (s *Store) RetryLater(id int64, at time.Time, code, reason string) error {
	return s.transition(id, Queued, `, progress=0, worker='', error=?, error_code=?, attempts=attempts+1, retry_at=?`, reason, code, at.UTC().Format(time.RFC3339))
}

// Keep marks a running job "kept original": a safety check refused the result, code and reason say why.
func (s *Store) Keep(id int64, code, reason string) error {
	return s.transition(id, Kept, `, error=?, error_code=?, attempts=attempts+1, retry_at=''`, reason, code)
}

// Retry puts a failed or kept job back in the queue: a fresh start (no tries counted, no wait).
func (s *Store) Retry(id int64) error {
	return s.transition(id, Queued, `, progress=0, error='', error_code='', worker='', attempts=0, retry_at=''`)
}

// Release puts a running job back in the queue (the worker was told to stop).
func (s *Store) Release(id int64) error {
	return s.transition(id, Queued, `, progress=0, worker=''`)
}

// RequeueRunning puts jobs left "running" by a crashed process back in the queue.
// Call once at startup, before workers start.
func (s *Store) RequeueRunning() (int64, error) {
	if err := s.finishRequestedStops(); err != nil {
		return 0, err
	}
	r, err := s.db.Exec(`UPDATE jobs SET status=?, progress=0, worker='', updated=? WHERE status=?`,
		Queued, s.stamp(), Running)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

// Step is one line of a job's step summary (S5): found, checked, converted, verified, kept, replaced.
type Step struct {
	Step  string `json:"step"`
	At    string `json:"at"`
	OK    bool   `json:"ok"`
	Words string `json:"words"`
	// NodeID and Port: the workflow node it was, and for a check ("branch") the port the file left by
	// (V6a; "" for the others).
	NodeID string `json:"nodeId,omitempty"`
	Port   string `json:"port,omitempty"`
}

// AddStep adds a step to job id's summary.
func (s *Store) AddStep(id int64, step string, ok bool, words string) error {
	_, err := s.db.Exec(`INSERT INTO job_steps(job_id,step,at,ok,words) VALUES(?,?,?,?,?)`, id, step, s.stamp(), ok, words)
	return err
}

// Steps is job id's step summary, in the order the steps happened.
func (s *Store) Steps(id int64) ([]Step, error) {
	rows, err := s.db.Query(`SELECT step,at,ok,words,node_id,port FROM job_steps WHERE job_id=? ORDER BY rowid`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Step{}
	for rows.Next() {
		var st Step
		if err := rows.Scan(&st.Step, &st.At, &st.OK, &st.Words, &st.NodeID, &st.Port); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// ForgetGoneJobSteps removes the steps of jobs that no longer exist (stopped, removed with their
// library, skipped by their worker), and returns the ids of the jobs that still exist, so their logs
// are kept and the others removed.
func (s *Store) ForgetGoneJobSteps() (map[int64]bool, error) {
	if _, err := s.db.Exec(`DELETE FROM job_steps WHERE job_id NOT IN (SELECT id FROM jobs)`); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id FROM jobs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

// LatestProbe is the newest stored probe (JSON) of path, whatever size and time it was taken at: the
// Files view's codec, resolution and length (S5). false if it was never probed.
func (s *Store) LatestProbe(path string) (string, bool) {
	var info string
	err := s.db.QueryRow(`SELECT info FROM probes WHERE path=? ORDER BY rowid DESC LIMIT 1`, path).Scan(&info)
	return info, err == nil
}

// jobsRemoved is called after jobs were deleted (stopped, skipped by their worker, removed with their
// library): their steps go, and OnJobsRemoved (the run logs, S5) is told.
func (s *Store) jobsRemoved(ids ...int64) {
	for _, id := range ids {
		s.db.Exec(`DELETE FROM job_steps WHERE job_id=?`, id)
	}
	if s.OnJobsRemoved != nil && len(ids) > 0 {
		s.OnJobsRemoved(ids)
	}
}

func jobIDs(q interface {
	Query(string, ...any) (*sql.Rows, error)
}, query string, args ...any) ([]int64, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// libraryOnSQL is true for a job (of table jobs) whose library is started, or that has none: a paused
// library's jobs stay waiting and are not taken (V5b).
const libraryOnSQL = `(jobs.library_id IS NULL OR jobs.library_id IN (SELECT id FROM libraries WHERE started=1))`

// libraryOn is libraryOnSQL for the jobs row named alias: Claim, the queue's order and its positions use
// the same condition (review-5d8b1f9).
func libraryOn(alias string) string {
	return `(` + alias + `.library_id IS NULL OR ` + alias + `.library_id IN (SELECT id FROM libraries WHERE started=1))`
}

// AddDecision records a check's answer on job id's timeline (V6a): the step "branch", the check's node,
// the port the file left by, and design's words ("Is it 4K? → Yes (resolution is 2160p)").
func (s *Store) AddDecision(id int64, nodeID, port, words string) error {
	_, err := s.db.Exec(`INSERT INTO job_steps(job_id,step,at,ok,words,node_id,port) VALUES(?,?,?,?,?,?,?)`, id, "branch", s.stamp(), true, words, nodeID, port)
	return err
}

// SetRoute records job id's path through its workflow (node ids).
func (s *Store) SetRoute(id int64, path []string) error {
	b, _ := json.Marshal(path)
	_, err := s.db.Exec(`UPDATE jobs SET route=? WHERE id=?`, string(b), id)
	return err
}

// Route is job id's path through its workflow (nil: a straight workflow, or none recorded).
func (s *Store) Route(id int64) ([]string, error) {
	var raw string
	if err := s.db.QueryRow(`SELECT route FROM jobs WHERE id=?`, id).Scan(&raw); err != nil || raw == "" {
		return nil, err
	}
	var path []string
	err := json.Unmarshal([]byte(raw), &path)
	return path, err
}
