// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"encoding/json"
)

// ResolveMyLanguages is the run-time lookup of "my languages" (page 07): an audio step with "keep every language" off
// and an empty list keeps the person's languages, the codes in general.languages as they are when the run starts. An
// empty result is kept as every language, and the caller notes it (decisions.md, 11-28).
//
// A flow saved before the flag had none: an empty list then meant every language, and a list meant that list. Its
// flag is written here with that meaning, so the engine reads an explicit value for every audio step.
func ResolveMyLanguages(f Flow, mine []string) Flow {
	return rewriteAudio(f, func(m map[string]json.RawMessage) bool {
		var list []string
		if raw, ok := m["languages"]; ok && json.Unmarshal(raw, &list) != nil {
			return false
		}
		if raw, ok := m["allLanguages"]; !ok {
			// The older meaning: no list is every language, a list is that list.
			return setFlag(m, len(list) == 0)
		} else {
			var all bool
			if json.Unmarshal(raw, &all) != nil || all || len(list) != 0 {
				return false
			}
		}
		return setList(m, "languages", dedupe(mine))
	})
}

// MineIsEmpty reports whether a flow keeps "my languages" with nothing set, so the run keeps every language and notes
// it. It reads the same settings ResolveMyLanguages does.
func MineIsEmpty(f Flow, mine []string) bool {
	return len(mine) == 0 && HasMyLanguagesStep(f)
}

// HasMyLanguagesStep reports whether an audio step keeps "my languages" (the flag off and no list of its own).
func HasMyLanguagesStep(f Flow) bool {
	for _, st := range f.Steps {
		if st.Type != "audio.tracks" {
			continue
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(st.Settings, &m) != nil {
			continue
		}
		var all bool
		var list []string
		if raw, ok := m["allLanguages"]; ok && json.Unmarshal(raw, &all) == nil && !all {
			if raw, ok := m["languages"]; !ok || json.Unmarshal(raw, &list) != nil || len(list) == 0 {
				return true
			}
		}
	}
	return false
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

func setFlag(m map[string]json.RawMessage, all bool) bool {
	b, err := json.Marshal(all)
	if err != nil {
		return false
	}
	m["allLanguages"] = b
	return true
}
