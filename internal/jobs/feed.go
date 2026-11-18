// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strconv"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Activity is one entry of the Dashboard's activity feed (contract ActivityEvent; V3d). It is made from
// the events table, so the feed, its live updates and their ids are the history itself: a job that
// ended (done, skipped, failed, kept) and a workflow published. Scans, settings and workers are not
// recorded as events yet, so they are not in it.
type Activity struct {
	ID   string            `json:"id"` // the event's seq
	At   string            `json:"at"`
	Type string            `json:"type"` // job.completed | job.skipped | job.failed | workflow.published
	Text string            `json:"text"`
	Refs map[string]string `json:"refs"`
}

const activityWhere = `((kind='job' AND to_status IN ('done','skipped','failed','kept')) OR kind='workflow.published')`

// ActivityOf is event e as a feed entry (false: not one).
func (s *Store) ActivityOf(e Event) (Activity, bool) {
	a := Activity{ID: strconv.FormatInt(e.Seq, 10), At: e.At, Refs: map[string]string{}}
	var d struct {
		Message, Path, Name string
		WorkflowID          string
		Version             int
	}
	json.Unmarshal([]byte(e.Data), &d)
	if e.Kind == "workflow.published" {
		a.Type, a.Refs["workflowId"] = "workflow.published", d.WorkflowID
		a.Text = "Published " + d.Name + ", version " + strconv.Itoa(d.Version) + "."
		return a, true
	}
	if e.Kind != "job" {
		return a, false
	}
	a.Refs["jobId"] = strconv.FormatInt(e.JobID, 10)
	var before, after int64
	if err := s.db.QueryRow(`SELECT path, size_before, size_after FROM jobs WHERE id=?`, e.JobID).Scan(&d.Path, &before, &after); err != nil && err != sql.ErrNoRows {
		return a, false
	}
	name := "A file"
	if d.Path != "" {
		name = filepath.Base(d.Path)
	}
	msg := ""
	if d.Message != "" {
		msg = " " + upperFirst(d.Message)
		if c := msg[len(msg)-1]; c != '.' && c != '!' && c != '?' {
			msg += "."
		}
	}
	switch Status(e.To) {
	case Done:
		a.Type, a.Text = "job.completed", "Converted "+name+"."
		if before > 0 && after > 0 {
			a.Text = "Converted " + name + ": " + rules.HumanBytes(before) + " to " + rules.HumanBytes(after) + "."
		}
	case "skipped":
		a.Type, a.Text = "job.skipped", "Left "+name+" as it is."+msg
	case Failed:
		a.Type, a.Text = "job.failed", name+" could not be converted."+msg
	case Kept:
		a.Type, a.Text = "job.failed", "Kept the original of "+name+": a check said no."+msg
	default:
		return a, false
	}
	return a, true
}

// ActivityFeed is the feed, newest first: limit entries before seq before (0: from the newest), and
// how many there are in all.
func (s *Store) ActivityFeed(before int64, limit int) ([]Activity, int, error) {
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE ` + activityWhere).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`SELECT seq, kind, COALESCE(job_id,0), at, from_status, COALESCE(to_status,''), reason, worker, COALESCE(data,'')
FROM events WHERE `+activityWhere+` AND (?=0 OR seq<?) ORDER BY seq DESC LIMIT ?`, before, before, limit)
	if err != nil {
		return nil, 0, err
	}
	var evs []Event
	for rows.Next() {
		var e Event
		var from sql.NullString
		if err := rows.Scan(&e.Seq, &e.Kind, &e.JobID, &e.At, &from, &e.To, &e.Reason, &e.Worker, &e.Data); err != nil {
			rows.Close()
			return nil, 0, err
		}
		evs = append(evs, e)
	}
	rows.Close()
	out := []Activity{}
	for _, e := range evs { // after the rows are closed: ActivityOf reads too
		if a, ok := s.ActivityOf(e); ok {
			out = append(out, a)
		}
	}
	return out, total, rows.Err()
}
