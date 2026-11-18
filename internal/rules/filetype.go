// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Changing the file type in replace mode (25a-2): the new file gets the original's name with the new
// extension, and the original's name is then free. Design's words.

// NameTaken is the job's message when the converted file's new name is taken (name: the full path).
func NameTaken(name string) string {
	return fmt.Sprintf("A file named %s is already there. Nothing was changed. Move or rename that file, then press Retry.", filepath.Base(name))
}

// RenamedAway is the 409 for "Convert again" on a done job whose file now has another name.
func RenamedAway(old, now string) string {
	return fmt.Sprintf("%s no longer exists: it was converted and is now %s. Nothing was changed.", filepath.Base(old), filepath.Base(now))
}

// RenameWarning is the setting's and the Preview's warning when replace mode changes the file type
// (fileType mkv or mp4); "" otherwise.
func RenameWarning(afterConverting, fileType string) string {
	if afterConverting != "replace" || fileType == "" || fileType == "keep" {
		return ""
	}
	from := "mkv"
	if fileType == "mkv" {
		from = "mp4"
	}
	return fmt.Sprintf("Changing the file type renames each file (movie.%s becomes movie.%s). Radarr, Sonarr, Plex or Jellyfin may lose track of the renamed files: after converting, rescan this library in those apps.", from, fileType)
}

// ReplaceName is where the converted file goes in replace mode: the original's name, or, when the file
// type changes (ext differs from the original's, ignoring case: movie.MKV stays movie.MKV), the same name
// with ext.
func ReplaceName(path, ext string) string {
	if strings.EqualFold(filepath.Ext(path), ext) {
		return path
	}
	return strings.TrimSuffix(path, filepath.Ext(path)) + ext
}
