// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
)

// jobDeps is what the job routes read: the queue, the lookups that name a job's library and workflow, and the
// event store for a job's step and warnings. Set with Jobs before the server takes requests.
type jobDeps struct {
	queue     *jobs.Queue
	libraries *library.Catalog
	flows     *flow.Store
	bus       *events.Bus
}

// Jobs gives the job routes what they read. Call it before Ready.
func (s *Server) Jobs(q *jobs.Queue, libs *library.Catalog, flows *flow.Store, bus *events.Bus) {
	s.jobs.Store(&jobDeps{queue: q, libraries: libs, flows: flows, bus: bus})
}

// The five list states the page shows, mapped to the queue's shown filter ("" is every job).
var shownFilter = map[string]string{
	"all": "", "waiting": "waiting", "running": "running", "done": "done", "failed": "failed", "skipped": "skipped",
}

// jobJSON is a job as types.ts shapes it (the list item and the head of the detail).
type jobJSON struct {
	ID       string       `json:"id"`
	State    string       `json:"state"`
	File     string       `json:"file"`
	Library  string       `json:"library"`
	Workflow string       `json:"workflow"`
	Worker   string       `json:"worker"`
	Step     *stepRefJSON `json:"step"`
	// Progress stays null until something emits live progress for a job; no emitter exists yet.
	Progress   *struct{}  `json:"progress"`
	QueuedAt   *string    `json:"queuedAt"`
	StartedAt  *string    `json:"startedAt"`
	FinishedAt *string    `json:"finishedAt"`
	TookMs     *int64     `json:"tookMs"`
	Result     resultJSON `json:"result"`
}

type stepRefJSON struct {
	Index int    `json:"index"`
	Count int    `json:"count"`
	Name  string `json:"name"`
}

type resultJSON struct {
	SizeBefore int64   `json:"sizeBefore"`
	SizeAfter  *int64  `json:"sizeAfter"`
	Outcome    *string `json:"outcome"`
	FailedStep *string `json:"failedStep"`
	Reason     *string `json:"reason"`
	Warnings   int     `json:"warnings"`
}

// jobsPageJSON is GET /jobs: the counts for the five states and one page of items.
type jobsPageJSON struct {
	Counts countsJSON `json:"counts"`
	Items  []jobJSON  `json:"items"`
	Next   *string    `json:"next"`
}

type countsJSON struct {
	All     int `json:"all"`
	Waiting int `json:"waiting"`
	Running int `json:"running"`
	Failed  int `json:"failed"`
	Done    int `json:"done"`
	Skipped int `json:"skipped"`
}

// jobState is the shown state for an inside state: retry_wait shows as waiting.
func jobState(s jobs.State) string {
	if s == jobs.RetryWait {
		return string(jobs.Waiting)
	}
	return string(s)
}

// view builds one job as the page shows it. The library and workflow names, the latest step, and the warning count
// come from their own packages; a name that can't be read is left empty rather than failing the page.
func (s *Server) view(ctx context.Context, d *jobDeps, j jobs.Job) jobJSON {
	v := jobJSON{
		ID: strconv.FormatInt(j.ID, 10), State: jobState(j.State), File: filepath.Base(j.Path),
		Worker: s.workerOf(ctx, d, j), Result: resultJSON{Warnings: 0},
	}
	if lib, err := d.libraries.GetLibrary(ctx, j.LibraryID); err == nil {
		v.Library = lib.Name
	}
	if wf, err := d.flows.Get(ctx, j.WorkflowID); err == nil {
		v.Workflow = wf.Name
	}
	v.QueuedAt = timeJSON(&j.QueuedAt)
	v.StartedAt = timeJSON(j.StartedAt)
	v.FinishedAt = timeJSON(j.FinishedAt)
	if j.StartedAt != nil && j.FinishedAt != nil {
		ms := j.FinishedAt.Sub(*j.StartedAt).Milliseconds()
		v.TookMs = &ms
	}
	v.Result.SizeBefore = derefInt(j.SizeBefore)
	v.Result.SizeAfter = j.SizeAfter
	v.Result.Outcome = nonEmpty(j.Outcome)
	v.Result.FailedStep = nonEmpty(j.FailedStep)
	v.Result.Reason = nonEmpty(j.Reason)
	v.Result.Warnings = s.countWarnings(ctx, d, j.ID)
	if j.State == jobs.Running {
		v.Step = s.latestStep(ctx, d, j)
	}
	return v
}

