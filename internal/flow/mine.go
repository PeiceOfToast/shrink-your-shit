// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"encoding/json"
)

// ResolveMyLanguages is the run-time lookup of "my languages" (page 07): an audio step with "keep every language" off
// and an empty list keeps the person's languages, the codes in general.languages as they are when the run starts. A
// flow with no "allLanguages" flag is an older one, where an empty list meant every language, and is left alone.
func ResolveMyLanguages(f Flow, mine []string) Flow {
	return rewriteAudio(f, func(m map[string]json.RawMessage) bool {
		var all bool
		raw, ok := m["allLanguages"]
		if !ok || json.Unmarshal(raw, &all) != nil || all {
			return false
		}
		// A list left out of the flow is an empty one.
		var list []string
		if raw, ok := m["languages"]; ok && json.Unmarshal(raw, &list) != nil {
			return false
		}
		if len(list) != 0 {
			return false
		}
		return setList(m, "languages", dedupe(mine))
	})
}

// rewriteAudio applies change to each audio step's settings and reports whether it changed one.
func rewriteAudio(f Flow, change func(map[string]json.RawMessage) bool) Flow {
	out := f
	out.Steps = make([]StepRef, len(f.Steps))
	copy(out.Steps, f.Steps)
	for i, st := range out.Steps {
		if st.Type != "audio.tracks" {
			continue
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(st.Settings, &m) != nil {
			continue
		}
		if !change(m) {
			continue
		}
		b, err := json.Marshal(m)
		if err != nil {
			continue
		}
		out.Steps[i].Settings = b
	}
	return out
}

func setList(m map[string]json.RawMessage, key string, list []string) bool {
	b, err := json.Marshal(list)
	if err != nil {
		return false
	}
	m[key] = b
	return true
}

func dedupe(list []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, c := range list {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}
