// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import "strings"

// Tag limits (FB2c): a tag is a short word or phrase; a record carries a handful. The store and the cards use
// these same numbers and the same cleaning, so a tag reads the same wherever it was set.
const (
	MaxTagLen = 40
	MaxTags   = 20
)

// CleanTags reads tags as typed (comma-separated; a comma is a separator and never part of a tag): trimmed,
// inner spaces kept, cut to MaxTagLen, the same tag in another case taken once, at most MaxTags. Order is as
// typed.
func CleanTags(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		t = strings.Join(strings.Fields(t), " ")
		if t == "" {
			continue
		}
		if r := []rune(t); len(r) > MaxTagLen {
			t = string(r[:MaxTagLen])
		}
		if HasTag(out, t) {
			continue
		}
		out = append(out, t)
		if len(out) == MaxTags {
			break
		}
	}
	return out
}

// HasTag says whether tags carries t, whatever the case.
func HasTag(tags []string, t string) bool {
	for _, x := range tags {
		if strings.EqualFold(x, t) {
			return true
		}
	}
	return false
}

// ApplyTags is what "Tag the file" does to a record's tags: removes first, then adds, each cleaned (commas
// split), the result capped at MaxTags (a tag past the cap is left off, in order). Never nil.
func ApplyTags(have []string, add, remove []string) []string {
	out := []string{}
	removed := CleanTags(strings.Join(remove, ","))
	for _, t := range CleanTags(strings.Join(have, ",")) {
		if !HasTag(removed, t) {
			out = append(out, t)
		}
	}
	for _, t := range CleanTags(strings.Join(add, ",")) {
		if !HasTag(out, t) && len(out) < MaxTags {
			out = append(out, t)
		}
	}
	return out
}
