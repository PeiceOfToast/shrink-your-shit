// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"errors"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// "Hand to another workflow" (FB4b): a workflow as it runs is its stored template with every path that
// hands the file on replaced by the other workflow's paths (rules.WithHandoffs). GetFlow stays the
// template as published (revisions and their hashes are made from it); GetRunFlow and TemplateFor are
// what the scanner and the worker read.

// GetRunFlow is flow id as it runs: its template with its hand-overs filled in from the workflows they
// lead to, as those are published now.
func (s *Store) GetRunFlow(id int64) (Flow, error) {
	f, err := s.GetFlow(id)
	if err != nil {
		return f, err
	}
	f.Template = s.WithHandoffs(f.Template)
	return f, nil
}

// WithHandoffs is t with its hand-overs filled in (rules.WithHandoffs) from this store's published
// workflows. A target never published (its status is draft), or gone, ends that path as failed.
func (s *Store) WithHandoffs(t rules.Template) rules.Template {
	return rules.WithHandoffs(t, s.handoffLookup())
}

func (s *Store) handoffLookup() rules.HandoffLookup {
	return func(id int64) (rules.Template, string, bool) {
		var status string
		if err := s.db.QueryRow(`SELECT status FROM flows WHERE id=?`, id).Scan(&status); err != nil || status == "draft" {
			return rules.Template{}, "", false
		}
		f, err := s.GetFlow(id)
		if err != nil {
			return rules.Template{}, "", false
		}
		return f.Template, f.Name, true
	}
}

// OtherWorkflow is what validation asks about a workflow a step may hand files to (workflow.Policy):
// its name, whether it was published, and the workflows its published paths hand files to. ok false:
// there is no such workflow.
func (s *Store) OtherWorkflow(id int64) (workflow.OtherWorkflow, bool) {
	var name, status string
	err := s.db.QueryRow(`SELECT name, status FROM flows WHERE id=?`, id).Scan(&name, &status)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return workflow.OtherWorkflow{}, false
	}
	out := workflow.OtherWorkflow{Name: name, Published: status != "draft"}
	if f, err := s.GetFlow(id); err == nil && f.Template.Branches != nil {
		for _, b := range *f.Template.Branches {
			if b.Goto != 0 {
				out.HandsTo = append(out.HandsTo, b.Goto)
			}
		}
	}
	return out, true
}
