// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import "strings"

// CanonLang is a language code in one form, so a person's languages and a track's tag compare: lower
// case ISO 639-2/B ("ger", "fre"). The 639-2/T forms files also use ("deu", "fra") and the common
// two-letter ISO 639-1 codes ("de", "en") are mapped to it. "" for no language ("", "und") and for
// anything that is not a language code.
func CanonLang(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	if c == "" || c == "und" {
		return ""
	}
	if b, ok := langT2B[c]; ok {
		return b
	}
	if b, ok := lang1to2[c]; ok {
		return b
	}
	if len(c) == 3 && strings.Trim(c, "abcdefghijklmnopqrstuvwxyz") == "" {
		return c
	}
	return ""
}

// langT2B: the ISO 639-2 languages whose terminology (T) code differs from the bibliographic (B) one.
var langT2B = map[string]string{
	"deu": "ger", "fra": "fre", "nld": "dut", "ces": "cze", "ell": "gre", "zho": "chi", "ron": "rum",
	"slk": "slo", "fas": "per", "hye": "arm", "isl": "ice", "mkd": "mac", "msa": "may", "mya": "bur",
	"sqi": "alb", "eus": "baq", "kat": "geo", "cym": "wel", "bod": "tib", "mri": "mao",
}

// lang1to2: common ISO 639-1 codes, to their 639-2/B code.
var lang1to2 = map[string]string{
	"en": "eng", "fr": "fre", "de": "ger", "es": "spa", "it": "ita", "nl": "dut", "pt": "por", "ru": "rus",
	"ja": "jpn", "zh": "chi", "ko": "kor", "sv": "swe", "no": "nor", "nb": "nob", "nn": "nno", "da": "dan",
	"fi": "fin", "pl": "pol", "cs": "cze", "sk": "slo", "hu": "hun", "tr": "tur", "el": "gre", "he": "heb",
	"ar": "ara", "hi": "hin", "th": "tha", "vi": "vie", "id": "ind", "ms": "may", "uk": "ukr", "ro": "rum",
	"bg": "bul", "hr": "hrv", "sr": "srp", "sl": "slv", "et": "est", "lv": "lav", "lt": "lit", "is": "ice",
	"ga": "gle", "ca": "cat", "eu": "baq", "gl": "glg", "fa": "per", "ur": "urd", "bn": "ben", "ta": "tam",
	"te": "tel", "tl": "tgl", "sw": "swa", "af": "afr", "la": "lat",
}
