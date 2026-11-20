// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// R8: the choice reaches the Plan as KeepIfBigger. It is left out of the Plan's JSON when false, so every
// existing Plan, and every existing job's fingerprint, stays exactly as it was.
func TestKeepIfBiggerReachesThePlan(t *testing.T) {
	var info media.Info
	for _, v := range probeSet(t) {
		info = v
		break
	}
	s := DefaultSelectors(3)
	replace := s.Template().Compile(info)
	if replace.KeepIfBigger {
		t.Fatal("a Plan from a replace workflow keeps the original")
	}
	if strings.Contains(planJSON(t, replace), "keepIfBigger") {
		t.Errorf("a replace Plan's JSON has keepIfBigger; existing fingerprints would change")
	}

	s.KeepIfBigger = true
	keep := s.Template().Compile(info)
	if !keep.KeepIfBigger {
		t.Fatal("a keep workflow's Plan does not keep the original")
	}
	if !strings.Contains(planJSON(t, keep), `"keepIfBigger":true`) {
		t.Errorf("a keep Plan's JSON lacks keepIfBigger: %s", planJSON(t, keep))
	}
	if keep.Output != replace.Output || !reflect.DeepEqual(keep.Video, replace.Video) {
		t.Errorf("the choice changed the conversion itself")
	}
}

func planJSON(t *testing.T, p Plan) string {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
