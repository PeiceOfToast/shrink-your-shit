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

// MaxStepLanguages is how many languages one Audio or Subtitles step can list (G2).
const MaxStepLanguages = 20

// stepLangs are the languages the Audio step and the Subtitles step keep by (G2). Audio: its own list, else
// the person's (langs, from Settings > General). Subtitles: its own, else the Audio step's, else the person's.
func stepLangs(s Selectors, langs []string) (audio, subs []string) {
	audio, subs = langs, langs
	if len(s.AudioLangs) > 0 {
		audio = s.AudioLangs
	}
	switch {
	case len(s.SubLangs) > 0:
		subs = s.SubLangs
	case len(s.AudioLangs) > 0:
		subs = s.AudioLangs
	}
	return audio, subs
}
