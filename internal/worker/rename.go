// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// renameNoReplace moves src to dst only if nothing is at dst; otherwise it fails with an error that
// matches fs.ErrExist and moves nothing (review 172f049: a restore must never overwrite a file that
// appeared meanwhile). Linux: renameat2 RENAME_NOREPLACE; Windows: MoveFileEx without
// MOVEFILE_REPLACE_EXISTING; elsewhere (and on Linux filesystems without renameat2): a hard link, which
// fails if dst exists, then the source name removed (same filesystem; a crash between the two leaves
// both names on the same file, which recovery recognises).
func renameNoReplace(src, dst string) error { return renameNoReplaceOS(src, dst) }

func linkRename(src, dst string) error {
	if err := os.Link(src, dst); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return &os.LinkError{Op: "rename", Old: src, New: dst, Err: fs.ErrExist}
		}
		return err
	}
	if err := linkRemove(src); err != nil {
		return fmt.Errorf("%w: %w", errPlacedSourceLeft, err)
	}
	return nil
}

// errPlacedSourceLeft: linkRename put the file at dst (it is there, the same file), but the source name
// could not be removed. Callers that only need dst in place (an .srt beside the video) can go on.
var errPlacedSourceLeft = errors.New("in place, but the old name is still there")

// linkRemove is os.Remove; tests make it fail.
var linkRemove = os.Remove
