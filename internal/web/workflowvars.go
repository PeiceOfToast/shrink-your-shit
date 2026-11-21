// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"net/http"
	"sync"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// v2GlobalVariables are the server's global variables (W2): the values every workflow's typed
// settings can name after its own.
//
//	GET /api/v2/workflow-variables
//	    {"items": [{name,type,value,options,description}], "total": n}
//	PUT /api/v2/workflow-variables {"variables": [...]}
//	    saves them; {"variables": [...], "total": n, "saved": true, "affects": {"workflows": [ids], "libraries": n}}
//	PUT with {"variables": [...], "preview": true}
//	    the same affects without saving: what this change would touch.
//
// A save records a workflow.variables.changed event on the live stream, like a publish, and forgets
// no library's files: a workflow reads the globals when a job starts, so its next job uses them.
func v2GlobalVariables(api *http.ServeMux, store *jobs.Store) {
	var mu sync.Mutex // one save at a time, so the count it records is the change it made
	api.HandleFunc("GET /api/v2/workflow-variables", func(w http.ResponseWriter, r *http.Request) {
		vs, err := store.GlobalVariables()
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, map[string]any{"items": vs, "total": len(vs)})
	})
	api.HandleFunc("PUT /api/v2/workflow-variables", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Variables []workflow.Variable `json:"variables"`
			Preview   bool                `json:"preview"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		if in.Variables == nil {
			in.Variables = []workflow.Variable{}
		}
		if issues := workflow.CheckGlobals(in.Variables); len(issues) > 0 {
			writeErrorWith(w, http.StatusBadRequest, issues[0].Code, issues[0].Message+" Nothing was changed.", map[string]any{"issues": issues})
			return
		}
		mu.Lock()
		defer mu.Unlock()
		var affects jobs.GlobalAffects
		var err error
		if in.Preview {
			affects, err = store.GlobalAffectsOf(in.Variables)
		} else {
			affects, err = store.SetGlobalVariables(in.Variables)
		}
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, map[string]any{"variables": in.Variables, "total": len(in.Variables), "saved": !in.Preview, "affects": affects})
	})
}
