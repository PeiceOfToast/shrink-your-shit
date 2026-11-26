// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// ForgetSettlingGone deletes the still-changing rows under one library folder whose file the scan did not see in its
// whole walk (copied, then renamed or deleted while it settled). Only those rows: no other decision, job or file.
func (s *Store) ForgetSettlingGone(libPath string, seen map[string]bool) error {
	rows, err := s.db.Query(`SELECT path FROM files WHERE reason=?`, rules.SkipStillChanging)
	if err != nil {
		return err
	}
	prefix := filepath.Clean(libPath) + string(filepath.Separator)
	var gone []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		if strings.HasPrefix(filepath.Clean(p), prefix) && !seen[p] {
			// only a file really gone: one under a folder the walk could not read is not seen, and is not gone
			if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
				gone = append(gone, p)
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, p := range gone {
		if _, err := s.db.Exec(`DELETE FROM files WHERE path=? AND reason=?`, p, rules.SkipStillChanging); err != nil {
			return err
		}
	}
	return nil
}
