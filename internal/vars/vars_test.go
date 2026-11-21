// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package vars

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
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

// A filled-in folder with a segment that acts as ".." (review-592 follow-up): the same rule the publish
// check uses for a templated setting.
func TestHasDotDot(t *testing.T) {
	for in, want := range map[string]bool{"/data/lib/..": true, "/data/lib/../out": true, "/data/lib/x..": true, "/data/lib/.. ": true, "/data/lib/...": true, "/data/lib/.. .": true, `C:\out\..\x`: true, "/data/lib/ ..": true,
		"/data/lib": false, "/data/lib/..x": false, "/data/lib/.x": false, "/data/lib/a.b": false, "/data/lib/./x": false, "": false} {
		if got := HasDotDot(in); got != want {
			t.Errorf("HasDotDot(%q) = %v", in, got)
		}
	}
}
