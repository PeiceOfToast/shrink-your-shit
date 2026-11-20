// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package vars

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

func TestRefs(t *testing.T) {
	cases := []struct {
		t        string
		want     []string
		unclosed bool
	}{
		{"{{title}}.mkv", []string{"title"}, false},
		{"a {{x}} b {{y}}", []string{"x", "y"}, false},
		{"{{ spaced }}", []string{"spaced"}, false},
		{"plain", nil, false},
		{"", nil, false},
		{"{{title", nil, true},
		{"{{a}} and {{b", []string{"a"}, true},
	}
	for _, c := range cases {
		refs, unclosed := Refs(c.t)
		var names []string
		for _, r := range refs {
			names = append(names, r.Name)
			if got := c.t[r.Start:r.End]; !strings.HasPrefix(got, "{{") || !strings.HasSuffix(got, "}}") || strings.TrimSpace(got[2:len(got)-2]) != r.Name {
				t.Errorf("%q: ref %q takes %q", c.t, r.Name, got)
			}
		}
		if !reflect.DeepEqual(names, c.want) || unclosed != c.unclosed {
			t.Errorf("%q: names %v unclosed %v, want %v %v", c.t, names, unclosed, c.want, c.unclosed)
		}
	}
}

func TestResolve(t *testing.T) {
	vals := map[string]string{"title": "Movie", "resolution": "1080p"}
	cases := []struct {
		t     string
		want  string
		empty []string
	}{
		{"{{title}} ({{resolution}})", "Movie (1080p)", nil},
		{"{{title}}.mkv", "Movie.mkv", nil},
		{"{{titel}} {{title}}", " Movie", []string{"titel"}}, // an unknown name is nothing, and is named
		{"{{x}}{{y}}{{x}}", "", []string{"x", "y"}},          // once each, in order
		{"plain text", "plain text", nil},
		{"{{title", "{{title", nil}, // unclosed: not a place, the text stays as typed (publish refuses it)
	}
	for _, c := range cases {
		got, empty := Resolve(c.t, vals)
		if got != c.want || !reflect.DeepEqual(empty, c.empty) {
			t.Errorf("%q: %q %v, want %q %v", c.t, got, empty, c.want, c.empty)
		}
	}
}

func TestCleanValueAndPiece(t *testing.T) {
	if got := CleanValue(`a<b>c:d"e/f\g|h?i*j` + "\x01k"); got != "abcdefghijk" {
		t.Errorf("CleanValue: %q", got)
	}
	if got := CleanValue("Movie (2019) - cut"); got != "Movie (2019) - cut" {
		t.Errorf("CleanValue kept too little: %q", got)
	}
	if got := CleanPiece(" .Movie. "); got != "Movie" {
		t.Errorf("CleanPiece: %q", got)
	}
	if got := CleanPiece("..."); got != "" {
		t.Errorf("CleanPiece of dots: %q", got)
	}
}

func TestReserved(t *testing.T) {
	for _, n := range []string{"CON", "con", "PRN", "aux", "nul", "COM1", "com9", "LPT9"} {
		if !Reserved(n) {
			t.Errorf("%q is reserved", n)
		}
	}
	for _, n := range []string{"console", "com", "com10", "Movie", ""} {
		if Reserved(n) {
			t.Errorf("%q is not reserved", n)
		}
	}
}

func TestOutputName(t *testing.T) {
	vals := map[string]string{"title": "Movie", "library": "Films"}
	cases := []struct {
		tpl   string
		vals  map[string]string
		want  string
		empty []string
	}{
		{"", vals, "", nil},                       // today's name
		{"  ", vals, "", nil},                     // today's name
		{"{{title}}", vals, "Movie.mkv", nil},     // the extension is added
		{"{{title}}.MKV", vals, "Movie.MKV", nil}, // an extension it already has is kept as typed
		{"{{library}} - {{title}}.mkv", vals, "Films - Movie.mkv", nil},
		{"{{title}}.{{resolution}}", vals, "Movie.mkv", []string{"resolution"}}, // nothing leaves a stray dot
		{"{{title}}.2019", vals, "Movie.2019.mkv", nil},                         // the text the template typed stays
		{"{{title}}.mp4", vals, "Movie.mp4.mkv", nil},                           // and the real extension is put after it
		{"CON.{{title}}", vals, "", nil},                                        // a name Windows refuses stays refused
		{"{{titel}}", vals, "", []string{"titel"}},                              // nothing left: today's name
		{"{{titel}}.mkv", vals, "mkv.mkv", []string{"titel"}},                   // an unknown name is nothing, and is named
		{"{{title}}.mkv", map[string]string{"title": "a/b:c"}, "abc.mkv", nil},  // a value is one path piece
		{"{{title}}", map[string]string{"title": " / "}, "", []string{"title"}},
		{"{{title}}", map[string]string{"title": "."}, "", []string{"title"}},
		{"{{title}}", map[string]string{"title": ".."}, "", []string{"title"}},
		{".", vals, "", nil},  // a template of nothing but a dot is no name either
		{"..", vals, "", nil}, // nor "..", which would name "..mkv"
		{"{{title}}", map[string]string{"title": "  Movie.  "}, "Movie.mkv", nil},
		{"{{title}}", map[string]string{"title": "CON"}, "", nil}, // Windows refuses it: today's name
		{"sub/{{title}}", vals, "subMovie.mkv", nil},              // the literal text is one piece too
	}
	for _, c := range cases {
		got, empty := OutputName(c.tpl, ".mkv", c.vals)
		if got != c.want || !reflect.DeepEqual(empty, c.empty) {
			t.Errorf("%q: %q %v, want %q %v", c.tpl, got, empty, c.want, c.empty)
		}
	}
}

