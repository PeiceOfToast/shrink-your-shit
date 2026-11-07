// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import "strings"

// CanonLang is a language code in one form, so a person's languages and a track's tag compare: lower
// case ISO 639-2 bibliographic ("ger", "fre"). The terminology forms files also use ("deu", "fra") and
// every ISO 639-1 two-letter code ("de", "sq") map to it, so "fra" and "fre" are the same language.
// "" for no language ("", "und") and for anything that is not an ISO 639 code ("zzz"); the tables are
// the Library of Congress's ISO 639-2 list (iso639.go).
func CanonLang(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	switch {
	case c == "" || c == "und":
		return ""
	case len(c) == 2:
		return iso6391To2B[c]
	case iso6392B[c]:
		return c
	}
	return iso6392T2B[c]
}
