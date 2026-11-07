// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package doctor

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
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
	Files int
	Bytes int64
}

// OrphanedOriginals lists the .syp-trash folders under root (one beside each converted file, so in
// subfolders too) that hold files not in known (jobs.Store.KnownOriginals). It only reads. Slice 28's
// uninstall summary is meant to list these too, with this function.
func OrphanedOriginals(root string, known map[string]bool) []OrphanFolder {
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
			if info, err := e.Info(); err == nil {
				o.Files++
				o.Bytes += info.Size()
			}
			return nil
		})
		if o.Files > 0 {
			out = append(out, o)
		}
		return fs.SkipDir
	})
	return out
}

// Message is design's sentence for the folder, with the real count, size and path.
func (o OrphanFolder) Message() string {
	if o.Files == 1 {
		return fmt.Sprintf("1 original file (%s) from an earlier setup is still in the hidden folder %s. It is not listed under Kept originals and will not be deleted automatically. Move it back or delete it yourself; we won't touch it.", rules.HumanBytes(o.Bytes), o.Dir)
	}
	return fmt.Sprintf("%d original files (%s) from an earlier setup are still in the hidden folder %s. They are not listed under Kept originals and will not be deleted automatically. Move them back or delete them yourself; we won't touch them.", o.Files, rules.HumanBytes(o.Bytes), o.Dir)
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
