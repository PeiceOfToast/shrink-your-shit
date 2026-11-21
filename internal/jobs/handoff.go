// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"errors"
	"log"

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

// handoffLookup reads a hand-over's target as it would run now (the scope rule, review-579): a target
// whose typed settings name a variable is compiled from its published document with the server's global
// values as they are now, the way TemplateFor does for the target's own jobs; a file.* or source.* value
// stays a reference (there is no file here). A target without such a reference is its template as published.
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
		if rev, ok, rerr := s.LatestRevision(id); rerr == nil && ok && workflow.HasRefs(rev.Doc) {
			sc := workflow.Scopes{LeaveFile: true}
			if g, gerr := s.GlobalVariables(); gerr == nil {
				sc.Global = workflow.VariableMap(g)
			}
			if t, issues := workflow.ToTemplateWith(rev.Doc, f.Template, sc); len(issues) == 0 {
				return t, f.Name, true
			}
			log.Printf("workflow %d: its published settings no longer resolve; a hand-over to it runs the stored template", id)
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
