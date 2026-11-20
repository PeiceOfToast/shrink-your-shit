// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"fmt"
	"regexp"
	"strings"
)

// The names {{name}} in a step's fields take: the built-ins the engine sets for every file (design-
// joblog-vars section 2), then a workflow's own variables. This file is the server's side of that
// list: the names, their groups and words, and the checks a workflow's own variables must pass.

// NamePattern is what a variable name may be: lowercase letters, numbers and _, starting with a
// letter, at most 40 characters — the editor's rule (VariablesCard), so a name the page takes is one
// the server takes too.
var NamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// Builtin is one name the engine sets itself.
type Builtin struct {
	Name        string
	Group       string // File, Library, Video, Audio, Subtitles, Run
	Description string
	// FieldOnly: the name exists in one field only (the .srt name), so it is not offered as a
	// variable of its own until that field takes variables.
	FieldOnly bool
}

// Builtins are every built-in name, in the design's groups and order.
var Builtins = []Builtin{
	{Name: "title", Group: "File", Description: "The file name without its extension"},
	{Name: "file_name", Group: "File", Description: "The file name with its extension"},
	{Name: "ext", Group: "File", Description: "The file's extension"},
	{Name: "folder", Group: "File", Description: "The folder the file is in"},
	{Name: "path", Group: "File", Description: "The full path of the file"},
	{Name: "size_bytes", Group: "File", Description: "The file's size in bytes"},
	{Name: "library", Group: "Library", Description: "The name of the library"},
	{Name: "library_root", Group: "Library", Description: "The library's folder"},
	{Name: "video_codec", Group: "Video", Description: "The video's codec"},
	{Name: "width", Group: "Video", Description: "The width in pixels"},
	{Name: "height", Group: "Video", Description: "The height in pixels"},
	{Name: "resolution", Group: "Video", Description: "The resolution, such as 1080p"},
	{Name: "hdr", Group: "Video", Description: "Whether the video is HDR"},
	{Name: "duration", Group: "Video", Description: "The running time"},
	{Name: "bitrate_kbps", Group: "Video", Description: "The video's bitrate in kbps"},
	{Name: "container", Group: "Video", Description: "The file type the file is in now"},
	{Name: "audio_languages", Group: "Audio", Description: "The audio languages"},
	{Name: "audio_codecs", Group: "Audio", Description: "The audio codecs"},
	{Name: "audio_channels", Group: "Audio", Description: "The channel counts"},
	{Name: "subtitle_languages", Group: "Subtitles", Description: "The subtitle languages"},
	{Name: "workflow", Group: "Run", Description: "The workflow's name"},
	{Name: "workflow_revision", Group: "Run", Description: "The workflow's revision number"},
	{Name: "worker", Group: "Run", Description: "The worker that ran the job"},
	{Name: "encoder", Group: "Run", Description: "The encoder used"},
	{Name: "target_codec", Group: "Run", Description: "The codec the new file uses"},
	{Name: "output_path", Group: "Run", Description: "Where the new file was saved"},
	{Name: "new_size_bytes", Group: "Run", Description: "The new file's size in bytes"},
	{Name: "saved_percent", Group: "Run", Description: "How much smaller the new file is"},
	{Name: "sub_lang", Group: "Subtitles", Description: "The subtitle's language, in the .srt name", FieldOnly: true},
	{Name: "sub_kind", Group: "Subtitles", Description: "The subtitle's kind (.forced or .sdh), in the .srt name", FieldOnly: true},
}

// BuiltinName reports whether name is a built-in (a name the engine sets, so a workflow can't).
func BuiltinName(name string) bool {
	for _, b := range Builtins {
		if b.Name == name {
			return true
		}
	}
	return false
}

// CheckVariables checks a workflow's own variables: each name is one a person may type, no name is a
// built-in, and no two are the same. The editor refuses these too; the server refuses them as well,
// so a draft saved another way can't break the engine's names.
func CheckVariables(d Doc) []Issue {
	var out []Issue
	seen := map[string]bool{}
	for _, v := range d.Variables {
		switch {
		case !NamePattern.MatchString(v.Name):
			out = append(out, Issue{Code: "invalid_variable_name", Severity: "error", Name: v.Name,
				Message: fmt.Sprintf("%s can't be a variable name: use lowercase letters, numbers and _, starting with a letter, up to 40 characters.", nameOrEmpty(v.Name))})
		case BuiltinName(v.Name):
			out = append(out, Issue{Code: "builtin_variable_name", Severity: "error", Name: v.Name,
				Message: fmt.Sprintf("{{%s}} is set by the server already, so a variable can't use that name.", v.Name)})
		case seen[v.Name]:
			out = append(out, Issue{Code: "duplicate_variable", Severity: "error", Name: v.Name,
				Message: fmt.Sprintf("Two variables are called {{%s}}. Give one of them another name.", v.Name)})
		}
		seen[v.Name] = true
	}
	return out
}

// nameOrEmpty is a name for a message: the name in quotes, or "A variable" when it is empty.
func nameOrEmpty(name string) string {
	if strings.TrimSpace(name) == "" {
		return "A variable's empty name"
	}
	return fmt.Sprintf("“%s”", name)
}
