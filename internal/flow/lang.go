// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import "strings"

// CanonLang is a language code as the language choices compare it (L-AUD-16): lowercase, in the
// ISO 639-2/B form the language table uses, "" for unset, "und" or a code nobody knows, and "zxx"
// ("no linguistic content") as itself. A two-letter code and the 639-2/T form both become the B
// form, so "de", "deu" and "ger" are one language.
func CanonLang(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	if c == "" || c == "und" {
		return ""
	}
	if c == "zxx" {
		return "zxx"
	}
	return langAliases[c]
}

// SameLanguage reports whether two codes name the same language; "" and "und" are the same thing
// (L-AUD-17).
func SameLanguage(a, b string) bool {
	return CanonLang(a) == CanonLang(b)
}

// langAliases maps every spelling the table's languages arrive in to their ISO 639-2/B code:
// the B code itself, the 639-2/T code and the two-letter code. A code that is not here has no
// canonical form and compares as unknown (CanonLang answers "").
var langAliases = map[string]string{
	"en": "eng", "eng": "eng",
	"de": "ger", "deu": "ger", "ger": "ger",
	"fr": "fre", "fra": "fre", "fre": "fre",
	"es": "spa", "spa": "spa",
	"it": "ita", "ita": "ita",
	"pt": "por", "por": "por",
	"nl": "dut", "nld": "dut", "dut": "dut",
	"sv": "swe", "swe": "swe",
	"no": "nor", "nb": "nor", "nn": "nor", "nor": "nor",
	"da": "dan", "dan": "dan",
	"fi": "fin", "fin": "fin",
	"is": "ice", "isl": "ice", "ice": "ice",
	"pl": "pol", "pol": "pol",
	"cs": "cze", "ces": "cze", "cze": "cze",
	"sk": "slo", "slk": "slo", "slo": "slo",
	"hu": "hun", "hun": "hun",
	"ro": "rum", "ron": "rum", "rum": "rum",
	"bg": "bul", "bul": "bul",
	"el": "gre", "ell": "gre", "gre": "gre",
	"tr": "tur", "tur": "tur",
	"ru": "rus", "rus": "rus",
	"uk": "ukr", "ukr": "ukr",
	"sr": "srp", "srp": "srp",
	"hr": "hrv", "hrv": "hrv",
	"bs": "bos", "bos": "bos",
	"sl": "slv", "slv": "slv",
	"mk": "mac", "mkd": "mac", "mac": "mac",
	"sq": "alb", "sqi": "alb", "alb": "alb",
	"et": "est", "est": "est",
	"lv": "lav", "lav": "lav",
	"lt": "lit", "lit": "lit",
	"ca": "cat", "cat": "cat",
	"eu": "baq", "eus": "baq", "baq": "baq",
	"gl": "glg", "glg": "glg",
	"cy": "wel", "cym": "wel", "wel": "wel",
	"ga": "gle", "gle": "gle",
	"he": "heb", "heb": "heb",
	"ar": "ara", "ara": "ara",
	"fa": "per", "fas": "per", "per": "per",
	"hi": "hin", "hin": "hin",
	"ur": "urd", "urd": "urd",
	"bn": "ben", "ben": "ben",
	"ta": "tam", "tam": "tam",
	"te": "tel", "tel": "tel",
	"th": "tha", "tha": "tha",
	"vi": "vie", "vie": "vie",
	"id": "ind", "ind": "ind",
	"ms": "may", "msa": "may", "may": "may",
	"tl": "fil", "fil": "fil",
	"zh": "chi", "zho": "chi", "chi": "chi",
	"ja": "jpn", "jpn": "jpn",
	"ko": "kor", "kor": "kor",
	"af": "afr", "afr": "afr",
	"sw": "swa", "swa": "swa",
}
