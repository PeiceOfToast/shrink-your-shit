// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import "testing"

// A secret's value is never sent back, and hasValue says one is stored; a draft saved back without it
// keeps the stored one, and a value that is sent replaces it.
func TestMaskedAndKeptSecrets(t *testing.T) {
	stored := Doc{Variables: []Variable{
		{Name: "api_key", Type: "secret", Value: "s3cret"},
		{Name: "quality", Type: "string", Value: "high"},
	}}
	m := Masked(stored)
	if m.Variables[0].Value != nil || !m.Variables[0].HasValue {
		t.Errorf("masked secret: %+v, want no value and hasValue", m.Variables[0])
	}
	if m.Variables[1].Value != "high" {
		t.Errorf("masked plain variable: %+v, want its value", m.Variables[1])
	}
	if stored.Variables[0].Value != "s3cret" {
		t.Error("Masked changed the stored doc")
	}
	// what the page sends back: the secret with no value (as GET gave it), and a plain variable edited
	back := Doc{Variables: []Variable{
		{Name: "api_key", Type: "secret", HasValue: true},
		{Name: "quality", Type: "string", Value: "medium"},
	}}
	k := KeepSecretValues(back, stored)
	if k.Variables[0].Value != "s3cret" {
		t.Errorf("kept secret: %+v, want the stored value", k.Variables[0])
	}
	if k.Variables[1].Value != "medium" {
		t.Errorf("plain variable: %+v, want the sent value", k.Variables[1])
	}
	// a new value is the new value, and a secret with neither stays without
	newer := Doc{Variables: []Variable{{Name: "api_key", Type: "secret", Value: "fresh"}}}
	if got := KeepSecretValues(newer, stored); got.Variables[0].Value != "fresh" {
		t.Errorf("sent secret: %+v, want fresh", got.Variables[0])
	}
	none := Doc{Variables: []Variable{{Name: "other", Type: "secret"}}}
	if got := KeepSecretValues(none, stored); got.Variables[0].Value != nil {
		t.Errorf("a secret with no stored value: %+v", got.Variables[0])
	}
}
