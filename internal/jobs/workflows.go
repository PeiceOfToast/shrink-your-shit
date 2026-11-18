// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// A flow is a workflow (V1, UI rebuild): its revisions are one draft at most and every published one.
// The flow's template stays what the engine runs: publishing writes it, and a template changed by the
// old pages becomes a new published revision when the workflow is next read (EnsureRevision).

// Revision is one version of a workflow's logic and layout.
type Revision struct {
	ID           int64           `json:"id"`
	WorkflowID   int64           `json:"workflowId"`
	Number       *int            `json:"number"`
	Status       string          `json:"status"` // draft | published
	Doc          workflow.Doc    `json:"workflow"`
	Layout       workflow.Layout `json:"layout"`
	LogicHash    string          `json:"logicHash"`
	TemplateHash string          `json:"-"`
	Etag         string          `json:"etag"`
	Created      string          `json:"createdAt"`
	Published    *string         `json:"publishedAt"`
}

// WorkflowMeta are a workflow's own fields beside its flow (name and template).
type WorkflowMeta struct {
	Description string   `json:"description"`
	Status      string   `json:"status"` // draft (never published) | enabled | disabled
	Favourite   bool     `json:"favourite"`
	Tags        []string `json:"tags"`
}

// ErrNotPublished: the workflow was never published, so it has nothing to run (its flow's template is
// a placeholder): no library may use it, and it can't be copied as a flow (review acace8e).
var ErrNotPublished = errors.New("workflow not published")