// latestStep is the step a running job reached last, with the workflow's step count. nil when no step has started.
func (s *Server) latestStep(ctx context.Context, d *jobDeps, j jobs.Job) *stepRefJSON {
	if d.bus == nil {
		return nil
	}
	evs, _, err := d.bus.Query(ctx, events.Filter{JobID: j.ID, Kind: events.KindStepStart}, events.Page{Limit: 1, Newest: true})
	if err != nil || len(evs) == 0 {
		return nil
	}
	ref := &stepRefJSON{Index: evs[0].StepIndex, Name: evs[0].StepName}
	// The revision this job ran, not the workflow's current one: a publish since it was queued
	// may have changed the step count.
	rev := j.WorkflowRev
	if wf, err := d.flows.Get(ctx, j.WorkflowID); err == nil {
		if rev == 0 {
			rev = wf.CurrentRev
		}
		if rev > 0 {
			if r, err := d.flows.Revision(ctx, j.WorkflowID, rev); err == nil {
				ref.Count = len(r.Flow.Steps)
			}
		}
	}
	return ref
}

// countWarnings counts the warning lines of a job. An unreadable event store counts as none.
func (s *Server) countWarnings(ctx context.Context, d *jobDeps, id int64) int {
	if d.bus == nil {
		return 0
	}
	evs, _, err := d.bus.Query(ctx, events.Filter{JobID: id, Kind: events.KindWarning}, events.Page{Limit: 500})
	if err != nil {
		return 0
	}
	return len(evs)
}

// listJobs is GET /jobs?state=&worker=&limit=&cursor=, newest first. cursor is the last id of the previous page.
func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	d := s.jobs.Load()
	if d == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	state := q.Get("state")
	if state == "" {
		state = "all"
	}
	shown, ok := shownFilter[state]
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request", "That isn't a job state the list shows.", "state", 0)
		return
	}
	limit, ok := parseInt(q.Get("limit"), 100, 1, 500)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request", "The limit must be a number from 1 to 500.", "limit", 0)
		return
	}
	var before int64
	if c := q.Get("cursor"); c != "" {
		n, err := strconv.ParseInt(c, 10, 64)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_request", "That cursor isn't one this list gave.", "cursor", 0)
			return
		}
		before = n
	}
	// stage 1 has one worker, "local": another worker's jobs are none.
	rows := []jobs.Job{}
	if wk := q.Get("worker"); wk == "" || wk == "local" {
		var err error
		rows, err = d.queue.List(ctx, jobs.ListFilter{Shown: shown, BeforeID: before, Limit: limit})
		if err != nil {
			internalError(w)
			return
		}
	}
	counts, err := d.queue.Counts(ctx)
	if err != nil {
		internalError(w)
		return
	}
	page := jobsPageJSON{Counts: countsOf(counts), Items: make([]jobJSON, 0, len(rows))}
	for _, j := range rows {
		page.Items = append(page.Items, s.view(ctx, d, j))
	}
	if len(rows) == limit {
		next := strconv.FormatInt(rows[len(rows)-1].ID, 10)
		page.Next = &next
	}
	writeJSON(w, http.StatusOK, page)
}

