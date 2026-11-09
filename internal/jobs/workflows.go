// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

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
		s.Notes = nil
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
	for _, id := range ids {
		if _, err := s.EnsureRevision(id); err != nil {
			return err
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
	if _, err := s.insertPublished(tx, flowID, d.Doc, d.Layout, templateHash(t)); err != nil {
		return Revision{}, err
	}
	if _, err := tx.Exec(`DELETE FROM workflow_revisions WHERE workflow_id=? AND status='draft'`, flowID); err != nil {
		return Revision{}, err
	}
	if _, err := tx.Exec(`UPDATE flows SET status='enabled' WHERE id=? AND status='draft'`, flowID); err != nil {
		return Revision{}, err
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
