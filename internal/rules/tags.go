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
func CleanTags(s string) []string { return cleanTags(s, MaxTags) }

// cleanTags is CleanTags with its own cap (0: none).
func cleanTags(s string, cap int) []string {
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
		if cap > 0 && len(out) == cap {
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
// split). The cap applies to the adds only: what the record already carries is kept whole, so a remove-only
// write never drops a tag it wasn't asked to (review-542). Never nil. LeftOff says how many adds did not
// fit.
func ApplyTags(have []string, add, remove []string) []string {
	out, _ := ApplyTagsCounted(have, add, remove)
	return out
}

// ApplyTagsCounted is ApplyTags that also says how many of the adds were left off at the cap.
func ApplyTagsCounted(have []string, add, remove []string) (out []string, leftOff int) {
	out = []string{}
	removed := cleanTags(strings.Join(remove, ","), 0)
	for _, t := range cleanTags(strings.Join(have, ","), 0) {
		if !HasTag(removed, t) {
			out = append(out, t)
		}
	}
	for _, t := range cleanTags(strings.Join(add, ","), 0) {
		if HasTag(out, t) {
			continue
		}
		if len(out) >= MaxTags {
			leftOff++
			continue
		}
		out = append(out, t)
	}
	return out, leftOff
}
