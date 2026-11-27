// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// State is a job's inner state (backend-plan §6). The UI shows five of them; see Shown.
type State string

const (
	Waiting   State = "waiting"
	RetryWait State = "retry_wait"
	Running   State = "running"
	Done      State = "done"
	Failed    State = "failed"
	Skipped   State = "skipped"
	Cancelled State = "cancelled"
)

// Shown is the state a user sees. retry_wait reads as waiting. Cancelled jobs are left out of lists.
func (s State) Shown() string {
	if s == RetryWait {
		return string(Waiting)
	}
	return string(s)
}

type verb string

const (
	verbClaim   verb = "claim"
	verbFinish  verb = "finish"
	verbRequeue verb = "requeue"
	verbCancel  verb = "cancel"
	verbRetry   verb = "retry"
)

// edge is one row of the transition table: verb moves a job from any of from to to. Enqueue is an insert, not a
// move, so it is not here; it writes waiting itself.
type edge struct {
	verb verb
	from []State
	to   State
}

// transitions is backend-plan §6 as data. Every change to jobs.state is one of these rows, and move refuses any
// other (verb, from) pair.
var transitions = []edge{
	{verbClaim, []State{Waiting, RetryWait}, Running},
	{verbFinish, []State{Running}, Done},
	{verbFinish, []State{Running}, Skipped},
	{verbFinish, []State{Running}, RetryWait},
	{verbFinish, []State{Running}, Failed},
	{verbRequeue, []State{Running}, Waiting},
	{verbCancel, []State{Waiting, RetryWait, Running}, Cancelled},
	{verbRetry, []State{Failed, Cancelled}, Waiting},
}

// openStates are the states the partial unique index holds to one row per file.
var openStates = []State{Waiting, RetryWait, Running}

// fromStates is what a verb may leave when it ends in to. A pair missing from the table is a programming error.
func fromStates(v verb, to State) []State {
	for _, e := range transitions {
		if e.verb == v && e.to == to {
			return e.from
		}
	}
	panic(fmt.Sprintf("jobs: no transition %s to %s", v, to))
}

// ErrNotFound means no job has that id.
var ErrNotFound = errors.New("jobs: no such job")

// ErrLibraryUnavailable means the library is missing or paused, so nothing is queued for it.
var ErrLibraryUnavailable = errors.New("jobs: the library is missing or paused")

// ErrFileBusy means a retry would give the file a second open job.
type ErrFileBusy struct{ JobID int64 }

func (e *ErrFileBusy) Error() string {
	return fmt.Sprintf("jobs: the file already has open job %d", e.JobID)
}

// ErrChangedSince means the job was not in a state the verb accepts. State is what it is in now. Nothing changed.
type ErrChangedSince struct {
	JobID int64
	Verb  string
	State State
}

func (e *ErrChangedSince) Error() string {
	return fmt.Sprintf("jobs: %s refused for job %d: it is %s now", e.Verb, e.JobID, e.State)
}

// change is the extra part of a guarded update: SET clauses (each starting ", ") and WHERE clauses (each starting
// " AND "), with their values in order.
type change struct {
	set      string
	args     []any
	cond     string
	condArgs []any
}

// move is the one place jobs.state changes. The UPDATE names the states the verb may leave, so when another
// actor moved the job first it changes no row, and the caller gets the state that is there now.
func move(tx *sql.Tx, id int64, v verb, to State, ch change) error {
	from := fromStates(v, to)
	q := "UPDATE jobs SET state = ?" + ch.set + " WHERE id = ? AND state IN (" + marks(len(from)) + ")" + ch.cond
	args := append([]any{string(to)}, ch.args...)
	args = append(args, id)
	args = append(args, stateArgs(from)...)
	args = append(args, ch.condArgs...)
	res, err := tx.Exec(q, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	var cur string
	err = tx.QueryRow(`SELECT state FROM jobs WHERE id = ?`, id).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return &ErrChangedSince{JobID: id, Verb: string(v), State: State(cur)}
}

// marks is n SQL placeholders, "?,?,?".
func marks(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func stateArgs(s []State) []any {
	out := make([]any, len(s))
	for i, x := range s {
		out[i] = string(x)
	}
	return out
}