// getJob is GET /jobs/{id}: the job's detail page data.
func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	d := s.jobs.Load()
	if d == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	id, ok := parseJobID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "job_not_found", "That job doesn't exist.", "", 0)
		return
	}
	j, err := d.queue.Get(r.Context(), id)
	if errors.Is(err, jobs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "job_not_found", "That job doesn't exist.", "", 0)
		return
	}
	if err != nil {
		internalError(w)
		return
	}
	detail, err := s.detail(r.Context(), d, j)
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// neighbours is GET /jobs/{id}/neighbours?state=: the ids just above and below a job in the list shown by state.
func (s *Server) neighbours(w http.ResponseWriter, r *http.Request) {
	d := s.jobs.Load()
	if d == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	id, ok := parseJobID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "job_not_found", "That job doesn't exist.", "", 0)
		return
	}
	state := r.URL.Query().Get("state")
	if state == "" {
		state = "all"
	}
	shown, ok := shownFilter[state]
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request", "That isn't a job state the list shows.", "state", 0)
		return
	}
	prev, next, err := d.queue.Neighbours(r.Context(), id, shown)
	if errors.Is(err, jobs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "job_not_found", "That job isn't in this list.", "", 0)
		return
	}
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]*string{"prev": idOrNil(prev), "next": idOrNil(next)})
}

