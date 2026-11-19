// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package doctor

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// trashDir is worker.TrashDir (the doctor does not import the worker).
const trashDir = ".syp-trash"

// OrphanFolder is a .syp-trash folder holding originals the database does not know: kept by an
// earlier setup whose database was reset or lost. They are never purged and not listed under Kept
// originals, so they are only reported, never deleted, renamed or moved.
type OrphanFolder struct {
	Dir   string // the .syp-trash folder, full path
	Files int    // orphans that are not moved-aside outputs (below)
	Bytes int64
	// Moved are the names of earlier converted files that Convert again moved here because they had been
	// changed (worker trashPath: "<name>.<job id>.ext"). They are not orphans; each gets its own note.
	Moved []string
}

// movedAsideRE splits a trash name's stem: the base, then the job id that moved it, then an optional
// number when that name was taken (trashPath). The lazy base keeps "old.7.2" as base "old", id 7.
var movedAsideRE = regexp.MustCompile(`^(.+?)\.(\d+)(?:\.\d+)?$`)

// movedAside says whether name is an output that Convert again moved aside: its id belongs to a job whose
// output has the same base name. outputOf is jobs.Store.OutputOfJob; nil means there is no database to ask.
func movedAside(name string, outputOf func(id int64) (string, bool, error)) bool {
	if outputOf == nil {
		return false
	}
	ext := filepath.Ext(name)
	m := movedAsideRE.FindStringSubmatch(strings.TrimSuffix(name, ext))
	if m == nil {
		return false
	}
	id, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return false
	}
	out, ok, err := outputOf(id)
	return err == nil && ok && filepath.Base(out) == m[1]+ext
}

// MovedAsideMessage is the note for one earlier converted file that Convert again moved to the trash.
func MovedAsideMessage(name string) string {
	return name + " is an earlier converted file that had been changed; Convert again moved it here so the new file could take its place. Delete it when you no longer need it."
}

// MovedAsideNote is the one note for all of a library's moved-aside files: the single-file sentence for one,
// otherwise a count with up to five names.
func MovedAsideNote(names []string) string {
	if len(names) == 1 {
		return MovedAsideMessage(names[0])
	}
	shown, more := names, ""
	if len(names) > 5 {
		shown, more = names[:5], fmt.Sprintf(" and %d more", len(names)-5)
	}
	return fmt.Sprintf("%d earlier converted files had been changed, so Convert again moved them to the trash: %s%s. Delete them when you no longer need them.",
		len(names), strings.Join(shown, ", "), more)
}

// OrphanedOriginals lists the .syp-trash folders under root (one beside each converted file, so in
// subfolders too) that hold files not in known (jobs.Store.KnownOriginals). A file that movedAside names
// goes to Moved instead. It only reads. Slice 28's uninstall summary is meant to list these too, with this
// function.
func OrphanedOriginals(root string, known map[string]bool, outputOf func(id int64) (string, bool, error)) []OrphanFolder {
	// Both sides in one form: a path recorded through a linked folder (macOS /var is /private/var) is
	// the same file as the one the walk finds (review 8fd5cd5).
	canon := map[string]bool{}
	for k := range known {
		canon[canonFile(k)] = true
	}
	var out []OrphanFolder
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || d.Name() != trashDir {
			return nil
		}
		o := OrphanFolder{Dir: p}
		filepath.WalkDir(p, func(f string, e fs.DirEntry, err error) error {
			if err != nil || !e.Type().IsRegular() || canon[canonFile(f)] {
				return nil
			}
			if movedAside(e.Name(), outputOf) {
				o.Moved = append(o.Moved, e.Name())
				return nil
			}
			if info, err := e.Info(); err == nil {
				o.Files++
				o.Bytes += info.Size()
			}
			return nil
		})
		if o.Files > 0 || len(o.Moved) > 0 {
			out = append(out, o)
		}
		return fs.SkipDir
	})
	return out
}

// Message is design's sentence for the folder, with the real count, size and path.
func (o OrphanFolder) Message() string {
	if o.Files == 1 {
		return fmt.Sprintf("1 original file (%s) from an earlier setup is still in the hidden folder %s. It is not one of the originals kept by shrink-your-shit and will not be deleted automatically. Move it back or delete it yourself; we won't touch it.", rules.HumanBytes(o.Bytes), o.Dir)
	}
	return fmt.Sprintf("%d original files (%s) from an earlier setup are still in the hidden folder %s. They are not among the originals kept by shrink-your-shit and will not be deleted automatically. Move them back or delete them yourself; we won't touch them.", o.Files, rules.HumanBytes(o.Bytes), o.Dir)
}

// canonFile is p with its folder's links resolved (the file itself is not followed), compared without
// case where the system's names are not case sensitive.
func canonFile(p string) string {
	p = filepath.Clean(p)
	if dir, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		p = filepath.Join(dir, filepath.Base(p))
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		p = strings.ToLower(p)
	}
	return p
}
