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

// Store is the workflow tables. It writes through the store's one writer and reads through its
// read pool, like the other modules.
type Store struct {
	db  *store.DB
	now func() time.Time
}

// Open binds the workflow tables to an open database.
func Open(db *store.DB) *Store { return &Store{db: db} }

// Workflow is the workflow row without its draft and answers: what the engine and setup need.
type Workflow struct {
	ID          int64
	Name        string
	Description string
	Kind        string
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
		`SELECT id, name, description, kind, current_rev, created_at, updated_at
		 FROM workflows WHERE id = ? AND deleted_at IS NULL`, id).
		Scan(&w.ID, &w.Name, &w.Description, &w.Kind, &current, &created, &updated)
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

func (s *Store) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}
