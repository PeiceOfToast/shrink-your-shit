// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// A published workflow whose setting refers to a secret with no value stops the run and names the secret; a
// secret nothing refers to does not stop it.
func TestRunStopsOnAReferencedSecretWithNoValue(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d := workflow.Preset()
	d.Nodes[0].Config = map[string]any{"note": "{{api_token}}"}
	d.Variables = []workflow.Variable{{Name: "api_token", Type: "secret"}, {Name: "unused", Type: "secret"}}
	f, err := s.AddWorkflow("Unset", d)
	if err != nil {
		t.Fatal(err)
	}
	draft, _, err := s.Draft(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(f.ID, draft.Etag, rules.DefaultSelectors(3).Template()); err != nil {
		t.Fatal(err)
	}
	lib, err := s.AddLibrary("Unset library", t.TempDir(), rules.DefaultSelectors(3).Template(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetLibraryFlow(lib.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.RunInfoFor(Job{LibraryID: lib.ID, Library: lib.Path})
	var unset *SecretUnsetError
	if !errors.As(err, &unset) || !slices.Equal(unset.Names, []string{"api_token"}) {
		t.Fatalf("run info error %v, want a SecretUnsetError naming api_token", err)
	}
}
