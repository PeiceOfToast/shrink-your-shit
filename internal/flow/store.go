// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// ErrNotFound is what Get and Revision answer for a workflow that is not there, or was deleted.
var ErrNotFound = errors.New("flow: not found")

// ErrRevisionNotFound is what Revision and Restore answer for a revision the workflow never had.
var ErrRevisionNotFound = errors.New("flow: revision not found")

// ErrNothingToPublish is what Publish answers when neither a flow was handed to it nor the
// workflow has a saved draft.
var ErrNothingToPublish = errors.New("flow: nothing to publish")

// ChangedSinceError is the editor's 409 changed_since: the draft moved on since the page read
// it. Rev is the draft's revision now, so the page can reload instead of overwriting it.
type ChangedSinceError struct{ Rev int }

func (e *ChangedSinceError) Error() string { return "the workflow was saved somewhere else" }

// PublishBlockedError is the editor's 409 has_problems: the check said no. Problem is the first
// one, whose sentence the page shows after "Fix this first:".
type PublishBlockedError struct{ Problem Problem }

func (e *PublishBlockedError) Error() string { return e.Problem.Message }

// Store is the workflow tables. It writes through the store's one writer and reads through its
// read pool, like the other modules.
type Store struct {
	db *store.DB
	// Now is the clock; tests set it. Nil means time.Now.
	Now func() time.Time
}

// Open binds the workflow tables to an open database.
func Open(db *store.DB) *Store { return &Store{db: db} }

// Workflow is the workflow row without its draft and answers: what the engine and setup need.
type Workflow struct {
	ID          int64
	Name        string
	Description string
	Kind        string
	// MadeWith is "starter", "guided" or "editor". The list serves "starter" as guided: a
	// starter is the guided builder's own flow, saved without changes.
	MadeWith string
	// CurrentRev is the published revision, 0 when nothing is published yet.
	CurrentRev int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Revision is one published revision: the flow and when it was written.
type Revision struct {
	WorkflowID int64
	Rev        int
	Flow       Flow
	CreatedAt  time.Time
}

// RevisionInfo is a revision without its flow: what the history list needs.
type RevisionInfo struct {
	Rev       int
	CreatedAt time.Time
}

// DraftInfo is what GET /workflows/{id} serves: the flow the editor shows and the draft
// revision the page holds and sends back as baseRev. Answers is the guided builder's answers for
// this flow, nil when the guide cannot say the flow exactly (fitsGuided). A row whose answers
// column is empty (a stage 1 install wrote none) gets them from its flow on the read.
type DraftInfo struct {
	Flow    Flow
	Rev     int
	Answers Answers
}

// ListItem is one row of GET /workflows before the check and the libraries are added. Flow is
// the draft when there is one, else the published revision: the flow the check runs on. Answers is
// nil when the flow does not fit the guide; an empty answers column is filled from the flow.
type ListItem struct {
	Workflow
	DraftAhead bool
	Flow       Flow
	Answers    Answers
	draft      sql.NullString // the row's draft column, kept until the flows are read
	answers    sql.NullString // the row's answers column, read with the flow
}

// CreateFromStarter writes a workflow and its published revision 1 from a starter, in one
// transaction. Setup (R1-9) calls the Tx variant inside its own all-or-nothing call.
func (s *Store) CreateFromStarter(ctx context.Context, starterID, name string) (workflowID int64, rev int, err error) {
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		workflowID, rev, err = s.CreateFromStarterTx(ctx, tx, starterID, name)
		return err
	})
	return workflowID, rev, err
}

