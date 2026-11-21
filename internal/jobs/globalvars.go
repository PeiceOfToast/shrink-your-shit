// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"

	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// The server's global variables (W2, workflow-architecture.md 2.3): values every workflow's typed
// settings can name after its own. They live in app_meta under one key, beside the settings.

// GlobalVariablesKey is where the globals are kept in app_meta (the contract's name for them).
const GlobalVariablesKey = "workflow_variables"

// GlobalVariables are the server's global variables, in the order saved (never nil).
func (s *Store) GlobalVariables() ([]workflow.Variable, error) {
	v, ok, err := s.Meta(GlobalVariablesKey)
	if err != nil || !ok {
		return []workflow.Variable{}, err
	}
	var out []workflow.Variable
	if err := json.Unmarshal([]byte(v), &out); err != nil {
		return nil, fmt.Errorf("the global variables can't be read: %w", err)
	}
	return out, nil
}

// GlobalAffects is what a change to the globals touches: the workflows whose newest published
// revision names a changed global in a typed setting, and how many libraries use those workflows.
// Workflows are the ids as JSON strings, the type /api/v2's other workflow endpoints use
// ("id": "12"; #561 review ask 4), so a page can compare them with a library's workflowId as it is.
type GlobalAffects struct {
	Workflows []string `json:"workflows"`
	Libraries int      `json:"libraries"`
}

// GlobalAffectsOf says which workflows and libraries saving next would change: a global whose kind or
// value differs from the one saved now, and every workflow whose published settings name one. A
// workflow that names a global being removed is affected too: its settings stop resolving.
func (s *Store) GlobalAffectsOf(next []workflow.Variable) (GlobalAffects, error) {
	cur, err := s.GlobalVariables()
	if err != nil {
		return GlobalAffects{}, err
	}
	changed := changedGlobals(cur, next)
	out := GlobalAffects{Workflows: []string{}}
	if len(changed) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(`SELECT r.workflow_id, r.doc FROM workflow_revisions r
WHERE r.status='published' AND r.number=(SELECT MAX(x.number) FROM workflow_revisions x WHERE x.workflow_id=r.workflow_id AND x.status='published')`)
	if err != nil {
		return GlobalAffects{}, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return GlobalAffects{}, err
		}
		var d workflow.Doc
		if json.Unmarshal([]byte(raw), &d) != nil {
			continue // a doc that can't be read names nothing
		}
		if slices.ContainsFunc(workflow.RefNames(d), func(n string) bool { return changed[n] }) {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return GlobalAffects{}, err
	}
	slices.Sort(ids)
	for _, id := range ids {
		out.Workflows = append(out.Workflows, strconv.FormatInt(id, 10))
	}
	for _, id := range ids {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM libraries WHERE flow_id=?`, id).Scan(&n); err != nil {
			return GlobalAffects{}, err
		}
		out.Libraries += n
	}
	return out, nil
}

// changedGlobals are the names whose kind or value differs between old and next (including a name
// only one of them has).
func changedGlobals(old, next []workflow.Variable) map[string]bool {
	before := workflow.VariableMap(old)
	after := workflow.VariableMap(next)
	out := map[string]bool{}
	for n, v := range after {
		o, had := before[n]
		if !had || o.Type != v.Type || !reflect.DeepEqual(o.Value, v.Value) {
			out[n] = true
		}
	}
	for n := range before {
		if _, ok := after[n]; !ok {
			out[n] = true
		}
	}
	return out
}

// SetGlobalVariables saves the globals and records the change like a publish: an event on the live
// stream saying what it touched. It forgets no library's files and publishes nothing: a workflow
// resolves the globals when a job starts, so the next job uses the new values.
func (s *Store) SetGlobalVariables(vs []workflow.Variable) (GlobalAffects, error) {
	affects, err := s.GlobalAffectsOf(vs)
	if err != nil {
		return GlobalAffects{}, err
	}
	b, err := json.Marshal(vs)
	if err != nil {
		return GlobalAffects{}, err
	}
	if err := s.SetMeta(GlobalVariablesKey, string(b)); err != nil {
		return GlobalAffects{}, err
	}
	if err := s.RecordEvent("workflow.variables.changed", map[string]any{"type": "workflow.variables.changed",
		"variables": len(vs), "workflows": affects.Workflows, "libraries": affects.Libraries}); err != nil {
		return GlobalAffects{}, err
	}
	return affects, nil
}