func TestFolder(t *testing.T) {
	vals := map[string]string{"library_root": filepath.Join("media", "films"), "title": "Movie"}
	plain := filepath.Join("srv", "converted")
	if got, empty := Folder(plain, vals); got != plain || empty != nil {
		t.Errorf("plain folder: %q %v", got, empty)
	}
	// a value is one path piece, so a path in one loses its separators (the folder field itself is the path)
	if got, _ := Folder("{{library_root}}/small", vals); got != "mediafilms/small" {
		t.Errorf("templated folder: %q", got)
	}
	if got, empty := Folder(" /srv/{{titel}} ", vals); got != "/srv/" || len(empty) != 1 || empty[0] != "titel" {
		t.Errorf("empty name: %q %v", got, empty)
	}
	if got, _ := Folder("{{library_root}}/a/b", map[string]string{"library_root": "/media/../x"}); got != "media..x/a/b" {
		t.Errorf("a value can't walk the path: %q", got)
	}
}

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
	// a name Windows refuses keeps today's name, checked before the first dot as OutputName checks it
	for _, refused := range []string{"CON", "NUL", "con.eng", "LPT1.movie"} {
		got, empty := SrtNames(final, "{{title}}", subs[:1], map[string]string{"title": refused})
		if want := rules.SrtName(final, "eng"); got[0] != want || empty != nil {
			t.Errorf("a refused name %q: %v %v, want today's %q", refused, got, empty, want)
		}
	}
}

func TestValues(t *testing.T) {
	info := &media.Info{SizeBytes: 2000, Ext: ".mkv", DurationSec: 6312.4, BitrateKbps: 8000, Streams: []media.Stream{
		{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080, BitrateKbps: 8000},
		{Index: 1, Type: "audio", Codec: "ac3", Language: "eng", Channels: 6},
		{Index: 2, Type: "audio", Codec: "aac", Language: "jpn", Channels: 2},
		{Index: 3, Type: "subtitle", Codec: "subrip", Language: "eng"},
	}}
	root, src := filepath.Join("media"), filepath.Join("media", "Movie (2019).mkv")
	out := filepath.Join("out", "Movie.mkv")
	v := Source{Path: src, Library: "Films", LibraryRoot: root,
		Info: info, Workflow: "Shrink videos", Revision: 7, Worker: "castle", TargetCodec: "hevc",
		OutputPath: out, NewSize: 1250,
		Variables: []Own{{Name: "keep_audio", Type: "boolean", Value: true}, {Name: "api_key", Type: "secret", Value: "s3cret"}}}
	got := v.Values()
	want := map[string]string{"file_name": "Movie (2019).mkv", "title": "Movie (2019)", "ext": "mkv",
		"folder": filepath.Dir(src), // the source's own separator, so Windows agrees
		"path":   src, "library": "Films", "library_root": root,
		"size_bytes": "2000", "container": "mkv", "video_codec": "h264", "width": "1920", "height": "1080",
		"resolution": "1080p", "hdr": "no", "duration": "1:45:12", "bitrate_kbps": "8000",
		"audio_languages": "eng, jpn", "audio_codecs": "ac3, aac", "audio_channels": "6, 2",
		"subtitle_languages": "eng", "workflow": "Shrink videos", "workflow_revision": "7", "worker": "castle",
		"target_codec": "hevc", "output_path": out, "new_size_bytes": "1250", "saved_percent": "37.5",
		"keep_audio": "true"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("values:\n got %v\nwant %v", got, want)
	}
	if _, ok := got["api_key"]; ok {
		t.Error("a secret's value is never a value")
	}
	// a name with no value is left out: nothing is made up
	if _, ok := got["encoder"]; ok {
		t.Error("encoder has no value here")
	}
}

func TestEndValues(t *testing.T) {
	out := filepath.Join("out", "Movie.mkv")
	got := EndValues(out, 2000, 1250)
	want := map[string]string{"output_path": out, "new_size_bytes": "1250", "saved_percent": "37.5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%v, want %v", got, want)
	}
	if got := EndValues("", 0, 0); len(got) != 0 {
		t.Errorf("nothing known: %v", got)
	}
	if got := EndValues("", 1000, 1250); got["saved_percent"] != "-25" {
		t.Errorf("a bigger file: %v", got) // negative, as Source.Values has it
	}
}

func TestValuesLanguageTags(t *testing.T) {
	// an untagged stream and an unknown one are neither shown; one language is shown once
	info := &media.Info{Streams: []media.Stream{
		{Index: 0, Type: "video", Codec: "h264", Width: 1280, Height: 720},
		{Index: 1, Type: "audio", Codec: "aac", Language: ""},
		{Index: 2, Type: "audio", Codec: "aac", Language: "und"},
	}}
	got := Source{Path: "Movie.mkv", Info: info}.Values()
	if _, ok := got["audio_languages"]; ok {
		t.Errorf("audio_languages should be nothing: %q", got["audio_languages"])
	}
	if got["resolution"] != "720p" {
		t.Errorf("resolution %q", got["resolution"])
	}
}