// CreateFromStarterTx is CreateFromStarter on the caller's transaction, so it commits or rolls
// back with the caller's other writes.
func (s *Store) CreateFromStarterTx(ctx context.Context, tx *sql.Tx, starterID, name string) (workflowID int64, rev int, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, 0, errors.New("A workflow needs a name.")
	}
	starter, ok := StarterByID(starterID)
	if !ok {
		return 0, 0, fmt.Errorf("there is no starter called %q", starterID)
	}
	f := starter.Flow
	f.Name = name
	text, err := json.Marshal(f)
	if err != nil {
		return 0, 0, fmt.Errorf("save the starter's flow: %w", err)
	}
	// A guided starter's answers are stored with it, so the setup-made workflow shows as one the
	// guide can edit (ui-shop 19:28Z). A starter the guide cannot say keeps NULL.
	var answers any
	if starter.Guided {
		if answers, err = answersColumn(f); err != nil {
			return 0, 0, err
		}
	}
	now := s.clock().UTC().UnixMilli()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO workflows(name, description, kind, made_with, current_rev, answers, created_at, updated_at)
		 VALUES(?, ?, 'video', 'starter', 1, ?, ?, ?)`,
		name, f.Description, answers, now, now)
	if err != nil {
		return 0, 0, fmt.Errorf("save the workflow: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, 0, fmt.Errorf("read the new workflow's id: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO workflow_revisions(workflow_id, rev, flow, created_at) VALUES(?, 1, ?, ?)`,
		id, string(text), now); err != nil {
		return 0, 0, fmt.Errorf("save revision 1: %w", err)
	}
	return id, 1, nil
}

// Get reads one workflow. A deleted workflow is not there.
func (s *Store) Get(ctx context.Context, id int64) (Workflow, error) {
	var w Workflow
	var current sql.NullInt64
	var created, updated int64
	err := s.db.Read().QueryRowContext(ctx,
		`SELECT id, name, description, kind, made_with, current_rev, created_at, updated_at
		 FROM workflows WHERE id = ? AND deleted_at IS NULL`, id).
		Scan(&w.ID, &w.Name, &w.Description, &w.Kind, &w.MadeWith, &current, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Workflow{}, ErrNotFound
	}
	if err != nil {
		return Workflow{}, fmt.Errorf("read workflow %d: %w", id, err)
	}
	w.CurrentRev = int(current.Int64)
	w.CreatedAt = time.UnixMilli(created).UTC()
	w.UpdatedAt = time.UnixMilli(updated).UTC()
	return w, nil
}

// Revision reads one saved revision. Revisions are never changed once written, so this is the
// flow a job ran, whatever the workflow looks like now.
func (s *Store) Revision(ctx context.Context, id int64, rev int) (Revision, error) {
	var text string
	var created int64
	err := s.db.Read().QueryRowContext(ctx,
		`SELECT flow, created_at FROM workflow_revisions WHERE workflow_id = ? AND rev = ?`, id, rev).
		Scan(&text, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, ErrNotFound
	}
	if err != nil {
		return Revision{}, fmt.Errorf("read revision %d of workflow %d: %w", rev, id, err)
	}
	var f Flow
	if err := json.Unmarshal([]byte(text), &f); err != nil {
		return Revision{}, fmt.Errorf("read the flow of revision %d of workflow %d: %w", rev, id, err)
	}
	return Revision{WorkflowID: id, Rev: rev, Flow: f, CreatedAt: time.UnixMilli(created).UTC()}, nil
}

