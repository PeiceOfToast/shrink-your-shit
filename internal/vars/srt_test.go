// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// An external test package: rules imports vars (a question resolves its value), so a test that compares
// against rules.SrtName must live outside package vars or the test binary has an import cycle (review2-531 P1).
package vars_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	. "github.com/shrink-your-shit/shrink-your-shit/internal/vars"
)

func TestSrtNamesDefaultIsTodays(t *testing.T) {
	dir := t.TempDir()
	finals := []string{filepath.Join(dir, "Movie.mkv"), filepath.Join(dir, "Movie.hevc.mkv"), filepath.Join(dir, "A.B.mp4")}
	parts := []string{"", "eng", "eng.forced", "eng.2", "fre.forced"}
	var subs []Subtitle
	for _, p := range parts {
		subs = append(subs, Subtitle{Part: p})
	}
	for _, f := range finals {
		got, empty := SrtNames(f, "", subs, nil)
		if empty != nil {
			t.Errorf("%s: empty %v", f, empty)
		}
		for k, p := range parts {
			if want := rules.SrtName(f, p); got[k] != want {
				t.Errorf("%s part %q: %q, want %q", f, p, got[k], want)
			}
		}
	}
}

func TestSrtNamesTemplate(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "Movie.hevc.mkv")
	subs := []Subtitle{{Part: "eng", Lang: "eng"}, {Part: "eng.2", Lang: "eng"}, {Part: "fre.forced", Lang: "fre", Kind: ".forced"}}
	got, empty := SrtNames(final, "{{title}}.{{sub_lang}}{{sub_kind}}.srt", subs, map[string]string{"title": "Movie"})
	want := []string{filepath.Join(dir, "Movie.eng.srt"), filepath.Join(dir, "Movie.eng.2.srt"), filepath.Join(dir, "Movie.fre.forced.srt")}
	if empty != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("names %v %v, want %v", got, empty, want)
	}
	// a name that does not end in .srt gets it; a repeated one gets .2 before it
	got, _ = SrtNames(final, "{{title}}", subs, map[string]string{"title": "Subs"})
	want = []string{filepath.Join(dir, "Subs.srt"), filepath.Join(dir, "Subs.2.srt"), filepath.Join(dir, "Subs.3.srt")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("names %v, want %v", got, want)
	}
	// a name that is empty is reported, and what is left of the template still names the file
	got, empty = SrtNames(final, "{{title}}.{{titel}}", subs[:1], map[string]string{"title": "Movie"})
	if got[0] != filepath.Join(dir, "Movie.srt") || !reflect.DeepEqual(empty, []string{"titel"}) {
		t.Errorf("empty name: %v %v", got, empty)
	}
	// the final names are what the numbering compares: the second "eng" cannot take ".2" while the "eng.2"
	// subtitle has that name of its own, so it takes ".3" and every subtitle keeps a path of its own
	got, empty = SrtNames(final, "{{title}}.{{sub_lang}}", []Subtitle{{Lang: "eng"}, {Lang: "eng"}, {Lang: "eng.2"}}, map[string]string{"title": "Movie"})
	want = []string{filepath.Join(dir, "Movie.eng.srt"), filepath.Join(dir, "Movie.eng.3.srt"), filepath.Join(dir, "Movie.eng.2.srt")}
	if empty != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("a number in a language tag: %v %v, want %v", got, empty, want)
	}
	// a name Windows refuses keeps today's name, checked before the first dot as OutputName checks it
	for _, refused := range []string{"CON", "NUL", "con.eng", "LPT1.movie"} {
		got, empty := SrtNames(final, "{{title}}", subs[:1], map[string]string{"title": refused})
		if want := rules.SrtName(final, "eng"); got[0] != want || empty != nil {
			t.Errorf("a refused name %q: %v %v, want today's %q", refused, got, empty, want)
		}
	}
}