// published is nil when flow id has been published (or is a flow from before workflows).
func (s *Store) published(q interface {
	QueryRow(string, ...any) *sql.Row
}, id int64) error {
	var status string
	err := q.QueryRow(`SELECT status FROM flows WHERE id=?`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err == nil && status == "draft" {
		return ErrNotPublished
	}
	return err
}

// ErrStaleDraft: the draft changed since the etag the caller had.
var ErrStaleDraft = errors.New("stale draft")

const revCols = `id,workflow_id,number,status,doc,layout,logic_hash,template_hash,etag,created,published`

func scanRevision(r interface{ Scan(...any) error }) (Revision, error) {
	var v Revision
	var doc, layout string
	var num sql.NullInt64
	var pub sql.NullString
	if err := r.Scan(&v.ID, &v.WorkflowID, &num, &v.Status, &doc, &layout, &v.LogicHash, &v.TemplateHash, &v.Etag, &v.Created, &pub); err != nil {
		return v, err
	}
	if num.Valid {
		n := int(num.Int64)
		v.Number = &n
	}
	if pub.Valid {
		v.Published = &pub.String
	}
	if err := json.Unmarshal([]byte(doc), &v.Doc); err != nil {
		return v, err
	}
	json.Unmarshal([]byte(layout), &v.Layout)
	normalLayout(&v.Layout)
	return v, nil
}

func normalLayout(l *workflow.Layout) {
	if l.Nodes == nil {
		l.Nodes = map[string]workflow.NodeLayout{}
	}
	if l.Groups == nil {
		l.Groups = []workflow.Group{}
	}
	if l.Comments == nil {
		l.Comments = []workflow.Comment{}
	}
}

// templateHash identifies what a template runs; a migration's notes (shown until the next save) don't count.
func templateHash(t rules.Template) string {
	if t.Selectors != nil {
		s := *t.Selectors
		s.Notes, s.Notices = nil, nil
		t.Selectors = &s
	}
	b, _ := json.Marshal(t)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// LatestRevision is a workflow's newest published revision (ok false if it has none).
func (s *Store) LatestRevision(flowID int64) (Revision, bool, error) {
	v, err := scanRevision(s.db.QueryRow(`SELECT `+revCols+` FROM workflow_revisions WHERE workflow_id=? AND status='published' ORDER BY number DESC LIMIT 1`, flowID))
	if errors.Is(err, sql.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}

// EnsureRevision makes sure the workflow's newest published revision is what its flow's template
// runs: revision 1 for a flow from before workflows (migration 20), or a new one after the old pages
// saved the flow. A workflow never published (status draft) is left alone.
func (s *Store) EnsureRevision(flowID int64) (Revision, error) {
	f, err := s.GetFlow(flowID)
	if err != nil {
		return Revision{}, err
	}
	var status string
	if err := s.db.QueryRow(`SELECT status FROM flows WHERE id=?`, flowID).Scan(&status); err != nil {
		return Revision{}, err
	}
	latest, ok, err := s.LatestRevision(flowID)
	if err != nil || status == "draft" || ok && latest.TemplateHash == templateHash(f.Template) {
		return latest, err
	}
	var prev *workflow.Doc
	if ok {
		prev = &latest.Doc
	}
	doc := workflow.FromTemplate(f.Template, prev)
	layout := workflow.Layout{}
	if ok {
		layout = latest.Layout
	}
	if _, err := s.insertPublished(s.db, flowID, doc, layout, templateHash(f.Template)); err != nil {
		if v, ok2, err2 := s.LatestRevision(flowID); err2 == nil && ok2 && v.TemplateHash == templateHash(f.Template) {
			return v, nil // another caller made it at the same moment
		}
		return Revision{}, err
	}
	v, _, err := s.LatestRevision(flowID)
	return v, err
}

// EnsureRevisions runs EnsureRevision for every flow (when the store opens: migration 20's revision 1).
func (s *Store) EnsureRevisions() error {
	rows, err := s.db.Query(`SELECT id FROM flows`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids { // one flow that can't be read doesn't stop the app starting (review 109): it's logged
		if _, err := s.EnsureRevision(id); err != nil {
			log.Printf("workflow %d: its revision 1 could not be written (its flow stays as it is): %v", id, err)
		}
	}
	return nil
}

func (s *Store) insertPublished(q execer, flowID int64, doc workflow.Doc, layout workflow.Layout, tHash string) (int64, error) {
	now := s.stamp()
	r, err := q.Exec(`INSERT INTO workflow_revisions(workflow_id,number,status,doc,layout,logic_hash,template_hash,etag,created,published)
VALUES(?, (SELECT COALESCE(MAX(number),0)+1 FROM workflow_revisions WHERE workflow_id=?), 'published', ?,?,?,?,?,?,?)`,
		flowID, flowID, mustJSON(doc), mustJSON(layout), workflow.LogicHash(doc), tHash, workflow.NewID(), now, now)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// Draft is the workflow's draft, or a new one made from its newest published revision (not saved
// until SaveDraft); fresh is true when it isn't stored.
func (s *Store) Draft(flowID int64) (v Revision, fresh bool, err error) {
	v, err = scanRevision(s.db.QueryRow(`SELECT `+revCols+` FROM workflow_revisions WHERE workflow_id=? AND status='draft'`, flowID))
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return v, false, err
	}
	latest, ok, err := s.LatestRevision(flowID)
	if err != nil {
		return v, false, err
	}
	if !ok {
		return v, false, ErrNotFound
	}
	latest.ID, latest.Number, latest.Status, latest.Published = 0, nil, "draft", nil
	return latest, true, nil
}

// SaveDraft stores doc and layout as the workflow's draft. ifEtag must be the draft's etag (or the
// published revision's, when there is no draft yet); otherwise ErrStaleDraft.
func (s *Store) SaveDraft(flowID int64, doc workflow.Doc, layout workflow.Layout, ifEtag string) (Revision, error) {
	cur, fresh, err := s.Draft(flowID)
	if err != nil {
		return Revision{}, err
	}
	if ifEtag != cur.Etag {
		return Revision{}, ErrStaleDraft
	}
	normalLayout(&layout)
	etag, now := workflow.NewID(), s.stamp()
	if fresh {
		_, err = s.db.Exec(`INSERT INTO workflow_revisions(workflow_id,number,status,doc,layout,logic_hash,etag,created) VALUES(?,NULL,'draft',?,?,?,?,?)`,
			flowID, mustJSON(doc), mustJSON(layout), workflow.LogicHash(doc), etag, now)
	} else {
		var r sql.Result
		r, err = s.db.Exec(`UPDATE workflow_revisions SET doc=?, layout=?, logic_hash=?, etag=?, created=? WHERE workflow_id=? AND status='draft' AND etag=?`,
			mustJSON(doc), mustJSON(layout), workflow.LogicHash(doc), etag, now, flowID, ifEtag)
		if n, _ := r.RowsAffected(); err == nil && n == 0 {
			return Revision{}, ErrStaleDraft
		}
	}
	if err != nil {
		return Revision{}, err
	}
	v, _, err := s.Draft(flowID)
	return v, err
}

// Publish makes the draft (etag) the newest published revision and t, what it runs, the flow's
// template (every library using it looks at its files again); the draft is gone after. A workflow
// never published becomes enabled.
func (s *Store) Publish(flowID int64, etag string, t rules.Template) (Revision, error) {
	d, fresh, err := s.Draft(flowID)
	if err != nil {
		return Revision{}, err
	}
	if fresh || d.Etag != etag {
		return Revision{}, ErrStaleDraft
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Revision{}, err
	}
	defer tx.Rollback()
	// the etag again inside the transaction (review acace8e): a save between the read above and here
	// is never published under the old etag
	if d, err = scanRevision(tx.QueryRow(`SELECT `+revCols+` FROM workflow_revisions WHERE workflow_id=? AND status='draft' AND etag=?`, flowID, etag)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Revision{}, ErrStaleDraft
		}
		return Revision{}, err
	}
	rid, err := s.insertPublished(tx, flowID, d.Doc, d.Layout, templateHash(t))
	if err != nil {
		return Revision{}, err
	}
	// the live stream's workflow.published and the activity feed's entry (V3d), in the same transaction
	if _, err := tx.Exec(`INSERT INTO events(kind, at, data) VALUES ('workflow.published', ?, json_object('type','workflow.published',
  'workflowId',CAST(? AS TEXT),'revisionId',CAST(? AS TEXT),'version',(SELECT number FROM workflow_revisions WHERE id=?),'name',(SELECT name FROM flows WHERE id=?)))`,
		s.stamp(), flowID, rid, rid, flowID); err != nil {
		return Revision{}, err
	}
	if _, err := tx.Exec(`DELETE FROM workflow_revisions WHERE workflow_id=? AND status='draft'`, flowID); err != nil {
		return Revision{}, err
	}
	if _, err := tx.Exec(`UPDATE flows SET status='enabled' WHERE id=? AND status='draft'`, flowID); err != nil {
		return Revision{}, err
	}
	if t.Selectors != nil && t.Selectors.Notices != nil { // a publish clears the coded notices (user review 11-13 item A)
		sel := *t.Selectors
		sel.Notices = nil
		t.Selectors = &sel
	}
	if err := s.setTemplate(tx, flowID, t); err != nil {
		return Revision{}, err
	}
	if err := tx.Commit(); err != nil {
		return Revision{}, err
	}
	v, _, err := s.LatestRevision(flowID)
	return v, err
}

// Revisions are the workflow's published revisions, newest first.
func (s *Store) Revisions(flowID int64) ([]Revision, error) {
	rows, err := s.db.Query(`SELECT `+revCols+` FROM workflow_revisions WHERE workflow_id=? AND status='published' ORDER BY number DESC`, flowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Revision{}
	for rows.Next() {
		v, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// RevisionByID is one of the workflow's revisions.
func (s *Store) RevisionByID(flowID, revID int64) (Revision, error) {
	v, err := scanRevision(s.db.QueryRow(`SELECT `+revCols+` FROM workflow_revisions WHERE workflow_id=? AND id=?`, flowID, revID))
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

// AddWorkflow is a new workflow, never published (status draft), whose draft is doc. Its flow's
// template is the engine's default until it is published; the old pages don't list it meanwhile.
func (s *Store) AddWorkflow(name string, doc workflow.Doc) (Flow, error) {
	if err := s.nameFree(s.db, name, 0); err != nil {
		return Flow{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Flow{}, err
	}
	defer tx.Rollback()
	id, err := s.addFlow(tx, name, rules.Template{Kind: rules.KindSelectors, Selectors: ptrTo(rules.DefaultSelectors(3)), CompilerVersion: rules.CompilerVersion})
	if err != nil {
		return Flow{}, nameErr(err)
	}
	if _, err := tx.Exec(`UPDATE flows SET status='draft' WHERE id=?`, id); err != nil {
		return Flow{}, err
	}
	if _, err := tx.Exec(`INSERT INTO workflow_revisions(workflow_id,number,status,doc,layout,logic_hash,etag,created) VALUES(?,NULL,'draft',?,?,?,?,?)`,
		id, mustJSON(doc), mustJSON(workflow.Layout{}), workflow.LogicHash(doc), workflow.NewID(), s.stamp()); err != nil {
		return Flow{}, err
	}
	if err := tx.Commit(); err != nil {
		return Flow{}, err
	}
	return s.GetFlow(id)
}

// WorkflowMetaOf is a workflow's own fields.
func (s *Store) WorkflowMetaOf(flowID int64) (WorkflowMeta, error) {
	var m WorkflowMeta
	var fav int
	var tags string
	err := s.db.QueryRow(`SELECT description,status,favourite,tags FROM flows WHERE id=?`, flowID).Scan(&m.Description, &m.Status, &fav, &tags)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	m.Favourite = fav != 0
	json.Unmarshal([]byte(tags), &m.Tags)
	if m.Tags == nil {
		m.Tags = []string{}
	}
	return m, err
}

// SetWorkflowMeta changes the fields given (nil: unchanged). A never-published workflow's status can't be set.
func (s *Store) SetWorkflowMeta(flowID int64, description, status *string, favourite *bool, tags []string) error {
	m, err := s.WorkflowMetaOf(flowID)
	if err != nil {
		return err
	}
	if description != nil {
		m.Description = *description
	}
	if status != nil && m.Status != "draft" {
		m.Status = *status
	}
	if favourite != nil {
		m.Favourite = *favourite
	}
	if tags != nil {
		m.Tags = tags
	}
	fav := 0
	if m.Favourite {
		fav = 1
	}
	_, err = s.db.Exec(`UPDATE flows SET description=?, status=?, favourite=?, tags=?, changed=? WHERE id=?`, m.Description, m.Status, fav, mustJSON(m.Tags), s.stamp(), flowID)
	return err
}

func ptrTo[T any](v T) *T { return &v }

// WorkflowIDs are every workflow's id, never-published ones too, oldest first.
func (s *Store) WorkflowIDs() ([]int64, error) {
	rows, err := s.db.Query(`SELECT id FROM flows ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// WorkflowRuns are a workflow's jobs, over the libraries that use it: those waiting or running now,
// and those done or failed since since (skipped files keep no job before V2), with the newest end.
type WorkflowRuns struct {
	Active, Completed, Failed int
	LastAt                    string
}

func (s *Store) WorkflowRunsSince(flowID int64, since string) (WorkflowRuns, error) {
	var r WorkflowRuns
	var last sql.NullString
	err := s.db.QueryRow(`SELECT
  COALESCE(SUM(j.status IN ('queued','running')),0),
  COALESCE(SUM(j.status='done' AND j.updated>=?),0),
  COALESCE(SUM(j.status IN ('failed','kept') AND j.updated>=?),0),
  MAX(CASE WHEN j.status IN ('done','failed','kept') THEN j.updated END)
FROM jobs j JOIN libraries l ON l.id=j.library_id WHERE l.flow_id=?`, since, since, flowID).Scan(&r.Active, &r.Completed, &r.Failed, &last)
	r.LastAt = last.String
	return r, err
}

// Trash is what .syp-trash holds now: originals kept after a replace, freed when their days are up.
type Trash struct {
	Bytes        int64   `json:"bytes"`
	Files        int     `json:"files"`
	NextDeleteAt *string `json:"nextDeleteAt"`
	LastDeleteAt *string `json:"lastDeleteAt"`
}

// KeptUntil is when a kept original is due to be deleted: when it was kept plus days, the setting now
// (a lower setting applies at once; 0 days: due at once, as the swap deletes it). Purge, the kept
// days left and the trash dates all read it, so they can't disagree (review d3d44e2).
func KeptUntil(keptAt string, days int) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, keptAt)
	if err != nil {
		return time.Time{}, false
	}
	return t.Add(time.Duration(days) * 24 * time.Hour), true
}

// TrashNow sums the kept originals and when they go, with days the keep-originals setting now.
func (s *Store) TrashNow(days int) (Trash, error) {
	rows, err := s.Originals(OriginalKept)
	if err != nil {
		return Trash{}, err
	}
	var t Trash
	var first, last time.Time
	for _, o := range rows {
		t.Files++
		t.Bytes += o.Bytes
		due, ok := KeptUntil(o.KeptAt, days)
		if !ok {
			continue
		}
		if first.IsZero() || due.Before(first) {
			first = due
		}
		if due.After(last) {
			last = due
		}
	}
	if !first.IsZero() {
		f, l := first.UTC().Format(time.RFC3339), last.UTC().Format(time.RFC3339)
		t.NextDeleteAt, t.LastDeleteAt = &f, &l
	}
	return t, nil
}

// KnownTemplate reports whether t is a template this workflow has published (its flow's now, or an
// earlier revision's). A read-only step's template is the server's, not the page's: only one of these
// may be published (review acace8e, P2: a draft could otherwise send any values to the encoder).
func (s *Store) KnownTemplate(flowID int64, t rules.Template) (bool, error) {
	if f, err := s.GetFlow(flowID); err == nil && templateHash(f.Template) == templateHash(t) {
		return true, nil
	}
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM workflow_revisions WHERE workflow_id=? AND status='published' AND template_hash=?`, flowID, templateHash(t)).Scan(&n)
	return n > 0, err
}

// JobEvent is one entry of a job's history (migration 21): its v1 statuses, plus skipped and cancelled.
type JobEvent struct {
	Seq    int64   `json:"seq"`
	At     string  `json:"at"`
	From   *string `json:"from"`
	To     string  `json:"to"`
	Reason string  `json:"reason,omitempty"`
	// Message is the reason's sentence (migration 23), "" when there is no reason.
	Message string `json:"message,omitempty"`
	Worker  string `json:"worker,omitempty"`
	// Attempts are the tries a job had made when it ended by being deleted (skipped, stopped); 0 otherwise.
	Attempts int `json:"-"`
	// Path is the job's file, recorded when it ended by being deleted; "" otherwise (or before migration 32
	// for a job removed with its library).
	Path string `json:"-"`
}

// JobEvents is job id's history, oldest first; it stays after the job's row is gone.
func (s *Store) JobEvents(id int64) ([]JobEvent, error) {
	rows, err := s.db.Query(`SELECT seq, at, from_status, to_status, reason, COALESCE(json_extract(data,'$.message'),''), worker, COALESCE(json_extract(data,'$.attempts'),0), COALESCE(json_extract(data,'$.path'),'') FROM events WHERE kind='job' AND job_id=? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JobEvent{}
	for rows.Next() {
		var e JobEvent
		var from sql.NullString
		if err := rows.Scan(&e.Seq, &e.At, &from, &e.To, &e.Reason, &e.Message, &e.Worker, &e.Attempts, &e.Path); err != nil {
			return nil, err
		}
		if from.Valid {
			e.From = &from.String
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RevisionAt is the workflow's revision that was published at time at (what a job queued then ran):
// the newest published at or before it; ok is false if none was.
func (s *Store) RevisionAt(flowID int64, at string) (Revision, bool, error) {
	v, err := scanRevision(s.db.QueryRow(`SELECT `+revCols+` FROM workflow_revisions WHERE workflow_id=? AND status='published' AND published<=? ORDER BY number DESC LIMIT 1`, flowID, at))
	if !errors.Is(err, sql.ErrNoRows) {
		return v, err == nil, err
	}
	// none yet then: a flow made after the store opened gets revision 1 when first read, after its first
	// jobs; that first revision is what they ran (the template hasn't changed, or it would have a newer one)
	if _, err := s.EnsureRevision(flowID); err != nil {
		return v, false, err
	}
	v, err = scanRevision(s.db.QueryRow(`SELECT `+revCols+` FROM workflow_revisions WHERE workflow_id=? AND status='published' ORDER BY number LIMIT 1`, flowID))
	if errors.Is(err, sql.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}

// SetPriority sets a queued job's priority (higher is taken first by Claim).
// First and Last (design 03:22Z, SCREENS V6): a job the person put first or last keeps that place over
// any library priority. It is kept in the job's priority itself, outside the range a library's priority
// can take (-1000..1000): Claim and the queue's positions order by priority then age, one order, with no
// other column or index; a library's change leaves pinned jobs alone (SetLibraryPriority).
const (
	PinFirstPriority   = 1_000_000
	PinLastPriority    = -1_000_000
	MaxLibraryPriority = 1000
)

// PinOf is "first", "last" or "" for a job's priority.
func PinOf(priority int) string {
	switch {
	case priority >= PinFirstPriority:
		return "first"
	case priority <= PinLastPriority:
		return "last"
	}
	return ""
}

// SetPin puts waiting job id first, last, or back in its library's order ("").
func (s *Store) SetPin(id int64, pin string) error {
	var r sql.Result
	var err error
	switch pin {
	case "first":
		r, err = s.db.Exec(`UPDATE jobs SET priority=? WHERE id=? AND status=?`, PinFirstPriority, id, Queued)
	case "last":
		r, err = s.db.Exec(`UPDATE jobs SET priority=? WHERE id=? AND status=?`, PinLastPriority, id, Queued)
	case "":
		r, err = s.db.Exec(`UPDATE jobs SET priority=COALESCE((SELECT priority FROM libraries WHERE id=jobs.library_id),0) WHERE id=? AND status=?`, id, Queued)
	default:
		return fmt.Errorf("pin %q: first, last or none", pin)
	}
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotStoppable // not waiting: nothing to reorder
	}
	return nil
}

func (s *Store) SetPriority(id int64, p int) error {
	r, err := s.db.Exec(`UPDATE jobs SET priority=? WHERE id=? AND status=?`, p, id, Queued)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotStoppable // not waiting: nothing to reorder
	}
	return nil
}

// PruneJobEvents forgets history entries older than before (the run logs' 30 days, review 66806b3).
func (s *Store) PruneJobEvents(before time.Time) (int64, error) {
	r, err := s.db.Exec(`DELETE FROM events WHERE at < ?`, before.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

// RetryNow lets a job waiting to retry go at once (bulk retry of a retry_wait job, review 66806b3);
// ErrNotStoppable if it isn't waiting to retry.
func (s *Store) RetryNow(id int64) error {
	r, err := s.db.Exec(`UPDATE jobs SET retry_at='' WHERE id=? AND status=? AND retry_at<>''`, id, Queued)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotStoppable
	}
	return nil
}

// Event is one entry of the live stream's sequence (migration 22): a job's state change now; other
// kinds (workflow published, pauses, activity) carry their facts in Data.
type Event struct {
	Seq    int64
	Kind   string
	JobID  int64
	At     string
	From   *string
	To     string
	Reason string
	Worker string
	Data   string
}

// EventsAfter are the events after seq, oldest first, at most limit.
func (s *Store) EventsAfter(seq int64, limit int) ([]Event, error) {
	rows, err := s.db.Query(`SELECT seq, kind, COALESCE(job_id,0), at, from_status, COALESCE(to_status,''), reason, worker, COALESCE(data,'')
FROM events WHERE seq > ? ORDER BY seq LIMIT ?`, seq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var from sql.NullString
		if err := rows.Scan(&e.Seq, &e.Kind, &e.JobID, &e.At, &from, &e.To, &e.Reason, &e.Worker, &e.Data); err != nil {
			return nil, err
		}
		if from.Valid {
			e.From = &from.String
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EventBounds are the oldest and newest seq kept (0, 0 when there are none).
func (s *Store) EventBounds() (oldest, newest int64, err error) {
	err = s.db.QueryRow(`SELECT COALESCE(MIN(seq),0), COALESCE(MAX(seq),0) FROM events`).Scan(&oldest, &newest)
	return oldest, newest, err
}

// RecordEvent writes a non-job event (kind, its facts as data) to the live stream's sequence (V3c).
func (s *Store) RecordEvent(kind string, data map[string]any) error {
	b, _ := json.Marshal(data)
	_, err := s.db.Exec(`INSERT INTO events(kind, at, data) VALUES (?,?,?)`, kind, s.stamp(), string(b))
	return err
}

// JobEndsSince counts job history entries since at by the state they went to (done, failed, kept,
// skipped, cancelled, ...): the summary's last 24 hours (V3c).
func (s *Store) JobEndsSince(at string) (map[string]int, error) {
	rows, err := s.db.Query(`SELECT COALESCE(to_status,''), COUNT(*) FROM events WHERE kind='job' AND at >= ? GROUP BY to_status`, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// HourOfWork is one hour of the summary's throughput: jobs done, and what they saved (measured sizes).
type HourOfWork struct {
	HourStart  string `json:"hourStart"`
	Completed  int    `json:"completed"`
	SavedBytes int64  `json:"savedBytes"`
}

// Throughput are the hours since at with jobs done, oldest first (V3c).
func (s *Store) Throughput(at string) ([]HourOfWork, error) {
	rows, err := s.db.Query(`SELECT substr(e.at,1,13) || ':00:00Z', COUNT(*), COALESCE(SUM(MAX(j.size_before - COALESCE(j.size_after,0), 0)),0)
FROM events e LEFT JOIN jobs j ON j.id = e.job_id
WHERE e.kind='job' AND e.to_status='done' AND e.at >= ? GROUP BY substr(e.at,1,13) ORDER BY 1`, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HourOfWork{}
	for rows.Next() {
		var h HourOfWork
		if err := rows.Scan(&h.HourStart, &h.Completed, &h.SavedBytes); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// CorruptFlowForTest makes a flow's template unreadable, as TestOpenSurvivesABadFlow does (for other
// packages' tests).
func (s *Store) CorruptFlowForTest(id int64) error {
	_, err := s.db.Exec(`UPDATE flows SET template='not json' WHERE id=?`, id)
	return err
}