// Create makes a workflow and its draft; nothing is published. The rev it returns is the draft's
// own revision (1), which the editor holds and sends back as baseRev. madeWith is "editor" for a
// flow the page built and "guided" for one made from the guide's answers; either way the answers
// column is filled when the guide can say the flow exactly.
func (s *Store) Create(ctx context.Context, f Flow, madeWith string) (id int64, rev int, err error) {
	text, err := json.Marshal(f)
	if err != nil {
		return 0, 0, fmt.Errorf("save the new workflow's flow: %w", err)
	}
	answers, err := answersColumn(f)
	if err != nil {
		return 0, 0, err
	}
	now := s.clock().UTC().UnixMilli()
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO workflows(name, description, kind, made_with, draft, draft_rev, answers, created_at, updated_at)
			 VALUES(?, ?, 'video', ?, ?, 1, ?, ?, ?)`,
			f.Name, f.Description, madeWith, string(text), answers, now, now)
		if err != nil {
			return fmt.Errorf("save the workflow: %w", err)
		}
		id, err = res.LastInsertId()
		if err != nil {
			return fmt.Errorf("read the new workflow's id: %w", err)
		}
		return nil
	})
	return id, 1, err
}

// Draft reads the flow the editor shows: the saved draft when there is one, else the published
// revision. Rev is the draft's revision, the value the page sends back as baseRev.
func (s *Store) Draft(ctx context.Context, id int64) (DraftInfo, error) {
	var draft, answers sql.NullString
	var rev int
	var current sql.NullInt64
	err := s.db.Read().QueryRowContext(ctx,
		`SELECT draft, draft_rev, current_rev, answers FROM workflows WHERE id = ? AND deleted_at IS NULL`, id).
		Scan(&draft, &rev, &current, &answers)
	if errors.Is(err, sql.ErrNoRows) {
		return DraftInfo{}, ErrNotFound
	}
	if err != nil {
		return DraftInfo{}, fmt.Errorf("read the draft of workflow %d: %w", id, err)
	}
	info := DraftInfo{Rev: rev}
	switch {
	case draft.Valid:
		if err := json.Unmarshal([]byte(draft.String), &info.Flow); err != nil {
			return DraftInfo{}, fmt.Errorf("read the draft of workflow %d: %w", id, err)
		}
	case current.Valid && current.Int64 > 0:
		r, err := s.Revision(ctx, id, int(current.Int64))
		if err != nil {
			return DraftInfo{}, err
		}
		info.Flow = r.Flow
	default:
		return DraftInfo{}, fmt.Errorf("workflow %d has neither a draft nor a published revision", id)
	}
	// The flow is known before the answers are, because an empty column is filled from it.
	if info.Answers, err = answersFor(id, info.Flow, answers); err != nil {
		return DraftInfo{}, err
	}
	return info, nil
}

// SaveDraft writes the draft, if the page's baseRev is the draft's own revision now. A page that
// has fallen behind gets a ChangedSinceError with the current rev instead of overwriting it.
// An empty name keeps the workflow's name, as the mock's save does.
func (s *Store) SaveDraft(ctx context.Context, id int64, f Flow, baseRev int) (rev int, err error) {
	now := s.clock().UTC().UnixMilli()
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		var have int
		var name string
		err := tx.QueryRowContext(ctx,
			`SELECT draft_rev, name FROM workflows WHERE id = ? AND deleted_at IS NULL`, id).
			Scan(&have, &name)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read workflow %d: %w", id, err)
		}
		if have != baseRev {
			return &ChangedSinceError{Rev: have}
		}
		if strings.TrimSpace(f.Name) == "" {
			f.Name = name
		}
		text, err := json.Marshal(f)
		if err != nil {
			return fmt.Errorf("save the draft of workflow %d: %w", id, err)
		}
		// The answers are read from the flow the page saved: kept when the guide can say it
		// exactly, cleared when the page edited it into something the guide cannot (item 4).
		answers, err := answersColumn(f)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE workflows SET name = ?, description = ?, draft = ?, answers = ?,
			 draft_rev = draft_rev + 1, draft_base_rev = COALESCE(current_rev, 0), updated_at = ?
			 WHERE id = ?`,
			f.Name, f.Description, string(text), answers, now, id); err != nil {
			return fmt.Errorf("save the draft of workflow %d: %w", id, err)
		}
		rev = have + 1
		return nil
	})
	return rev, err
}