func parseJobID(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

// parseInt reads an optional integer in [lo, hi]. An empty value is def.
func parseInt(s string, def, lo, hi int) (int, bool) {
	if s == "" {
		return def, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo || n > hi {
		return 0, false
	}
	return n, true
}

func countsOf(c map[jobs.State]int) countsJSON {
	out := countsJSON{
		Waiting: c[jobs.Waiting] + c[jobs.RetryWait],
		Running: c[jobs.Running], Failed: c[jobs.Failed], Done: c[jobs.Done], Skipped: c[jobs.Skipped],
	}
	out.All = out.Waiting + out.Running + out.Failed + out.Done + out.Skipped
	return out
}

func timeJSON(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format("2006-01-02T15:04:05.000Z07:00")
	return &s
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func idOrNil(id int64) *string {
	if id == 0 {
		return nil
	}
	s := strconv.FormatInt(id, 10)
	return &s
}

func derefInt(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// jobDetailJSON is GET /jobs/{id} as types.ts shapes JobDetail. What the events and the job row do not yet give is
// empty and says so in the handoff: logs and media are [] (no decision or probe rows are read yet), and timeLeftMs
// is null (no live progress is emitted yet).
type jobDetailJSON struct {
	Job             jobJSON       `json:"job"`
	Path            string        `json:"path"`
	Workflow        string        `json:"workflow"`
	Worker          string        `json:"worker"`
	Attempt         int           `json:"attempt"`
	StartedAt       *string       `json:"startedAt"`
	TookMs          *int64        `json:"tookMs"`
	SizeBeforeBytes int64         `json:"sizeBeforeBytes"`
	SizeAfterBytes  *int64        `json:"sizeAfterBytes"`
	FinishedAt      *string       `json:"finishedAt"`
	WrittenAt       *string       `json:"writtenAt"`
	OutputPath      *string       `json:"outputPath"`
	TimeLeftMs      *int64        `json:"timeLeftMs"`
	Original        originalJSON  `json:"original"`
	Failure         *failureJSON  `json:"failure"`
	SkipReason      *string       `json:"skipReason"`
	Steps           []stepJSON    `json:"steps"`
	Logs            []any         `json:"logs"`
	Media           []any         `json:"media"`
	History         []historyJSON `json:"history"`
}

type originalJSON struct {
	Action string  `json:"action"`
	Until  *string `json:"until"`
}

type failureJSON struct {
	Step        string `json:"step"`
	Reason      string `json:"reason"`
	NextAction  string `json:"nextAction"`
	ActionLabel string `json:"actionLabel"`
}

// stepJSON is one step of the job (types.ts JobStep). Its outcome comes from the step's start and end events.
type stepJSON struct {
	Index      int    `json:"index"`
	Name       string `json:"name"`
	Outcome    string `json:"outcome"`
	DurationMs *int64 `json:"durationMs"`
}

// steps pairs each step's start and end events by index, in the order the steps started.
func (s *Server) steps(ctx context.Context, d *jobDeps, j jobs.Job) []stepJSON {
	if d.bus == nil {
		return []stepJSON{}
	}
	// Only the latest attempt's steps count: an earlier attempt that failed in a step must not stretch that step.
	starts, _ := s.kindEvents(ctx, d, j.ID, events.KindStepStart)
	ends, _ := s.kindEvents(ctx, d, j.ID, events.KindStepEnd)
	starts, ends = onAttempt(starts, j.Attempt), onAttempt(ends, j.Attempt)
	endBy := map[int]events.Event{}
	for _, e := range ends {
		endBy[e.StepIndex] = e
	}
	out := []stepJSON{}
	for _, st := range starts {
		step := stepJSON{Index: st.StepIndex, Name: st.StepName}
		if end, ok := endBy[st.StepIndex]; ok {
			ms := end.TS.Sub(st.TS).Milliseconds()
			step.DurationMs = &ms
			step.Outcome = "done"
		} else if j.State == jobs.Running {
			step.Outcome = "running"
		} else if j.FailedStep != "" && j.FailedStep == st.StepName {
			step.Outcome = "failed"
		} else {
			step.Outcome = "skipped"
		}
		if j.FailedStep != "" && j.FailedStep == st.StepName {
			step.Outcome = "failed"
		}
		out = append(out, step)
	}
	return out
}

// kindEvents reads every event of one kind for a job, oldest first.
func (s *Server) kindEvents(ctx context.Context, d *jobDeps, id int64, kind events.Kind) ([]events.Event, error) {
	var all []events.Event
	page := events.Page{Limit: 500}
	for {
		evs, next, err := d.bus.Query(ctx, events.Filter{JobID: id, Kind: kind}, page)
		if err != nil {
			return all, err
		}
		all = append(all, evs...)
		if len(evs) < page.Limit || len(all) >= maxJobLog {
			return all, nil
		}
		page.Cursor = next.Cursor
	}
}

// detail builds the job's detail page data.
func (s *Server) detail(ctx context.Context, d *jobDeps, j jobs.Job) (jobDetailJSON, error) {
	head := s.view(ctx, d, j)
	evs, err := s.jobEvents(ctx, d, j.ID)
	if err != nil {
		return jobDetailJSON{}, err
	}
	hist := []historyJSON{}
	for _, e := range evs {
		if historyKinds[e.Kind] && e.Level != events.LevelDebug {
			hist = append(hist, historyJSON{Time: timeJSON2(e.TS), Level: string(e.Level), Text: e.Message})
		}
	}
	done := j.State == jobs.Done
	out := jobDetailJSON{
		Job: head, Path: j.Path, Workflow: head.Workflow, Worker: head.Worker, Attempt: j.Attempt,
		StartedAt: head.StartedAt, TookMs: head.TookMs, SizeBeforeBytes: derefInt(j.SizeBefore),
		SizeAfterBytes: j.SizeAfter, FinishedAt: head.FinishedAt, OutputPath: nonEmpty(j.OutputPath),
		SkipReason: nonEmpty(j.SkipReason), Steps: s.steps(ctx, d, j), Logs: []any{}, Media: []any{}, History: hist,
	}
	// The new file is written when the job finishes with an output. The time is the finish time: no separate
	// write time is recorded yet.
	if j.OutputPath != "" && head.FinishedAt != nil {
		out.WrittenAt = head.FinishedAt
	}
	out.Original = originalJSON{Action: "not_yet"}
	if done {
		out.Original.Action = "not_touched"
	}
	if j.State == jobs.Failed {
		out.Failure = &failureJSON{Step: j.FailedStep, Reason: j.Reason, NextAction: j.NextAction, ActionLabel: j.NextAction}
	}
	return out, nil
}

// workerOf is the worker that ran a job's latest attempt. The job row clears its worker when it finishes, so the
// attempt is the record; the row's own worker is the fallback while nothing is recorded.
func (s *Server) workerOf(ctx context.Context, d *jobDeps, j jobs.Job) string {
	attempts, err := d.queue.Attempts(ctx, j.ID)
	if err != nil || len(attempts) == 0 {
		return j.WorkerID
	}
	if w := attempts[len(attempts)-1].WorkerID; w != "" {
		return w
	}
	return j.WorkerID
}

// onAttempt keeps the events of one attempt, in their order.
func onAttempt(evs []events.Event, attempt int) []events.Event {
	var out []events.Event
	for _, e := range evs {
		if e.Attempt == attempt {
			out = append(out, e)
		}
	}
	return out
}
