// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"fmt"
	"strings"
	"testing"
)

// A comma is a separator and never part of a tag; tags are trimmed, unique whatever their case, short and few.
func TestCleanAndApplyTags(t *testing.T) {
	if got := CleanTags(" a ,b  c, A, , " + strings.Repeat("y", MaxTagLen+3)); fmt.Sprint(got) != "[a b c "+strings.Repeat("y", MaxTagLen)+"]" {
		t.Errorf("CleanTags: %q", got)
	}
	var many []string
	for i := 0; i < MaxTags+3; i++ {
		many = append(many, fmt.Sprintf("t%d", i))
	}
	if got := CleanTags(strings.Join(many, ",")); len(got) != MaxTags {
		t.Errorf("the cap: %d", len(got))
	}
	if got := ApplyTags([]string{"remux", "Needs review"}, []string{"converted", "REMUX"}, []string{"needs review"}); fmt.Sprint(got) != "[remux converted]" {
		t.Errorf("ApplyTags: %q", got)
	}
	if got := ApplyTags([]string{"a,b"}, []string{"c,d"}, []string{"b"}); fmt.Sprint(got) != "[a c d]" { // a stored "a,b" reads as two tags
		t.Errorf("commas: %q", got)
	}
	if got := ApplyTags(nil, nil, []string{"x"}); got == nil || len(got) != 0 {
		t.Errorf("ApplyTags of nothing: %#v", got)
	}
}