// SaveGuidedDraft saves the flow the guided builder made from its answers as the draft. A guided
// save must not undo an editor save that came after the guide opened, and the page's baseRev is
// what says so: in the same transaction a stale revision is a ChangedSinceError. baseRev nil is
// the page that has not sent one yet, and the save is allowed (a stage 1 workflow has no answers
// to compare, and the person saving from the guide is choosing its flow). An empty name or
// description keeps the workflow's own, the way the mock's putAnswers does.
func (s *Store) SaveGuidedDraft(ctx context.Context, id int64, f Flow, baseRev *int) (rev int, err error) {
	now := s.clock().UTC().UnixMilli()
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		var have int
		var name, description string
		err := tx.QueryRowContext(ctx,
			`SELECT draft_rev, name, description FROM workflows WHERE id = ? AND deleted_at IS NULL`, id).
			Scan(&have, &name, &description)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read workflow %d: %w", id, err)
		}
		if baseRev != nil && *baseRev != have {
			return &ChangedSinceError{Rev: have}
		}
		if strings.TrimSpace(f.Name) == "" {
			f.Name = name
		}
		if strings.TrimSpace(f.Description) == "" {
			f.Description = description
		}
		text, err := json.Marshal(f)
		if err != nil {
			return fmt.Errorf("save the draft of workflow %d: %w", id, err)
		}
		answers, err := answersColumn(f)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE workflows SET name = ?, description = ?, draft = ?, answers = ?,
			 draft_rev = draft_rev + 1, draft_base_rev = COALESCE(current_rev, 0), updated_at = ?
			 WHERE id = ?`,
			f.Name, f.Description, string(text), answers, now, id); err != nil {
			return fmt.Errorf("save the draft of workflow %d: %w", id, err)
		}
		rev = have + 1
		return nil
	})
	return rev, err
}

// Publish copies the flow into a new revision and makes it current. given is the flow the editor
// is publishing; without one the saved draft is published. The check runs first: a problem is a
// PublishBlockedError with its first sentence, so a workflow that cannot run never becomes one a
// library's jobs pick up.
func (s *Store) Publish(ctx context.Context, id int64, given *Flow, vc ValidateContext) (rev int, err error) {
	w, err := s.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	var f Flow
	if given != nil {
		f = *given
	} else {
		f, err = s.draftFlow(ctx, id)
		if err != nil {
			return 0, err
		}
	}
	rep := Validate(f, vc)
	if !rep.OK() {
		return 0, &PublishBlockedError{Problem: rep.Problems[0]}
	}
	if strings.TrimSpace(f.Name) == "" {
		f.Name = w.Name
	}
	text, err := json.Marshal(f)
	if err != nil {
		return 0, fmt.Errorf("save revision of workflow %d: %w", id, err)
	}
	// Publishing is a save: what the guide can say about the published flow becomes the row's
	// answers, so fitsGuided follows the published flow until a draft changes it again.
	answers, err := answersColumn(f)
	if err != nil {
		return 0, err
	}
	now := s.clock().UTC().UnixMilli()
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		var exists int
		err := tx.QueryRowContext(ctx,
			`SELECT 1 FROM workflows WHERE id = ? AND deleted_at IS NULL`, id).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read workflow %d: %w", id, err)
		}
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(rev), 0) + 1 FROM workflow_revisions WHERE workflow_id = ?`, id).
			Scan(&rev); err != nil {
			return fmt.Errorf("number the new revision of workflow %d: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workflow_revisions(workflow_id, rev, flow, created_at) VALUES(?, ?, ?, ?)`,
			id, rev, string(text), now); err != nil {
			return fmt.Errorf("save revision %d of workflow %d: %w", rev, id, err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE workflows SET name = ?, description = ?, current_rev = ?, draft = NULL,
			 answers = ?, draft_base_rev = ?, draft_rev = draft_rev + 1, updated_at = ? WHERE id = ?`,
			f.Name, f.Description, rev, answers, rev, now, id); err != nil {
			return fmt.Errorf("mark revision %d of workflow %d current: %w", rev, id, err)
		}
		return nil
	})
	return rev, err
}

// draftFlow reads the saved draft only; ErrNothingToPublish when there is none.
func (s *Store) draftFlow(ctx context.Context, id int64) (Flow, error) {
	var draft sql.NullString
	err := s.db.Read().QueryRowContext(ctx,
		`SELECT draft FROM workflows WHERE id = ? AND deleted_at IS NULL`, id).Scan(&draft)
	if errors.Is(err, sql.ErrNoRows) {
		return Flow{}, ErrNotFound
	}
	if err != nil {
		return Flow{}, fmt.Errorf("read the draft of workflow %d: %w", id, err)
	}
	if !draft.Valid {
		return Flow{}, ErrNothingToPublish
	}
	var f Flow
	if err := json.Unmarshal([]byte(draft.String), &f); err != nil {
		return Flow{}, fmt.Errorf("read the draft of workflow %d: %w", id, err)
	}
	return f, nil
}

// List reads every workflow that is not deleted, ready for the check and the libraries to be
// added. A draft ahead of the published revision is the flow the check runs on.
func (s *Store) List(ctx context.Context) ([]ListItem, error) {
	rows, err := s.db.Read().QueryContext(ctx,
		`SELECT id, name, description, kind, made_with, current_rev, draft, answers, created_at, updated_at
		 FROM workflows WHERE deleted_at IS NULL ORDER BY updated_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("read the workflows: %w", err)
	}
	defer rows.Close()
	var items []ListItem
	for rows.Next() {
		var it ListItem
		var current sql.NullInt64
		var draft, answers sql.NullString
		var created, updated int64
		if err := rows.Scan(&it.ID, &it.Name, &it.Description, &it.Kind, &it.MadeWith, &current,
			&draft, &answers, &created, &updated); err != nil {
			return nil, fmt.Errorf("read a workflow: %w", err)
		}
		it.CurrentRev = int(current.Int64)
		it.CreatedAt = time.UnixMilli(created).UTC()
		it.UpdatedAt = time.UnixMilli(updated).UTC()
		it.DraftAhead = draft.Valid
		it.draft = draft
		it.answers = answers
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the workflows: %w", err)
	}
	// The published flows are read after the row loop closes, so a reader is never held open. The
	// answers follow the flow: an empty column is filled from it.
	for i := range items {
		it := &items[i]
		switch {
		case it.draft.Valid:
			if err := json.Unmarshal([]byte(it.draft.String), &it.Flow); err != nil {
				return nil, fmt.Errorf("read the draft of workflow %d: %w", it.ID, err)
			}
		case it.CurrentRev > 0:
			r, err := s.Revision(ctx, it.ID, it.CurrentRev)
			if err != nil {
				return nil, err
			}
			it.Flow = r.Flow
		}
		if it.Answers, err = answersFor(it.ID, it.Flow, it.answers); err != nil {
			return nil, err
		}
	}
	return items, nil
}

// Duplicate copies the published flow (or, for a workflow never published, its draft) into a new
// workflow named "<name> copy", saved as a draft. The copy has no published revision yet.
func (s *Store) Duplicate(ctx context.Context, id int64) (Workflow, error) {
	src, err := s.Get(ctx, id)
	if err != nil {
		return Workflow{}, err
	}
	var f Flow
	if src.CurrentRev > 0 {
		r, err := s.Revision(ctx, id, src.CurrentRev)
		if err != nil {
			return Workflow{}, err
		}
		f = r.Flow
	} else {
		f, err = s.draftFlow(ctx, id)
		if err != nil {
			return Workflow{}, err
		}
	}
	f.Name = src.Name + " copy"
	f.Description = src.Description
	text, err := json.Marshal(f)
	if err != nil {
		return Workflow{}, fmt.Errorf("save the copy of workflow %d: %w", id, err)
	}
	// The copy's answers come from the flow the copy takes, not from the source's answers column:
	// while a draft is pending that column holds the draft's answers, and the copy's flow is the
	// published revision (dev-review, #840).
	answers, err := answersColumn(f)
	if err != nil {
		return Workflow{}, err
	}
	now := s.clock().UTC().UnixMilli()
	var newID int64
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO workflows(name, description, kind, made_with, draft, draft_rev, answers, created_at, updated_at)
			 VALUES(?, ?, ?, ?, ?, 1, ?, ?, ?)`,
			f.Name, f.Description, src.Kind, src.MadeWith, string(text), answers, now, now)
		if err != nil {
			return fmt.Errorf("save the copy of workflow %d: %w", id, err)
		}
		newID, err = res.LastInsertId()
		if err != nil {
			return fmt.Errorf("read the copy's id: %w", err)
		}
		return nil
	})
	if err != nil {
		return Workflow{}, err
	}
	return s.Get(ctx, newID)
}

// Delete marks a workflow deleted. The revisions stay: a job's log still names the flow it ran,
// and the row can be read back from a backup. Libraries are refused by the caller: the flow store
// does not read the library tables.
func (s *Store) Delete(ctx context.Context, id int64) error {
	now := s.clock().UTC().UnixMilli()
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE workflows SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
			now, now, id)
		if err != nil {
			return fmt.Errorf("delete workflow %d: %w", id, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("delete workflow %d: %w", id, err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// Revisions lists a workflow's published revisions, newest first.
func (s *Store) Revisions(ctx context.Context, id int64) ([]RevisionInfo, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.Read().QueryContext(ctx,
		`SELECT rev, created_at FROM workflow_revisions WHERE workflow_id = ? ORDER BY rev DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("read the revisions of workflow %d: %w", id, err)
	}
	defer rows.Close()
	out := []RevisionInfo{}
	for rows.Next() {
		var info RevisionInfo
		var created int64
		if err := rows.Scan(&info.Rev, &created); err != nil {
			return nil, fmt.Errorf("read a revision of workflow %d: %w", id, err)
		}
		info.CreatedAt = time.UnixMilli(created).UTC()
		out = append(out, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the revisions of workflow %d: %w", id, err)
	}
	return out, nil
}

// Restore copies an old revision into a new, current one. The old revision is not rewritten: the
// new row takes its stored flow as it is.
func (s *Store) Restore(ctx context.Context, id int64, rev int) (newRev int, err error) {
	if _, err := s.Get(ctx, id); err != nil {
		return 0, err
	}
	var text string
	err = s.db.Read().QueryRowContext(ctx,
		`SELECT flow FROM workflow_revisions WHERE workflow_id = ? AND rev = ?`, id, rev).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrRevisionNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("read revision %d of workflow %d: %w", rev, id, err)
	}
	var f Flow
	if err := json.Unmarshal([]byte(text), &f); err != nil {
		return 0, fmt.Errorf("read the flow of revision %d of workflow %d: %w", rev, id, err)
	}
	answers, err := answersColumn(f)
	if err != nil {
		return 0, err
	}
	now := s.clock().UTC().UnixMilli()
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(rev), 0) + 1 FROM workflow_revisions WHERE workflow_id = ?`, id).
			Scan(&newRev); err != nil {
			return fmt.Errorf("number the restored revision of workflow %d: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workflow_revisions(workflow_id, rev, flow, created_at)
			 SELECT ?, ?, flow, ? FROM workflow_revisions WHERE workflow_id = ? AND rev = ?`,
			id, newRev, now, id, rev); err != nil {
			return fmt.Errorf("save the restored revision of workflow %d: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE workflows SET name = ?, description = ?, current_rev = ?, draft = NULL,
			 answers = ?, draft_base_rev = ?, draft_rev = draft_rev + 1, updated_at = ? WHERE id = ?`,
			f.Name, f.Description, newRev, answers, newRev, now, id); err != nil {
			return fmt.Errorf("make the restored revision of workflow %d current: %w", id, err)
		}
		return nil
	})
	return newRev, err
}

func (s *Store) clock() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// answersColumn is what the workflows.answers column gets for a flow: the guide's answers as JSON
// when ToAnswers can say the flow exactly, and nil (SQL NULL) when it cannot. NULL is also the
// server's fitsGuided: a workflow fits the guide exactly when this column holds its answers.
func answersColumn(f Flow) (any, error) {
	a, ok := ToAnswers(f)
	if !ok {
		return nil, nil
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("save the answers of workflow %q: %w", f.Name, err)
	}
	return string(raw), nil
}

// answersFor is the answers a read serves: the column when a save wrote them, else what the flow
// itself makes when the guide can say it exactly. Stage 1 installs wrote no answers, and an editor
// draft the guide can say needs none either, so the read fills them from the flow; the guide then
// opens on the workflow's own answers. Nothing is written back: a read stays a read. nil is a flow
// the guide cannot say (fitsGuided false).
func answersFor(id int64, f Flow, raw sql.NullString) (Answers, error) {
	if raw.Valid {
		return readAnswers(id, raw)
	}
	if a, ok := ToAnswers(f); ok {
		return a, nil
	}
	return nil, nil
}

// readAnswers reads a row's answers column; an empty one is no answers, not an empty object.
func readAnswers(id int64, raw sql.NullString) (Answers, error) {
	if !raw.Valid {
		return nil, nil
	}
	var a Answers
	if err := json.Unmarshal([]byte(raw.String), &a); err != nil {
		return nil, fmt.Errorf("read the answers of workflow %d: %w", id, err)
	}
	return a, nil
}
