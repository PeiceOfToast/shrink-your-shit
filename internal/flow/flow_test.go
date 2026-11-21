// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// Set a variable makes a name the cards after it read; Add a note writes the resolved line.
func TestBeforeRunsTheCardsInOrder(t *testing.T) {
	var logged []string
	st := &State{Vars: map[string]string{"title": "Harrow Lane (2019)", "resolution": "1080p"}}
	notes, err := Before([]rules.Card{
		{NodeID: "a", Type: workflow.TypeSetVariable, Config: map[string]any{"name": "profile", "value": "remux of {{title}}"}},
		{NodeID: "b", Type: workflow.TypeNote, Name: "Say so", Config: map[string]any{"text": "Converting {{title}} as {{profile}} at {{resolution}}"}},
	}, st, Env{Log: func(l string) { logged = append(logged, l) }})
	if err != nil {
		t.Fatal(err)
	}
	if st.Vars["profile"] != "remux of Harrow Lane (2019)" {
		t.Errorf("profile = %q", st.Vars["profile"])
	}
	if len(logged) != 1 || logged[0] != "Converting Harrow Lane (2019) as remux of Harrow Lane (2019) at 1080p" {
		t.Errorf("logged %q", logged)
	}
	if len(notes) != 2 || notes[0].Name != "Set a variable" || !strings.Contains(notes[0].Words, "{{profile}}") || notes[1].Name != "Say so" || len(notes[1].Empty) != 0 {
		t.Errorf("notes %+v", notes)
	}
	// a name with no value is reported, not invented
	notes, err = Before([]rules.Card{{NodeID: "c", Type: workflow.TypeNote, Config: map[string]any{"text": "{{nothing}} here"}}}, st, Env{})
	if err != nil || len(notes) != 1 || len(notes[0].Empty) != 1 || notes[0].Empty[0] != "nothing" {
		t.Errorf("%v %+v", err, notes)
	}
}

// A card the engine can't run, or a name a step may not set, stops the run with the card's name.
func TestBeforeStopsAtTheFirstFailure(t *testing.T) {
	st := &State{}
	notes, err := Before([]rules.Card{
		{NodeID: "a", Type: workflow.TypeSetVariable, Config: map[string]any{"name": "ok", "value": "1"}},
		{NodeID: "b", Type: workflow.TypeSetVariable, Name: "Bad one", Config: map[string]any{"name": "title", "value": "x"}},
		{NodeID: "c", Type: workflow.TypeNote, Config: map[string]any{"text": "never"}},
	}, st, Env{})
	if err == nil || !strings.HasPrefix(err.Error(), "Bad one didn't work: ") || len(notes) != 1 || st.Vars["ok"] != "1" {
		t.Errorf("%v %+v %v", err, notes, st.Vars)
	}
	if _, err := Before([]rules.Card{{NodeID: "z", Type: "action.later"}}, st, Env{}); err == nil || !strings.Contains(err.Error(), "can't run") {
		t.Errorf("%v", err)
	}
}
