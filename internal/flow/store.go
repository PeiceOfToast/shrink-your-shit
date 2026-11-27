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
// revision the page holds and sends back as baseRev.
type DraftInfo struct {
	Flow Flow
	Rev  int
}

// ListItem is one row of GET /workflows before the check and the libraries are added. Flow is
// the draft when there is one, else the published revision: the flow the check runs on.
type ListItem struct {
	Workflow
	DraftAhead bool
	Flow       Flow
	draft      sql.NullString // the row's draft column, kept until the flows are read
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
	now := s.clock().UTC().UnixMilli()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO workflows(name, description, kind, made_with, current_rev, created_at, updated_at)
		 VALUES(?, ?, 'video', 'starter', 1, ?, ?)`,
		name, f.Description, now, now)
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
// own revision (1), which the editor holds and sends back as baseRev. madeWith is "editor" (the
// guided builder, "guided", is item 4).
func (s *Store) Create(ctx context.Context, f Flow, madeWith string) (id int64, rev int, err error) {
	text, err := json.Marshal(f)
	if err != nil {
		return 0, 0, fmt.Errorf("save the new workflow's flow: %w", err)
	}
	now := s.clock().UTC().UnixMilli()
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO workflows(name, description, kind, made_with, draft, draft_rev, created_at, updated_at)
			 VALUES(?, ?, 'video', ?, ?, 1, ?, ?)`,
			f.Name, f.Description, madeWith, string(text), now, now)
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
	var draft sql.NullString
	var rev int
	var current sql.NullInt64
	err := s.db.Read().QueryRowContext(ctx,
		`SELECT draft, draft_rev, current_rev FROM workflows WHERE id = ? AND deleted_at IS NULL`, id).
		Scan(&draft, &rev, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return DraftInfo{}, ErrNotFound
	}
	if err != nil {
		return DraftInfo{}, fmt.Errorf("read the draft of workflow %d: %w", id, err)
	}
	info := DraftInfo{Rev: rev}
	if draft.Valid {
		if err := json.Unmarshal([]byte(draft.String), &info.Flow); err != nil {
			return DraftInfo{}, fmt.Errorf("read the draft of workflow %d: %w", id, err)
		}
		return info, nil
	}
	if current.Valid && current.Int64 > 0 {
		r, err := s.Revision(ctx, id, int(current.Int64))
		if err != nil {
			return DraftInfo{}, err
		}
		info.Flow = r.Flow
		return info, nil
	}
	return DraftInfo{}, fmt.Errorf("workflow %d has neither a draft nor a published revision", id)
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
		if _, err := tx.ExecContext(ctx,
			`UPDATE workflows SET name = ?, description = ?, draft = ?, draft_rev = draft_rev + 1,
			 draft_base_rev = COALESCE(current_rev, 0), updated_at = ? WHERE id = ?`,
			f.Name, f.Description, string(text), now, id); err != nil {
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
			 draft_base_rev = ?, draft_rev = draft_rev + 1, updated_at = ? WHERE id = ?`,
			f.Name, f.Description, rev, rev, now, id); err != nil {
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
		`SELECT id, name, description, kind, made_with, current_rev, draft, created_at, updated_at
		 FROM workflows WHERE deleted_at IS NULL ORDER BY updated_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("read the workflows: %w", err)
	}
	defer rows.Close()
	var items []ListItem
	for rows.Next() {
		var it ListItem
		var current sql.NullInt64
		var draft sql.NullString
		var created, updated int64
		if err := rows.Scan(&it.ID, &it.Name, &it.Description, &it.Kind, &it.MadeWith, &current,
			&draft, &created, &updated); err != nil {
			return nil, fmt.Errorf("read a workflow: %w", err)
		}
		it.CurrentRev = int(current.Int64)
		it.CreatedAt = time.UnixMilli(created).UTC()
		it.UpdatedAt = time.UnixMilli(updated).UTC()
		it.DraftAhead = draft.Valid
		it.draft = draft
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the workflows: %w", err)
	}
	// The published flows are read after the row loop closes, so a reader is never held open.
	for i := range items {
		it := &items[i]
		if it.draft.Valid {
			if err := json.Unmarshal([]byte(it.draft.String), &it.Flow); err != nil {
				return nil, fmt.Errorf("read the draft of workflow %d: %w", it.ID, err)
			}
			continue
		}
		if it.CurrentRev == 0 {
			continue
		}
		r, err := s.Revision(ctx, it.ID, it.CurrentRev)
		if err != nil {
			return nil, err
		}
		it.Flow = r.Flow
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
	now := s.clock().UTC().UnixMilli()
	var newID int64
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO workflows(name, description, kind, made_with, draft, draft_rev, created_at, updated_at)
			 VALUES(?, ?, ?, ?, ?, 1, ?, ?)`,
			f.Name, f.Description, src.Kind, src.MadeWith, string(text), now, now)
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
			 draft_base_rev = ?, draft_rev = draft_rev + 1, updated_at = ? WHERE id = ?`,
			f.Name, f.Description, newRev, newRev, now, id); err != nil {
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
